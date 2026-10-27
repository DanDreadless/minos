package querylog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Derived tables are maintained from the query log so the reads users wait on
// never have to walk millions of rows: the domain dictionary (one row per
// distinct name with its hit counts and when it was last seen, so a search
// scans thousands of rows instead of millions and can answer "nothing
// matches" instantly), and the stats rollups in aggregate.go/rollups.go.
//
// Exactly-once is the invariant, because counts must not drift:
//   - Live writes update the derived tables in the same transaction as the
//     batch insert, and advance live_upto (the last row id written).
//   - Rows the derived tables have never seen — the whole log on first
//     upgrade, or rows an older Minos wrote after a downgrade — are listed as
//     pending id ranges. The backfill consumes them in chunks, each chunk's
//     upserts and its range update committed in one transaction.
//   - derivedVersion bumps when the derived schema changes; a mismatch drops
//     the derived tables and backfills everything again.
//
// Until the backfill finishes, readers use the raw-table paths (derivedReady).

// derivedVersion identifies the derived-table schema. Bump it whenever a
// derived table is added or its meaning changes.
const derivedVersion = 3 // 2: stats rollups; 3: daily tiers (roll_day, roll_day_blocked)

// backfillChunkRows per transaction: small enough that the writer lock is
// released often (the flush loop interleaves), large enough to be efficient.
// A variable so tests can force many chunks.
var backfillChunkRows int64 = 20000

const derivedSchema = `
CREATE TABLE IF NOT EXISTS querylog_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS qnames (
	name    TEXT PRIMARY KEY,
	hits    INTEGER NOT NULL,
	blocked INTEGER NOT NULL,
	last_ts INTEGER NOT NULL
) WITHOUT ROWID;`

// idRange is an inclusive range of querylog ids still to backfill.
type idRange [2]int64

// prepareDerived runs on the startup path, so it does only instant work:
// create the tables, reconcile the version, and record what is pending.
func prepareDerived(db *sql.DB) error {
	if _, err := db.Exec(derivedSchema + rollupSchema); err != nil {
		return fmt.Errorf("create derived tables: %w", err)
	}
	var version int
	switch err := db.QueryRow(`SELECT value FROM querylog_meta WHERE key = 'derived_version'`).Scan(&version); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read derived version: %w", err)
	}
	var maxID sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(id) FROM querylog`).Scan(&maxID); err != nil {
		return fmt.Errorf("read max id: %w", err)
	}
	if version == 2 {
		// v2 → v3 adds only the daily tiers, which are sums of the hourly
		// tables already here. Rather than drop everything and replay a log
		// of millions of rows (minutes on a Pi, with slow dashboards meanwhile),
		// mark them to be seeded from the hourly tables in the background
		// (seedDaily). The tables exist now (the schema above), so live
		// writes maintain them from this moment.
		if err := writeMeta(db, map[string]any{"derived_version": derivedVersion, "daily_seed": true}); err != nil {
			return err
		}
		version = derivedVersion
	}
	if version != derivedVersion {
		// New or changed derived schema: start the derived tables over and
		// backfill the whole log (DROP is instant; the rebuild is not, and
		// runs in the background).
		stmts := []string{`DELETE FROM querylog_meta`}
		for _, t := range derivedTables {
			stmts = append(stmts, `DROP TABLE IF EXISTS `+t)
		}
		for _, stmt := range stmts {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("reset derived tables: %w", err)
			}
		}
		if _, err := db.Exec(derivedSchema + rollupSchema); err != nil {
			return fmt.Errorf("recreate derived tables: %w", err)
		}
		var pending []idRange
		if maxID.Valid {
			pending = []idRange{{0, maxID.Int64}}
		}
		return writeMeta(db, map[string]any{
			"derived_version": derivedVersion,
			"live_upto":       maxID.Int64,
			"pending":         pending,
		})
	}
	// Same version: rows beyond live_upto were written by something that did
	// not maintain the derived tables (an older Minos after a downgrade).
	liveUpto, pending, err := readProgress(db)
	if err != nil {
		return err
	}
	if maxID.Valid && maxID.Int64 > liveUpto {
		pending = append(pending, idRange{liveUpto + 1, maxID.Int64})
		return writeMeta(db, map[string]any{"live_upto": maxID.Int64, "pending": pending})
	}
	return nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func writeMeta(db execer, kv map[string]any) error {
	for k, v := range kv {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encode meta %s: %w", k, err)
		}
		if _, err := db.Exec(`INSERT INTO querylog_meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, string(raw)); err != nil {
			return fmt.Errorf("write meta %s: %w", k, err)
		}
	}
	return nil
}

