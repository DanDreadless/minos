// Package querylog records every judged query. The hot path does one
// non-blocking channel send. Two goroutines take it from there, and the
// split between them matters: run() owns the in-memory ring (which feeds
// the live UI) and the WebSocket fan-out and touches SQLite nowhere, while
// flushLoop owns every writer-side database call — batched inserts, never
// per query, to keep SD cards alive, plus the retention prune. Anything
// holding SQLite's write lock therefore costs entries, never liveness.
package querylog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Entry is one judged query. Field names are the literal API vocabulary:
// verdict is "blocked" or "allowed" (the UI may dress them up, we don't).
type Entry struct {
	Time       time.Time `json:"time"`
	Client     string    `json:"client"`
	QName      string    `json:"qname"`
	QType      string    `json:"qtype"`
	Verdict    string    `json:"verdict"`
	List       string    `json:"list,omitempty"`
	Rule       string    `json:"rule,omitempty"`
	Upstream   string    `json:"upstream,omitempty"`
	DurationMs float64   `json:"duration_ms"`
	// AuditList/AuditRule mark an allowed query an audit-mode list would
	// have condemned ("would block") — attribution without enforcement.
	AuditList string `json:"audit_list,omitempty"`
	AuditRule string `json:"audit_rule,omitempty"`
	// DNSSEC is the validation outcome for this answer — one of the
	// DNSSEC* constants — so the dashboard's counters can be drilled into
	// rather than only counted. Empty when validation is off, when the
	// answer was never judged (a block, a local record, a route), or on a
	// cache hit: hits are not re-validated, so this column records
	// resolutions, not lookups.
	DNSSEC string `json:"dnssec,omitempty"`
}

// DNSSEC outcomes, mirroring the validator's states (RFC 4033). Text, to
// match list/audit_list, rather than a numeric code nobody could read
// straight out of the table.
const (
	DNSSECSecure        = "secure"
	DNSSECInsecure      = "insecure"
	DNSSECBogus         = "bogus"
	DNSSECIndeterminate = "indeterminate"
)

const (
	VerdictBlocked = "blocked"
	VerdictAllowed = "allowed"

	flushInterval = 30 * time.Second
	flushBatch    = 500
	subBuffer     = 256
	// flushQueue is how many completed batches may be in flight to the
	// disk writer before run() starts holding them in memory instead.
	flushQueue = 4
	// maxPending caps what run() will hold while the writer is behind
	// (~1 MB of entries). Beyond it the oldest are dropped and counted,
	// exactly as Record does when its own channel fills.
	maxPending = 10 * flushBatch
	// shutdownFlushWait bounds each wait on the disk writer at shutdown.
	shutdownFlushWait = 5 * time.Second

	// searchDeadline caps a free-text history search: an unindexable LIKE
	// scan with no matches would otherwise read the entire table.
	searchDeadline = 15 * time.Second
)

// ErrSearchTimeout is returned when a free-text search exceeds its deadline.
var ErrSearchTimeout = errors.New(
	"search took too long — try a narrower term, or use the list and device filters, which are indexed")

// Options configures a Log.
type Options struct {
	// RingSize is the in-memory buffer length backing the live UI.
	RingSize int
	// DBPath is the SQLite file; ignored when Ephemeral.
	DBPath string
	// Ephemeral disables disk persistence entirely.
	Ephemeral bool
	// RetentionDays bounds how long rows live in SQLite.
	RetentionDays int
	// MemoryMB caps the memory SQLite may use for the query log (page
	// caches, sorts, aggregates). 0 = DefaultMemoryMB.
	MemoryMB int
}

// DefaultMemoryMB is the query log's memory cap when none is configured:
// ample for a household log, and a small slice of even a 1 GB Pi.
const DefaultMemoryMB = 32