type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

func readProgress(db queryRower) (liveUpto int64, pending []idRange, err error) {
	var raw string
	if err := db.QueryRow(`SELECT value FROM querylog_meta WHERE key = 'live_upto'`).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &liveUpto)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, fmt.Errorf("read live_upto: %w", err)
	}
	if err := db.QueryRow(`SELECT value FROM querylog_meta WHERE key = 'pending'`).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &pending)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, fmt.Errorf("read pending: %w", err)
	}
	return liveUpto, pending, nil
}

// nameAgg is one name's contribution from a set of rows.
type nameAgg struct {
	hits, blocked int64
	lastTs        int64
}

func upsertNames(tx *sql.Tx, names map[string]*nameAgg) error {
	if len(names) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO qnames (name, hits, blocked, last_ts) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET hits = hits + excluded.hits,
			blocked = blocked + excluded.blocked, last_ts = MAX(last_ts, excluded.last_ts)`)
	if err != nil {
		return fmt.Errorf("prepare qnames upsert: %w", err)
	}
	defer stmt.Close()
	for name, a := range names {
		if _, err := stmt.Exec(name, a.hits, a.blocked, a.lastTs); err != nil {
			return fmt.Errorf("upsert qname: %w", err)
		}
	}
	return nil
}

// writeDerivedLive folds a just-inserted batch into the derived tables, in
// the batch's own transaction, and advances live_upto to lastID.
func writeDerivedLive(tx *sql.Tx, batch []Entry, lastID int64) error {
	agg := newDerivedAgg()
	for _, e := range batch {
		agg.add(e.Time.UnixMilli(), e.Client, e.QName, e.Verdict, e.List)
	}
	if err := agg.upsert(tx); err != nil {
		return err
	}
	return writeMeta(tx, map[string]any{"live_upto": lastID})
}

// backfillDerived consumes the pending ranges in chunks until none remain,
// then marks the derived tables ready. It runs on the writer connection, so
// each chunk serialises with the flush loop rather than fighting it.
func (l *Log) backfillDerived() {
	defer close(l.derivedDone)
	start := time.Now()
	total := int64(0)
	for {
		select {
		case <-l.done:
			return // shutting down; the ranges left resume next start
		default:
		}
		n, more, err := l.backfillChunk()
		if err != nil {
			slog.Error("query log backfill failed; search and stats use the slower paths", "err", err)
			return
		}
		total += n
		if !more {
			break
		}
		if total > 0 && total%(backfillChunkRows*25) == 0 {
			slog.Info("query log backfill progress", "rows", total)
		}
	}
	if total > 0 {
		slog.Info("query log backfill finished", "rows", total, "took", time.Since(start).Round(time.Millisecond))
	}
	if err := l.seedDaily(); err != nil {
		slog.Error("query log daily rollups could not be built; stats use the slower paths", "err", err)
		return
	}
	l.derivedReady.Store(true)
}

// seedDaily fills the daily tiers from the hourly tables after an upgrade
// that added them (prepareDerived's v2 → v3 step): one transaction on the
// writer connection, so it serialises with live batches — each of which
// updates both tiers — and the result is exact either way. It runs after
// the backfill, when the hourly tables hold every row. Measured on a Pi 4
// with 90 days (160k hourly rows): a few seconds, once.
func (l *Log) seedDaily() error {
	var pending bool
	switch err := l.db.QueryRow(`SELECT value FROM querylog_meta WHERE key = 'daily_seed'`).Scan(&pending); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("read daily seed flag: %w", err)
	}
	if !pending {
		return nil
	}
	start := time.Now()
	tx, err := l.db.Begin()
	if err != nil {
		return fmt.Errorf("begin daily seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		`DELETE FROM roll_day`,
		`INSERT INTO roll_day (bucket, client, verdict, list, n)
			SELECT bucket / 86400000 * 86400000, client, verdict, list, SUM(n) FROM roll_hour
			GROUP BY 1, client, verdict, list`,
		`DELETE FROM roll_day_blocked`,
		`INSERT INTO roll_day_blocked (bucket, qname, n)
			SELECT bucket / 86400000 * 86400000, qname, SUM(n) FROM roll_hour_blocked GROUP BY 1, qname`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("daily seed: %w", err)
		}
	}
	if err := writeMeta(tx, map[string]any{"daily_seed": false}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit daily seed: %w", err)
	}
	slog.Info("query log daily rollups built", "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// backfillChunk processes up to backfillChunk ids from the first pending
// range: aggregate, upsert, and shrink the range — one transaction.
func (l *Log) backfillChunk() (rows int64, more bool, err error) {
	tx, err := l.db.Begin()
	if err != nil {
		return 0, false, fmt.Errorf("begin backfill: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	_, pending, err := readProgress(tx)
	if err != nil {
		return 0, false, err
	}
	for len(pending) > 0 && pending[0][0] > pending[0][1] {
		pending = pending[1:]
	}
	if len(pending) == 0 {
		if err := writeMeta(tx, map[string]any{"pending": []idRange{}}); err != nil {
			return 0, false, err
		}
		return 0, false, tx.Commit()
	}
	lo := pending[0][0]
	hi := min(lo+backfillChunkRows-1, pending[0][1])
	agg := newDerivedAgg()
	r, err := tx.Query(`SELECT ts, client, qname, verdict, list FROM querylog WHERE id BETWEEN ? AND ?`, lo, hi)
	if err != nil {
		return 0, false, fmt.Errorf("backfill read: %w", err)
	}
	for r.Next() {
		var ts int64
		var c, q, v, list string
		if err := r.Scan(&ts, &c, &q, &v, &list); err != nil {
			r.Close()
			return 0, false, fmt.Errorf("backfill scan: %w", err)
		}
		rows++
		agg.add(ts, c, q, v, list)
	}
	if err := r.Err(); err != nil {
		r.Close()
		return 0, false, fmt.Errorf("backfill read: %w", err)
	}
	r.Close()
	if err := agg.upsert(tx); err != nil {
		return 0, false, err
	}
	pending[0][0] = hi + 1
	if err := writeMeta(tx, map[string]any{"pending": pending}); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit backfill: %w", err)
	}
	return rows, true, nil
}

// pruneDerived drops dictionary names last seen before the retention cutoff,
// so the dictionary stays bounded by retention like the log itself. Hit
// counts for surviving names may still include pruned rows; they are only
// ever used as an upper-bound estimate, which errs towards the safe path.
func (l *Log) pruneDerived(cutoffMs int64) {
	now := time.Now().UnixMilli()
	for _, p := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM qnames WHERE last_ts < ?`, cutoffMs},
		// Rollup buckets fully before the cutoff; the bucket straddling it
		// stays, so rollups may hold up to one bucket more than the log.
		{`DELETE FROM roll_hour WHERE bucket < ?`, cutoffMs/hourMs*hourMs - hourMs},
		{`DELETE FROM roll_hour_blocked WHERE bucket < ?`, cutoffMs/hourMs*hourMs - hourMs},
		{`DELETE FROM roll_day_client WHERE bucket < ?`, cutoffMs/dayMs*dayMs - dayMs},
		{`DELETE FROM roll_day WHERE bucket < ?`, cutoffMs/dayMs*dayMs - dayMs},
		{`DELETE FROM roll_day_blocked WHERE bucket < ?`, cutoffMs/dayMs*dayMs - dayMs},
		{`DELETE FROM roll_10m WHERE bucket < ?`, now - roll10mKeepMs},
	} {
		if _, err := l.db.Exec(p.sql, p.arg); err != nil {
			slog.Warn("query log derived prune failed", "err", err)
		}
	}
}

// nameMatch is the dictionary's answer for a search term.
type nameMatch struct {
	names   []string
	hits    int64 // upper bound on matching rows (any verdict)
	blocked int64 // upper bound on matching blocked rows
	capped  bool  // more than maxDictNames matched: treat the term as common
}

// maxDictNames bounds how many names a search will bind into an IN list.
// More than this many distinct names matching is a common term, which the
// newest-first scan answers quickly anyway.
const maxDictNames = 2000

func (l *Log) matchNames(ctx context.Context, term string) (nameMatch, error) {
	rows, err := l.reader().QueryContext(ctx, `SELECT name, hits, blocked FROM qnames
		WHERE name LIKE ? ESCAPE '\' LIMIT ?`, "%"+escapeLike(term)+"%", maxDictNames+1)
	if err != nil {
		return nameMatch{}, fmt.Errorf("match names: %w", err)
	}
	defer rows.Close()
	var m nameMatch
	for rows.Next() {
		var name string
		var hits, blocked int64
		if err := rows.Scan(&name, &hits, &blocked); err != nil {
			return nameMatch{}, fmt.Errorf("scan names: %w", err)
		}
		if len(m.names) == maxDictNames {
			m.capped = true
			break
		}
		m.names = append(m.names, name)
		m.hits += hits
		m.blocked += blocked
	}
	return m, rows.Err()
}