// Log is safe for concurrent use. Record never blocks the caller.
type Log struct {
	ch   chan Entry
	done chan struct{} // closed to stop the writer
	dead chan struct{} // closed when the writer has flushed and exited

	ringMu sync.RWMutex
	ring   []Entry
	head   int // next write position
	count  int

	subMu sync.Mutex
	subs  map[chan Entry]struct{}

	// db is the writer: one connection, so inserts, prunes and schema
	// migrations serialise against each other rather than inviting
	// SQLITE_BUSY. rdb is a separate pool the reads use — WAL admits
	// concurrent readers alongside the single writer, so an aggregate that
	// walks the whole window can no longer stall the flush, the websocket,
	// or every other API call behind it. Both are nil in ephemeral mode.
	db        *sql.DB
	rdb       atomic.Pointer[sql.DB] // swapped when the memory cap changes
	dbPath    string
	memoryMB  atomic.Int64
	retiredMu sync.Mutex
	retired   []*sql.DB    // replaced reader pools awaiting their grace period
	retention atomic.Int64 // nanoseconds; settable at runtime

	// flushCh carries completed batches from run() to flushLoop, which is
	// the only goroutine that writes. Nil in ephemeral mode.
	flushCh chan []Entry
	// flushDead closes when flushLoop has exited.
	flushDead chan struct{}

	// indexed reports whether migrateIndexes has finished. Index builds run
	// in the background (they take minutes on a large SD-card log), so the
	// one query that names an index explicitly has to ask before doing so.
	indexed atomic.Bool
	// ready closes when the background migration is done, whether it
	// succeeded or not. Callers that only want the fast path — the device
	// hydration at startup — wait on it; nothing else has to.
	ready chan struct{}

	// derivedReady reports that the derived tables (derived.go) cover the
	// whole log, so searches may trust the dictionary. derivedDone closes
	// when the backfill goroutine exits, ready or not.
	derivedReady atomic.Bool
	derivedDone  chan struct{}
	clientsMu    sync.Mutex
	clientsAt    time.Time
	clientsCache []string

	total   atomic.Uint64
	blocked atomic.Uint64
	dropped atomic.Uint64

	closeOnce sync.Once
}

func Open(opts Options) (*Log, error) {
	if opts.RingSize <= 0 {
		opts.RingSize = 10000
	}
	l := &Log{
		ch:    make(chan Entry, 4096),
		done:  make(chan struct{}),
		dead:  make(chan struct{}),
		ring:  make([]Entry, opts.RingSize),
		subs:  make(map[chan Entry]struct{}),
		ready: make(chan struct{}),

		derivedDone: make(chan struct{}),
	}
	l.retention.Store(int64(time.Duration(opts.RetentionDays) * 24 * time.Hour))
	if opts.MemoryMB <= 0 {
		opts.MemoryMB = DefaultMemoryMB
	}
	l.memoryMB.Store(int64(opts.MemoryMB))
	if opts.Ephemeral {
		close(l.ready)
		close(l.derivedDone)
		go l.run()
		return l, nil
	}
	db, err := openDB(opts.DBPath, opts.MemoryMB)
	if err != nil {
		return nil, err
	}
	rdb, err := openReader(opts.DBPath, opts.MemoryMB)
	if err != nil {
		db.Close()
		return nil, err
	}
	l.db, l.dbPath = db, opts.DBPath
	l.rdb.Store(rdb)
	l.flushCh = make(chan []Entry, flushQueue)
	l.flushDead = make(chan struct{})
	// Indexes are built off the startup path: on a log of any size the
	// build takes minutes, and it used to run before the DNS and HTTP
	// listeners came up — the whole machine silent while it worked.
	go func() {
		l.buildIndexes()
		l.backfillDerived() // after the indexes: one writer-lock user at a time
	}()
	go l.flushLoop()
	go l.run()
	return l, nil
}

// buildIndexes runs the index migration in the background and publishes the
// result. It uses the writer handle, so it serialises with flushes rather
// than fighting them; entries buffer in the channel meanwhile.
func (l *Log) buildIndexes() {
	defer close(l.ready)
	if err := migrateIndexes(l.db); err != nil {
		// Not fatal: every query still runs, some by a worse plan. A
		// failed build is worth shouting about but not worth refusing
		// to resolve DNS over.
		slog.Error("query log index migration failed", "err", err)
		return
	}
	l.indexed.Store(true)
}

// Ready returns a channel closed once the background index migration has
// finished (successfully or not). Reads never need to wait on it; the
// startup device hydration does, because the index it builds is what makes
// that scan cheap.
func (l *Log) Ready() <-chan struct{} { return l.ready }

// dsnPragmas are applied to every connection in a pool. busy_timeout is the
// one that matters now there is more than one connection: a reader that
// arrives mid-checkpoint, or during an index build, waits instead of
// failing outright.
const dsnPragmas = "?_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"

// readerConns + the writer share the memory cap's page-cache budget.
const readerConns = 4

// cacheKiB splits the memory cap evenly over every connection Minos holds
// open (1 writer + readerConns), so the page caches together stay inside it.
func cacheKiB(memoryMB int) int { return max(memoryMB*1024/(readerConns+1), 256) }

// dsn builds the per-connection settings that bound the query log's memory.
//
// cache_size is the lever that is actually enforced: a hard cap on each
// connection's page cache, which SQLite's sorter and the temporary b-trees
// behind GROUP BY also work within. temp_store=FILE makes anything larger
// spill to a temporary file instead of RAM — slower, never hungrier — and
// mmap_size=0 keeps the file out of the address space. What we deliberately
// do not rely on: soft/hard_heap_limit. The pure-Go driver is compiled with
// SQLITE_DEFAULT_MEMSTATUS=0, under which SQLite stores those limits and
// silently never enforces them (verified: a 1 MB hard limit refused nothing).
func dsn(path string, memoryMB int) string {
	return path + dsnPragmas + fmt.Sprintf(
		"&_pragma=cache_size(-%d)&_pragma=temp_store(FILE)&_pragma=mmap_size(0)", cacheKiB(memoryMB))
}

// reader returns the current read pool (nil in ephemeral mode).
func (l *Log) reader() *sql.DB { return l.rdb.Load() }

// SetMemoryMB changes the memory cap live: the writer's page cache is
// resized in place, and the reader pool is replaced by one opened at the new
// size. The old pool is closed a minute later — longer than any read's
// deadline — so a query that picked it up just before the swap always
// finishes (and Close closes it sooner on shutdown).
func (l *Log) SetMemoryMB(mb int) {
	if mb <= 0 {
		mb = DefaultMemoryMB
	}
	if l.db == nil || int64(mb) == l.memoryMB.Swap(int64(mb)) {
		return
	}
	if _, err := l.db.Exec(fmt.Sprintf("PRAGMA cache_size=-%d", cacheKiB(mb))); err != nil {
		slog.Warn("query log writer cache resize failed", "err", err)
	}
	fresh, err := openReader(l.dbPath, mb)
	if err != nil {
		slog.Warn("query log reader pool not resized", "err", err)
		return
	}
	old := l.rdb.Swap(fresh)
	l.retiredMu.Lock()
	l.retired = append(l.retired, old)
	l.retiredMu.Unlock()
	go func() {
		time.Sleep(time.Minute)
		l.retiredMu.Lock()
		defer l.retiredMu.Unlock()
		for i, p := range l.retired {
			if p == old {
				l.retired = append(l.retired[:i], l.retired[i+1:]...)
				_ = old.Close()
				return
			}
		}
	}()
	slog.Info("query log memory limit changed", "mb", mb)
}

// openReader opens the read-only-by-convention pool. It shares the file
// with the writer; WAL keeps the two out of each other's way. The pool is
// small on purpose — these are Raspberry Pis, and four concurrent
// full-window scans would thrash the SD card rather than finish sooner.
func openReader(path string, memoryMB int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, memoryMB))
	if err != nil {
		return nil, fmt.Errorf("open query log db (reader): %w", err)
	}
	db.SetMaxOpenConns(readerConns)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open query log db (reader): %w", err)
	}
	return db, nil
}

func openDB(path string, memoryMB int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, memoryMB))
	if err != nil {
		return nil, fmt.Errorf("open query log db: %w", err)
	}
	// One writer connection; a second would only invite SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	// journal_mode is a property of the file, not the connection, so it is
	// set once here and every later connection — including the reader
	// pool's — inherits it.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply PRAGMA journal_mode=WAL: %w", err)
	}
	const schema = `
CREATE TABLE IF NOT EXISTS querylog (
	id          INTEGER PRIMARY KEY,
	ts          INTEGER NOT NULL,
	client      TEXT NOT NULL,
	qname       TEXT NOT NULL,
	qtype       TEXT NOT NULL,
	verdict     TEXT NOT NULL,
	list        TEXT NOT NULL DEFAULT '',
	rule        TEXT NOT NULL DEFAULT '',
	upstream    TEXT NOT NULL DEFAULT '',
	duration_ms REAL NOT NULL DEFAULT 0,
	audit_list  TEXT NOT NULL DEFAULT '',
	audit_rule  TEXT NOT NULL DEFAULT '',
	dnssec      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_querylog_ts ON querylog(ts);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create query log schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := prepareDerived(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate adds columns introduced after a database was first created.
// Idempotent; ALTER TABLE ADD COLUMN is instant in SQLite (no table
// rewrite), so old query logs upgrade in place — SD-card safe.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(querylog)`)
	if err != nil {
		return fmt.Errorf("inspect query log schema: %w", err)
	}
	defer rows.Close()
	have := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan query log schema: %w", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for col, ddl := range map[string]string{
		"audit_list": `ALTER TABLE querylog ADD COLUMN audit_list TEXT NOT NULL DEFAULT ''`,
		"audit_rule": `ALTER TABLE querylog ADD COLUMN audit_rule TEXT NOT NULL DEFAULT ''`,
		// Deliberately no index for this one. Every drill-down is
		// time-bounded so it rides (ts) already, and the column is
		// low-selectivity — most answers are insecure, i.e. unsigned —
		// so an index would cost the measured ~45% file growth to serve
		// the case it helps least.
		"dnssec": `ALTER TABLE querylog ADD COLUMN dnssec TEXT NOT NULL DEFAULT ''`,
	} {
		if have[col] {
			continue
		}
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("add query log column %s: %w", col, err)
		}
	}
	return nil
}