// matchName is the dictionary entry for one exact name — no names when the
// log never saw it.
func (l *Log) matchName(ctx context.Context, name string) (nameMatch, error) {
	var m nameMatch
	err := l.reader().QueryRowContext(ctx, `SELECT hits, blocked FROM qnames WHERE name = ?`, name).
		Scan(&m.hits, &m.blocked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nameMatch{}, nil
	case err != nil:
		return nameMatch{}, fmt.Errorf("match name: %w", err)
	}
	m.names = []string{name}
	return m, nil
}

// knownClients lists every distinct client in the log by skip-walking the
// client index — O(clients) seeks, never O(rows). Cached briefly: the set
// changes when a new device appears, not per search.
func (l *Log) knownClients(ctx context.Context) ([]string, error) {
	l.clientsMu.Lock()
	defer l.clientsMu.Unlock()
	if time.Since(l.clientsAt) < time.Minute {
		return l.clientsCache, nil
	}
	var out []string
	next := ""
	for {
		var c sql.NullString
		if err := l.reader().QueryRowContext(ctx,
			`SELECT MIN(client) FROM querylog WHERE client > ?`, next).Scan(&c); err != nil {
			return nil, fmt.Errorf("list clients: %w", err)
		}
		if !c.Valid {
			break
		}
		out = append(out, c.String)
		next = c.String
	}
	sort.Strings(out)
	l.clientsCache, l.clientsAt = out, time.Now()
	return out, nil
}

// clientLike reports whether a search term could be part of a client
// address. Clients are stored as IP literals, so a term with any letter
// beyond hex cannot match one — "apple.com" never needs the client side.
func clientLike(term string) bool {
	return strings.IndexFunc(term, notAddressRune) < 0
}

func notAddressRune(r rune) bool {
	addr := r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' || r == '.' || r == ':'
	return !addr
}