// migrateIndexes builds indexes introduced after a database was first
// created. Unlike ADD COLUMN, building an index on a large existing log is
// NOT instant — minutes on a Pi reading a 90-day SD-card database — so it
// runs in the background (see buildIndexes), is announced in the log, and
// every read that needs it is fast forever after. It must not go back on
// the startup path: that is exactly the stall this file used to impose on
// every restart. The client/list indexes back the device page and the
// Docket's list filter, which otherwise walk the whole time index hunting
// for a sparse client or list (seconds per page on SD).
//
// Deliberately NOT partial indexes (with a WHERE excluding the empty
// string): the reads bind the name as a parameter, and SQLite can only use
// a partial index when the query's constraints provably imply the index
// predicate — a bound parameter never does, so a partial index here is
// silently ignored and the scan comes back. The empty-string entries cost
// some size; correctness of the plan beats it.
func migrateIndexes(db *sql.DB) error {
	have := make(map[string]bool)
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'index'`)
	if err != nil {
		return fmt.Errorf("inspect query log indexes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan query log indexes: %w", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var built []string
	for _, idx := range []struct{ name, ddl string }{
		// (client, ts, verdict) rather than (client, ts): the trailing
		// column is what makes the device summary a covering scan.
		// Without it the startup hydration reads every row of the table
		// to answer SUM(verdict = 'blocked') — 39s on a 7.4M-row log,
		// against 1.0s with it. (client, ts) is a prefix of this, so the
		// device drill-downs it used to serve are served by this too and
		// the narrower index is dropped below.
		{"idx_querylog_client_ts_verdict", `CREATE INDEX idx_querylog_client_ts_verdict ON querylog(client, ts, verdict)`},
		{"idx_querylog_list_ts", `CREATE INDEX idx_querylog_list_ts ON querylog(list, ts)`},
		{"idx_querylog_audit_ts", `CREATE INDEX idx_querylog_audit_ts ON querylog(audit_list, ts)`},
		// Serves a search for names the dictionary says are rare: fetch
		// exactly their rows instead of scanning the log for them. verdict
		// sits before ts so a verdict-filtered search ("netflix, blocked")
		// reads only the rows it returns — with (qname, ts) it visited every
		// netflix row to find 36 blocked ones (2.5 s on the field Pi).
		{"idx_querylog_qname_verdict_ts", `CREATE INDEX idx_querylog_qname_verdict_ts ON querylog(qname, verdict, ts)`},
	} {
		if have[idx.name] {
			continue
		}
		slog.Info("building query log index (one-time; may take a while on a large log)", "index", idx.name)
		start := time.Now()
		if _, err := db.Exec(idx.ddl); err != nil {
			return fmt.Errorf("build query log index %s: %w", idx.name, err)
		}
		slog.Info("query log index built", "index", idx.name, "took", time.Since(start).Round(time.Millisecond))
		built = append(built, idx.name)
	}
	// idx_querylog_client_ts is a strict prefix of the widened index above,
	// so once that exists the narrow one earns nothing and costs ~12% of
	// the file. Dropped after the build, never before: a crash in between
	// leaves the old index in place and the next start rebuilds from
	// there, which is the safe direction to fail in.
	if have["idx_querylog_client_ts"] {
		slog.Info("dropping superseded query log index", "index", "idx_querylog_client_ts")
		if _, err := db.Exec(`DROP INDEX idx_querylog_client_ts`); err != nil {
			return fmt.Errorf("drop superseded index idx_querylog_client_ts: %w", err)
		}
	}
	// Fresh indexes get planner statistics once (the query planner falls
	// back to guesses without sqlite_stat1, which can flip a query onto the
	// wrong index as the data grows); PRAGMA optimize in the daily prune
	// keeps them current from then on. Only the indexes just built: a whole
	// ANALYZE re-reads every index and took 2m18s on a 13.4M-row Pi log.
	for _, name := range built {
		start := time.Now()
		if _, err := db.Exec(`ANALYZE ` + name); err != nil {
			return fmt.Errorf("analyze %s: %w", name, err)
		}
		slog.Info("query log index analyzed", "index", name, "took", time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// Record enqueues an entry. It never blocks: if the writer is behind, the
// entry is dropped and counted — latency beats completeness on the hot path.
func (l *Log) Record(e Entry) {
	l.total.Add(1)
	if e.Verdict == VerdictBlocked {
		l.blocked.Add(1)
	}
	select {
	case l.ch <- e:
	default:
		l.dropped.Add(1)
	}
}

// Stats returns lifetime counters (since process start).
func (l *Log) Stats() (total, blocked, dropped uint64) {
	return l.total.Load(), l.blocked.Load(), l.dropped.Load()
}

// Recent returns up to n of the newest entries, newest first.
func (l *Log) Recent(n int) []Entry {
	l.ringMu.RLock()
	defer l.ringMu.RUnlock()
	if n <= 0 || n > l.count {
		n = l.count
	}
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		idx := (l.head - 1 - i + len(l.ring)*2) % len(l.ring)
		out = append(out, l.ring[idx])
	}
	return out
}

// Subscribe returns a channel of live entries and a cancel function.
// Slow subscribers lose entries rather than stalling the writer.
func (l *Log) Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, subBuffer)
	l.subMu.Lock()
	l.subs[ch] = struct{}{}
	l.subMu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			l.subMu.Lock()
			delete(l.subs, ch)
			l.subMu.Unlock()
		})
	}
	return ch, cancel
}

// SetRetentionDays changes how long rows live in SQLite. Takes effect at the
// next prune cycle; safe to call while running.
func (l *Log) SetRetentionDays(days int) {
	l.retention.Store(int64(time.Duration(days) * 24 * time.Hour))
}

// Resize changes the ring buffer capacity at runtime, preserving the newest
// entries. Safe to call while running; not on the hot path.
func (l *Log) Resize(n int) {
	if n <= 0 {
		return
	}
	l.ringMu.Lock()
	defer l.ringMu.Unlock()
	if n == len(l.ring) {
		return
	}
	keep := l.count
	if keep > n {
		keep = n
	}
	fresh := make([]Entry, n)
	// Copy the newest `keep` entries oldest-first so head/count stay simple.
	for i := 0; i < keep; i++ {
		idx := (l.head - keep + i + len(l.ring)*2) % len(l.ring)
		fresh[i] = l.ring[idx]
	}
	l.ring = fresh
	l.head = keep % n
	l.count = keep
}

// Close stops the writer, flushing any buffered entries to disk first.
func (l *Log) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	<-l.dead
	// The backfill stops between chunks once done is closed; wait for the
	// chunk in hand (bounded — a stuck one must not turn shutdown into a hang).
	select {
	case <-l.derivedDone:
	case <-time.After(shutdownFlushWait):
	}
	// Readers first: the writer holds the file's WAL, and an in-flight
	// aggregate should be let go of before it is closed under it.
	var err error
	if r := l.reader(); r != nil {
		err = r.Close()
	}
	l.retiredMu.Lock()
	for _, p := range l.retired {
		_ = p.Close()
	}
	l.retired = nil
	l.retiredMu.Unlock()
	if l.db != nil {
		if dbErr := l.db.Close(); err == nil {
			err = dbErr
		}
	}
	return err
}

// run owns the in-memory side of the log: the ring buffer the live UI reads
// and the fan-out to WebSocket subscribers. It touches SQLite nowhere.
//
// That separation is the point. Disk writes used to happen inline here, so
// anything that held SQLite's write lock stalled the ring and the WebSocket
// with it — and an index build holds that lock for minutes on a large log,
// which made the Docket look frozen and DNS look dead while it was in fact
// resolving perfectly. Batches are handed to flushLoop instead, and a
// writer that falls behind now costs entries, never liveness.
func (l *Log) run() {
	defer close(l.dead)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]Entry, 0, flushBatch)
	// flush hands the batch to the disk writer without ever waiting for it.
	// If the writer is behind, entries keep accumulating here up to
	// maxPending and the oldest are dropped beyond that — the same trade
	// Record already makes on a full channel, and counted the same way.
	flush := func() {
		if l.db == nil || len(batch) == 0 {
			return
		}
		select {
		case l.flushCh <- batch:
			batch = make([]Entry, 0, flushBatch) // the writer owns that one now
		default:
			if len(batch) >= maxPending {
				drop := len(batch) - maxPending + flushBatch
				l.dropped.Add(uint64(drop))
				slog.Warn("query log writer is behind; dropping oldest entries",
					"dropped", drop, "pending", len(batch))
				batch = append(batch[:0], batch[drop:]...)
			}
		}
	}

	for {
		select {
		case e := <-l.ch:
			l.append(e)
			l.fanOut(e)
			if l.db != nil {
				batch = append(batch, e)
				if len(batch) >= flushBatch {
					flush()
				}
			}
		case <-ticker.C:
			flush()
		case <-l.done:
			// Drain whatever is already queued, then hand over the tail.
			for {
				select {
				case e := <-l.ch:
					l.append(e)
					l.fanOut(e)
					if l.db != nil {
						batch = append(batch, e)
					}
				default:
					l.finalFlush(batch)
					return
				}
			}
		}
	}
}

// flushLoop owns every writer-side SQLite operation: batch inserts and the
// daily prune. One goroutine, so they serialise with each other rather than
// contending for the single writer connection, and neither can reach the
// ring. Index migration runs elsewhere but shares that connection, so a
// build in progress simply delays this loop — which is now harmless.
func (l *Log) flushLoop() {
	defer close(l.flushDead)
	pruneTicker := time.NewTicker(24 * time.Hour)
	defer pruneTicker.Stop()
	l.prune()
	for {
		select {
		case batch, ok := <-l.flushCh:
			if !ok {
				return
			}
			if err := l.writeBatch(batch); err != nil {
				slog.Error("query log flush failed", "err", err, "entries", len(batch))
			}
		case <-pruneTicker.C:
			l.prune()
		}
	}
}

// finalFlush hands the last batch over on the way out, bounded on both
// waits. A migration can hold the write lock for minutes, and a shutdown
// that waits on it reads as a hang and ends in SIGKILL; losing the tail of
// the query log is the better trade.
func (l *Log) finalFlush(batch []Entry) {
	if l.db == nil {
		return
	}
	if len(batch) > 0 {
		select {
		case l.flushCh <- batch:
		case <-time.After(shutdownFlushWait):
			l.dropped.Add(uint64(len(batch)))
			slog.Warn("query log: writer busy at shutdown, dropping unflushed entries",
				"entries", len(batch))
		}
	}
	close(l.flushCh)
	select {
	case <-l.flushDead:
	case <-time.After(shutdownFlushWait):
		slog.Warn("query log: disk writer still busy at shutdown, leaving it")
	}
}

func (l *Log) append(e Entry) {
	l.ringMu.Lock()
	l.ring[l.head] = e
	l.head = (l.head + 1) % len(l.ring)
	if l.count < len(l.ring) {
		l.count++
	}
	l.ringMu.Unlock()
}

func (l *Log) fanOut(e Entry) {
	l.subMu.Lock()
	for ch := range l.subs {
		select {
		case ch <- e:
		default: // slow subscriber: drop for them, never stall
		}
	}
	l.subMu.Unlock()
}

func (l *Log) writeBatch(batch []Entry) error {
	tx, err := l.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	stmt, err := tx.Prepare(`INSERT INTO querylog
		(ts, client, qname, qtype, verdict, list, rule, upstream, duration_ms, audit_list, audit_rule, dnssec)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()
	var lastID int64
	for _, e := range batch {
		res, err := stmt.Exec(e.Time.UnixMilli(), e.Client, e.QName, e.QType,
			e.Verdict, e.List, e.Rule, e.Upstream, e.DurationMs,
			e.AuditList, e.AuditRule, e.DNSSEC)
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}
		if lastID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("insert id: %w", err)
		}
	}
	// Same transaction: the derived tables can never disagree with the log.
	if err := writeDerivedLive(tx, batch, lastID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (l *Log) prune() {
	retention := time.Duration(l.retention.Load())
	if l.db == nil || retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-retention).UnixMilli()
	if _, err := l.db.Exec(`DELETE FROM querylog WHERE ts < ?`, cutoff); err != nil {
		slog.Error("query log prune failed", "err", err)
	}
	l.pruneDerived(cutoff)
	// Refresh planner statistics on the daily cadence (SQLite's targeted,
	// usually-no-op ANALYZE) so index choices stay right as the log grows.
	if _, err := l.db.Exec(`PRAGMA optimize`); err != nil {
		slog.Debug("query log optimize failed", "err", err)
	}
}

// HistoryFilter narrows QueryHistory. Empty fields impose no constraint.
type HistoryFilter struct {
	// Search matches (case-insensitively) as a substring of qname OR client —
	// the free-text match the Docket search box uses.
	Search string
	// Verdict is "blocked", "allowed", or "" for both.
	Verdict string
	// Clients, when non-empty, restricts to these exact client IPs — the
	// device drill-down, where one physical device may have several IPs.
	// Distinct from the substring Search.
	Clients []string
	// WouldBlock restricts to entries an audit-mode list flagged
	// ("would block"): allowed queries carrying an audit attribution.
	WouldBlock bool
	// List restricts to entries attributed to this exact list name —
	// enforcing (blocked, or allowed via a pardon list) or audit
	// ("would block"). Matches the Docket's List column semantics.
	List string
	// DNSSEC restricts to answers with this validation outcome (one of the
	// DNSSEC* constants) — the dashboard's counter drill-down. Deliberately
	// unindexed: callers always bound it by time, so it rides (ts), and the
	// column is too low-selectivity for an index to earn its cost.
	DNSSEC string
}

// searchPlan is how QueryHistory will answer a free-text search.
type searchPlan int

const (
	planScan  searchPlan = iota // newest-first scan with a LIKE filter (common terms)
	planNames                   // exact rows of the few names that match, via idx_querylog_qname_verdict_ts
	planEmpty                   // the dictionary proves nothing matches
)

// namesPlanMaxRows: above this many matching rows, fetching them all and
// sorting costs more than scanning newest-first until the page is full.
const namesPlanMaxRows = 50000

// planSearch chooses how to answer f.Search. The dictionary of distinct
// names (thousands of rows, not millions) says which names match and
// roughly how often, which turns the two pathological cases — a term that
// matches nothing, and one that matches only rarely — from a 15 s scan of
// the whole log into an instant answer or a handful of index lookups.
func (l *Log) planSearch(ctx context.Context, f HistoryFilter) (searchPlan, []string, error) {
	// The device and list drill-downs ride their own indexes; the search is
	// a filter on rows they already bound. And until the dictionary covers
	// the whole log it cannot prove absence.
	if !l.derivedReady.Load() || !l.indexed.Load() || len(f.Clients) > 0 || f.List != "" {
		return planScan, nil, nil
	}
	m, err := l.matchNames(ctx, f.Search)
	if err != nil {
		return planScan, nil, err
	}
	if clientLike(f.Search) {
		clients, err := l.knownClients(ctx)
		if err != nil {
			return planScan, nil, err
		}
		for _, c := range clients {
			if strings.Contains(c, f.Search) {
				return planScan, nil, nil // a device's own rows are plentiful: scan
			}
		}
	}
	if m.capped {
		return planScan, nil, nil
	}
	relevant := m.hits
	switch f.Verdict {
	case VerdictBlocked:
		relevant = m.blocked
	case VerdictAllowed:
		relevant = m.hits - m.blocked
	}
	switch {
	case len(m.names) == 0 || relevant == 0:
		return planEmpty, nil, nil
	case relevant <= namesPlanMaxRows:
		return planNames, m.names, nil
	}
	return planScan, nil, nil
}

// waitBackfill blocks until the derived-table backfill has finished (tests
// and the field benchmark).
func (l *Log) waitBackfill() { <-l.derivedDone }

// QueryHistory returns judged queries newest-first, older than `before`, that
// match the filter — the Docket's window into the persisted log (so a
// drill-down shows full history, not just what the ring buffer holds since the
// last restart). SQLite-backed; returns nil in ephemeral mode, where the ring
// already feeds both the Docket and the dashboard so the frontend's live
// filtering stays consistent. Off the hot path.
func (l *Log) QueryHistory(ctx context.Context, f HistoryFilter, limit int, before time.Time) ([]Entry, error) {
	if l.reader() == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if before.IsZero() {
		before = time.Now().Add(time.Hour)
	}

	where := []string{"ts < ?"}
	args := []any{before.UnixMilli()}
	plan := planScan
	if f.Search != "" {
		var names []string
		var err error
		plan, names, err = l.planSearch(ctx, f)
		if err != nil {
			return nil, historyErr(ctx, err)
		}
		switch plan {
		case planEmpty:
			return []Entry{}, nil
		case planNames:
			ph := make([]string, len(names))
			for i, n := range names {
				ph[i] = "?"
				args = append(args, n)
			}
			where = append(where, "qname IN ("+strings.Join(ph, ",")+")")
		default:
			where = append(where, "(qname LIKE ? ESCAPE '\\' OR client LIKE ? ESCAPE '\\')")
			like := "%" + escapeLike(f.Search) + "%"
			args = append(args, like, like)
		}
	}
	if f.Verdict == VerdictBlocked || f.Verdict == VerdictAllowed {
		where = append(where, "verdict = ?")
		args = append(args, f.Verdict)
	}
	if len(f.Clients) > 0 {
		ph := make([]string, len(f.Clients))
		for i, c := range f.Clients {
			ph[i] = "?"
			args = append(args, c)
		}
		where = append(where, "client IN ("+strings.Join(ph, ",")+")")
	}
	if f.WouldBlock {
		// `audit_list > ''` is identical to `!= ''` on a NOT NULL text column
		// but is a range the (audit_list, ts) index can serve. Without it,
		// a would-block filter on a log with few (or zero) audit entries
		// walks the whole time index backwards looking for them — measured
		// 3.5 s on a live Pi with none at all. The index scan touches only
		// the audit-attributed rows; the sort is bounded by their count.
		where = append(where, "audit_list > ''")
	}
	if f.DNSSEC != "" {
		// No INDEXED BY here: the caller has always bounded this by time,
		// so the (ts) index does the work and this is a filter applied to
		// the rows it returns. See HistoryFilter.DNSSEC.
		where = append(where, "dnssec = ?")
		args = append(args, f.DNSSEC)
	}
	const cols = `ts, client, qname, qtype, verdict, list, rule, upstream, duration_ms, audit_list, audit_rule, dnssec`
	var query string
	switch {
	case f.List == "" && f.WouldBlock:
		args = append(args, limit)
		// Conditional for the same reason as TopAuditedDomains: the index
		// build is in the background now, and naming an index before it
		// exists fails the query outright.
		from := "querylog"
		if l.indexed.Load() {
			from = "querylog INDEXED BY idx_querylog_audit_ts"
		}
		query = `SELECT ` + cols + ` FROM ` + from + ` WHERE ` +
			strings.Join(where, " AND ") + ` ORDER BY ts DESC LIMIT ?`
	case f.List == "":
		args = append(args, limit)
		from := "querylog"
		if plan == planNames {
			// Name it: left alone, the planner may prefer walking (ts)
			// newest-first, which is exactly the scan this plan avoids.
			from = "querylog INDEXED BY idx_querylog_qname_verdict_ts"
		}
		query = `SELECT ` + cols + ` FROM ` + from + ` WHERE ` + strings.Join(where, " AND ") +
			` ORDER BY ts DESC LIMIT ?`
	default:
		// A naive `(list = ? OR audit_list = ?)` clause can't ride either
		// composite index, degenerating to a backward time scan — seconds
		// per page for a rarely matching list on an SD-card-sized log. Two
		// UNION ALL halves each walk their own (column, ts) index in order
		// and stop at the limit; the outer sort merges at most 2×limit rows.
		// Names never appear in both columns for one row (enforcing and
		// audit sources share one name namespace), so ALL is safe.
		base := strings.Join(where, " AND ")
		sub := func(col string) string {
			return `SELECT * FROM (SELECT ` + cols + ` FROM querylog WHERE ` + base +
				` AND ` + col + ` = ? ORDER BY ts DESC LIMIT ?)`
		}
		query = sub("list") + ` UNION ALL ` + sub("audit_list") + ` ORDER BY ts DESC LIMIT ?`
		half := append(append([]any{}, args...), f.List, limit)
		args = append(append(append([]any{}, half...), half...), limit)
	}
	// A free-text Search is a LIKE substring match no index can serve; with
	// few or no matches it scans the whole table (many seconds on Pi/SD).
	// Bound it so the UI gets a prompt, explicit error instead of an
	// apparently-hung "Searching…" — the caller surfaces the message.
	if f.Search != "" && plan == planScan {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, searchDeadline)
		defer cancel()
	}

	rows, err := l.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, historyErr(ctx, err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var ts int64
		if err := rows.Scan(&ts, &e.Client, &e.QName, &e.QType, &e.Verdict,
			&e.List, &e.Rule, &e.Upstream, &e.DurationMs,
			&e.AuditList, &e.AuditRule, &e.DNSSEC); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		e.Time = time.UnixMilli(ts)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, historyErr(ctx, err)
	}
	return out, nil
}

// historyErr translates a deadline-driven interrupt into the user-facing
// search-timeout error; anything else passes through wrapped.
func historyErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrSearchTimeout
	}
	return fmt.Errorf("query history: %w", err)
}

// escapeLike escapes the SQL LIKE metacharacters so a search for "a_b" or
// "50%" is matched literally (paired with ESCAPE '\' in the query).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
