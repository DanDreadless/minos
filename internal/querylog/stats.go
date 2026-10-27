package querylog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Aggregates for the dashboard. When SQLite is enabled these are answered
// from the rollups once the backfill is complete (rollups.go — exact, and
// independent of window length), and from the raw log before that. In ephemeral mode they scan the ring buffer, so the window is
// bounded by ring size. Entries buffered but not yet flushed (≤30s worth)
// are not counted — an acceptable skew for charts.

// TimelineBucket is one time slice of query volume.
type TimelineBucket struct {
	Time    time.Time `json:"time"`
	Total   int       `json:"total"`
	Blocked int       `json:"blocked"`
}

// TopDomain is a domain ranked by hit count.
type TopDomain struct {
	QName string `json:"qname"`
	Count int    `json:"count"`
}

// ClientStat is per-client query volume.
type ClientStat struct {
	Client  string `json:"client"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
}

// ListStat counts the blocks attributed to one list.
type ListStat struct {
	List  string `json:"list"`
	Count int    `json:"count"`
}

// Timeline returns query counts bucketed by the given width, oldest first.
// Empty buckets are filled in so charts get a continuous series.
func (l *Log) Timeline(ctx context.Context, since time.Time, bucket time.Duration) ([]TimelineBucket, error) {
	if bucket < time.Minute {
		bucket = time.Minute
	}
	counts := make(map[int64]*TimelineBucket)
	bucketMs := bucket.Milliseconds()
	add := func(tsMs int64, total, blocked int) {
		key := tsMs / bucketMs * bucketMs
		b, ok := counts[key]
		if !ok {
			b = &TimelineBucket{Time: time.UnixMilli(key)}
			counts[key] = b
		}
		b.Total += total
		b.Blocked += blocked
	}

	rolled := false
	if l.useRollups() {
		var err error
		if rolled, err = l.timelineRolled(ctx, since, bucketMs, add); err != nil {
			return nil, err
		}
	}
	if rolled {
		// Answered from the rollups; fill below
	} else if l.reader() != nil {
		rows, err := l.reader().QueryContext(ctx, `SELECT ts/? * ? AS bucket,
			COUNT(*), SUM(verdict = ?) FROM querylog
			WHERE ts >= ? GROUP BY bucket`,
			bucketMs, bucketMs, VerdictBlocked, since.UnixMilli())
		if err != nil {
			return nil, fmt.Errorf("timeline query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var ts int64
			var total, blocked int
			if err := rows.Scan(&ts, &total, &blocked); err != nil {
				return nil, fmt.Errorf("timeline scan: %w", err)
			}
			add(ts, total, blocked)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		l.scanRing(since, func(e Entry) {
			blocked := 0
			if e.Verdict == VerdictBlocked {
				blocked = 1
			}
			add(e.Time.UnixMilli(), 1, blocked)
		})
	}

	// Fill the full window with empty buckets so the chart has no gaps.
	out := make([]TimelineBucket, 0, len(counts))
	start := since.UnixMilli() / bucketMs * bucketMs
	end := time.Now().UnixMilli()
	for ts := start; ts <= end; ts += bucketMs {
		if b, ok := counts[ts]; ok {
			out = append(out, *b)
		} else {
			out = append(out, TimelineBucket{Time: time.UnixMilli(ts)})
		}
	}
	return out, nil
}

// TopBlockedDomains returns the most-blocked domains since the given time.
func (l *Log) TopBlockedDomains(ctx context.Context, since time.Time, n int) ([]TopDomain, error) {
	if n <= 0 || n > 100 {
		n = 10
	}
	if l.useRollups() {
		return l.topBlockedRolled(ctx, since, n)
	}
	if l.reader() != nil {
		// Pinned to the time index: with idx_querylog_qname_verdict_ts present
		// the planner prefers walking it to avoid the GROUP BY sort — the
		// whole log instead of the window (24 h went 0.28 s → 1.17 s on the
		// field Pi). Conditional, as naming an unbuilt index is an error.
		from := "querylog"
		if l.indexed.Load() {
			from = "querylog INDEXED BY idx_querylog_ts"
		}
		rows, err := l.reader().QueryContext(ctx, `SELECT qname, COUNT(*) AS c
			FROM `+from+` WHERE ts >= ? AND verdict = ?
			GROUP BY qname ORDER BY c DESC, qname LIMIT ?`,
			since.UnixMilli(), VerdictBlocked, n)
		if err != nil {
			return nil, fmt.Errorf("top domains query: %w", err)
		}
		defer rows.Close()
		out := make([]TopDomain, 0, n)
		for rows.Next() {
			var d TopDomain
			if err := rows.Scan(&d.QName, &d.Count); err != nil {
				return nil, fmt.Errorf("top domains scan: %w", err)
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}
	counts := make(map[string]int)
	l.scanRing(since, func(e Entry) {
		if e.Verdict == VerdictBlocked {
			counts[e.QName]++
		}
	})
	out := make([]TopDomain, 0, len(counts))
	for q, c := range counts {
		out = append(out, TopDomain{QName: q, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].QName < out[j].QName
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// MaxBlockedNames caps BlockedNames: a month of a home network blocks tens
// of thousands of distinct names, and the long tail beyond this many is
// noise for every use of the list.
const MaxBlockedNames = 100_000

// BlockedNames returns every name blocked since the given time with its
// count, busiest first, up to MaxBlockedNames — for the blocklist value
// report, which asks which lists would block each one.
func (l *Log) BlockedNames(ctx context.Context, since time.Time) ([]TopDomain, error) {
	if l.useRollups() {
		counts, err := l.blockedCountsRolled(ctx, since)
		if err != nil {
			return nil, err
		}
		return rankDomains(counts, MaxBlockedNames), nil
	}
	if l.reader() != nil {
		from := "querylog"
		if l.indexed.Load() {
			from = "querylog INDEXED BY idx_querylog_ts"
		}
		rows, err := l.reader().QueryContext(ctx, `SELECT qname, COUNT(*) AS c
			FROM `+from+` WHERE ts >= ? AND verdict = ?
			GROUP BY qname ORDER BY c DESC, qname LIMIT ?`,
			since.UnixMilli(), VerdictBlocked, MaxBlockedNames)
		if err != nil {
			return nil, fmt.Errorf("blocked names query: %w", err)
		}
		defer rows.Close()
		var out []TopDomain
		for rows.Next() {
			var d TopDomain
			if err := rows.Scan(&d.QName, &d.Count); err != nil {
				return nil, fmt.Errorf("blocked names scan: %w", err)
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}
	counts := make(map[string]int)
	l.scanRing(since, func(e Entry) {
		if e.Verdict == VerdictBlocked {
			counts[e.QName]++
		}
	})
	return rankDomains(counts, MaxBlockedNames), nil
}

// AllowedBefore counts a device's ordinary allowed lookups of exactly qname
// in [since, before) — answered with no rule involved, so neither let
// through by an allow rule nor while blocking was paused (both carry a
// list) — up to limit, with the time of the latest. It is the "this used to
// work" evidence for the breakage detector.
func (l *Log) AllowedBefore(ctx context.Context, client, qname string, since, before time.Time, limit int) (int, time.Time, error) {
	return l.countAnswers(ctx, client, qname, VerdictAllowed, true, since, before, limit)
}

// BlockedBefore counts a device's blocked lookups of exactly qname in
// [since, before), up to limit — the breakage detector's "blocked and
// retried all day already" test.
func (l *Log) BlockedBefore(ctx context.Context, client, qname string, since, before time.Time, limit int) (int, error) {
	n, _, err := l.countAnswers(ctx, client, qname, VerdictBlocked, false, since, before, limit)
	return n, err
}

// countAnswers counts one device's lookups of exactly qname with verdict in
// [since, before), up to limit, with the latest time; plain keeps only
// those no rule decided (no list).
func (l *Log) countAnswers(ctx context.Context, client, qname, verdict string, plain bool, since, before time.Time, limit int) (int, time.Time, error) {
	if l.reader() != nil {
		var n int
		var last sql.NullInt64
		// Pinned to (qname, verdict, ts): exactly this name's lookups in the
		// window. Left alone the planner may take the client index and walk
		// the device's whole week — measured at 5% of Minos's CPU under load.
		// Conditional, as naming an unbuilt index is an error.
		from := "querylog"
		if l.indexed.Load() {
			from = "querylog INDEXED BY idx_querylog_qname_verdict_ts"
		}
		cond := ""
		if plain {
			cond = " AND (list = '' OR list IS NULL)"
		}
		err := l.reader().QueryRowContext(ctx, `SELECT COUNT(*), MAX(ts) FROM (SELECT ts FROM `+from+`
			WHERE qname = ? AND verdict = ? AND ts >= ? AND ts < ? AND client = ?`+cond+` LIMIT ?)`,
			qname, verdict, since.UnixMilli(), before.UnixMilli(), client, limit).Scan(&n, &last)
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("count answers: %w", err)
		}
		if !last.Valid {
			return n, time.Time{}, nil
		}
		return n, time.UnixMilli(last.Int64), nil
	}
	n, last := 0, time.Time{}
	l.scanRing(since, func(e Entry) {
		if n < limit && e.QName == qname && e.Client == client && e.Verdict == verdict &&
			(!plain || e.List == "") && e.Time.Before(before) {
			n++
			if e.Time.After(last) {
				last = e.Time
			}
		}
	})
	return n, last, nil
}

// TopAuditedDomains returns the domains a given audit source marked most
// often — "what would this source block if you enforced it". The mirror of
// TopBlockedDomains on the audit columns, so an audit list and DNSSEC's
// permissive mode both answer the question the same way.
//
// list is matched exactly (e.g. "dnssec"); an empty list means any audit
// source. Rides the (audit_list, ts) index — the any-source form compares
// the column as a range rather than for inequality, which is equivalent on
// a NOT NULL text column but is something the index can actually serve.
func (l *Log) TopAuditedDomains(ctx context.Context, since time.Time, list string, n int) ([]TopDomain, error) {
	if n <= 0 || n > 100 {
		n = 10
	}
	if l.reader() != nil {
		where, args := "audit_list > ''", []any{since.UnixMilli()}
		if list != "" {
			where = "audit_list = ?"
			args = append(args, list)
		}
		args = append(args, n)
		// INDEXED BY is not decoration: with the aggregate on top, SQLite
		// picks idx_querylog_ts for the any-source form and walks the
		// whole time window (the measured seconds-per-query pathology on
		// SD). The exact-list form chooses the audit index on its own;
		// pinning both keeps the two shapes from diverging silently.
		//
		// It is conditional because index migration now runs in the
		// background: naming an index that does not exist yet is a hard
		// error, and answering by a worse plan for the seconds that build
		// takes beats failing the request outright.
		from := "querylog"
		if l.indexed.Load() {
			from = "querylog INDEXED BY idx_querylog_audit_ts"
		}
		rows, err := l.reader().QueryContext(ctx, `SELECT qname, COUNT(*) AS c
			FROM `+from+`
			WHERE ts >= ? AND `+where+`
			GROUP BY qname ORDER BY c DESC, qname LIMIT ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("top audited domains query: %w", err)
		}
		defer rows.Close()
		out := make([]TopDomain, 0, n)
		for rows.Next() {
			var d TopDomain
			if err := rows.Scan(&d.QName, &d.Count); err != nil {
				return nil, fmt.Errorf("top audited domains scan: %w", err)
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}
	counts := make(map[string]int)
	l.scanRing(since, func(e Entry) {
		if e.AuditList == "" || (list != "" && e.AuditList != list) {
			return
		}
		counts[e.QName]++
	})
	out := make([]TopDomain, 0, len(counts))
	for q, c := range counts {
		out = append(out, TopDomain{QName: q, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].QName < out[j].QName
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// AuditedTotal counts every query an audit source marked in the window —
// the headline TopAuditedDomains can't give, since that is capped at n.
// Same list semantics: exact match, or empty for any audit source.
func (l *Log) AuditedTotal(ctx context.Context, since time.Time, list string) (int, error) {
	if l.reader() != nil {
		where, args := "audit_list > ''", []any{since.UnixMilli()}
		if list != "" {
			where = "audit_list = ?"
			args = append(args, list)
		}
		var n int
		err := l.reader().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM querylog WHERE ts >= ? AND `+where, args...).Scan(&n)
		if err != nil {
			return 0, fmt.Errorf("audited total query: %w", err)
		}
		return n, nil
	}
	var n int
	l.scanRing(since, func(e Entry) {
		if e.AuditList == "" || (list != "" && e.AuditList != list) {
			return
		}
		n++
	})
	return n, nil
}

// BlocksByList groups blocked queries by the list that condemned them,
// busiest first — "is this list earning its keep" on the lists page. Every
// list is small in number (subscriptions plus a few built-in pseudo-lists
// like "denylist" and "service:<name>"), so there is no paging, just a
// defensive cap.
func (l *Log) BlocksByList(ctx context.Context, since time.Time) ([]ListStat, error) {
	const maxLists = 200
	if l.useRollups() {
		return l.blocksByListRolled(ctx, since, maxLists)
	}
	if l.reader() != nil {
		rows, err := l.reader().QueryContext(ctx, `SELECT list, COUNT(*) AS c
			FROM querylog WHERE ts >= ? AND verdict = ? AND list != ''
			GROUP BY list ORDER BY c DESC, list LIMIT ?`,
			since.UnixMilli(), VerdictBlocked, maxLists)
		if err != nil {
			return nil, fmt.Errorf("blocks by list query: %w", err)
		}
		defer rows.Close()
		var out []ListStat
		for rows.Next() {
			var s ListStat
			if err := rows.Scan(&s.List, &s.Count); err != nil {
				return nil, fmt.Errorf("blocks by list scan: %w", err)
			}
			out = append(out, s)
		}
		return out, rows.Err()
	}
	counts := make(map[string]int)
	l.scanRing(since, func(e Entry) {
		if e.Verdict == VerdictBlocked && e.List != "" {
			counts[e.List]++
		}
	})
	out := make([]ListStat, 0, len(counts))
	for list, c := range counts {
		out = append(out, ListStat{List: list, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].List < out[j].List
	})
	if len(out) > maxLists {
		out = out[:maxLists]
	}
	return out, nil
}

// ListNames returns every distinct list name attributed in the window —
// enforcing and audit — sorted, for the Docket's list-filter dropdown.
// Bounded by the ts index (SQLite) or the ring (ephemeral mode). Like
// BlocksByList, lists are few by nature; the cap is defensive only.
func (l *Log) ListNames(ctx context.Context, since time.Time) ([]string, error) {
	const maxLists = 200
	if l.reader() != nil {
		// The previous shape range-scanned every row in the ts window
		// (seconds on an SD-card log), and even a plain DISTINCT visits
		// every index entry. Instead, skip-walk each (column, ts) index
		// with MIN(col) > last — one index seek per distinct name — then
		// window per name with one indexed recency probe each. Everything
		// is O(number of lists), which is dozens, never O(rows).
		set := make(map[string]bool)
		for _, col := range []string{"list", "audit_list"} {
			name := "" // MIN(col) > '' also skips the unattributed rows
			for range maxLists {
				var next sql.NullString
				err := l.reader().QueryRowContext(ctx,
					`SELECT MIN(`+col+`) FROM querylog WHERE `+col+` > ?`, name).Scan(&next)
				if err != nil {
					return nil, fmt.Errorf("list names walk: %w", err)
				}
				if !next.Valid {
					break
				}
				name = next.String
				set[name] = true
			}
		}
		sinceMs := since.UnixMilli()
		var out []string
		for name := range set {
			seen, err := l.attributedSince(ctx, name, sinceMs)
			if err != nil {
				return nil, err
			}
			if seen {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		if len(out) > maxLists {
			out = out[:maxLists]
		}
		return out, nil
	}
	set := make(map[string]bool)
	l.scanRing(since, func(e Entry) {
		if e.List != "" {
			set[e.List] = true
		}
		if e.AuditList != "" {
			set[e.AuditList] = true
		}
	})
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > maxLists {
		out = out[:maxLists]
	}
	return out, nil
}

// attributedSince reports whether any entry since the cutoff carries name as
// its enforcing or audit list — two index seeks, used to window ListNames.
func (l *Log) attributedSince(ctx context.Context, name string, sinceMs int64) (bool, error) {
	for _, col := range []string{"list", "audit_list"} {
		var one int
		err := l.reader().QueryRowContext(ctx,
			`SELECT 1 FROM querylog WHERE `+col+` = ? AND ts >= ? LIMIT 1`,
			name, sinceMs).Scan(&one)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, sql.ErrNoRows):
		default:
			return false, fmt.Errorf("list recency probe: %w", err)
		}
	}
	return false, nil
}

// TopClients returns the busiest clients since the given time.
func (l *Log) TopClients(ctx context.Context, since time.Time, n int) ([]ClientStat, error) {
	if n <= 0 || n > 100 {
		n = 10
	}
	if l.useRollups() {
		return l.topClientsRolled(ctx, since, n)
	}
	if l.reader() != nil {
		rows, err := l.reader().QueryContext(ctx, `SELECT client, COUNT(*) AS c,
			SUM(verdict = ?) FROM querylog WHERE ts >= ?
			GROUP BY client ORDER BY c DESC, client LIMIT ?`,
			VerdictBlocked, since.UnixMilli(), n)
		if err != nil {
			return nil, fmt.Errorf("top clients query: %w", err)
		}
		defer rows.Close()
		out := make([]ClientStat, 0, n)
		for rows.Next() {
			var c ClientStat
			if err := rows.Scan(&c.Client, &c.Total, &c.Blocked); err != nil {
				return nil, fmt.Errorf("top clients scan: %w", err)
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	counts := make(map[string]*ClientStat)
	l.scanRing(since, func(e Entry) {
		c, ok := counts[e.Client]
		if !ok {
			c = &ClientStat{Client: e.Client}
			counts[e.Client] = c
		}
		c.Total++
		if e.Verdict == VerdictBlocked {
			c.Blocked++
		}
	})
	out := make([]ClientStat, 0, len(counts))
	for _, c := range counts {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Client < out[j].Client
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// ClientOverview aggregates one device's traffic for the client drill-down:
// totals plus its most-queried allowed and blocked names. A physical device
// can span several IPs (MAC-merged leases), so it takes the full set.
type ClientOverview struct {
	// WindowStart is where the counted window actually begins: the
	// requested start, or — for windows of 48 h and more answered from the
	// daily rollups — the start of that UTC day.
	WindowStart time.Time   `json:"window_start"`
	Total       int         `json:"total"`
	Blocked     int         `json:"blocked"`
	TopAllowed  []TopDomain `json:"top_allowed"`
	TopBlocked  []TopDomain `json:"top_blocked"`
}

// ClientOverview reports what the given client addresses resolved since the
// given time. Off the hot path, like every aggregate here.
func (l *Log) ClientOverview(ctx context.Context, clients []string, since time.Time, n int) (ClientOverview, error) {
	if n <= 0 || n > 100 {
		n = 10
	}
	out := ClientOverview{WindowStart: since, TopAllowed: []TopDomain{}, TopBlocked: []TopDomain{}}
	if len(clients) == 0 {
		return out, nil
	}
	if l.useRollups() && time.Since(since) >= clientDaysFrom {
		return l.clientOverviewRolled(ctx, clients, since, n)
	}

	if l.reader() != nil {
		in := strings.Repeat("?,", len(clients))
		in = in[:len(in)-1]
		args := make([]any, 0, len(clients)+2)
		for _, c := range clients {
			args = append(args, c)
		}

		err := l.reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(verdict = ?), 0)
			FROM querylog WHERE client IN (`+in+`) AND ts >= ?`,
			append([]any{VerdictBlocked}, append(args, since.UnixMilli())...)...,
		).Scan(&out.Total, &out.Blocked)
		if err != nil {
			return out, fmt.Errorf("client totals query: %w", err)
		}

		top := func(verdict string) ([]TopDomain, error) {
			rows, err := l.reader().QueryContext(ctx, `SELECT qname, COUNT(*) AS c
				FROM querylog WHERE client IN (`+in+`) AND ts >= ? AND verdict = ?
				GROUP BY qname ORDER BY c DESC, qname LIMIT ?`,
				append(append([]any{}, args...), since.UnixMilli(), verdict, n)...)
			if err != nil {
				return nil, fmt.Errorf("client top domains query: %w", err)
			}
			defer rows.Close()
			doms := make([]TopDomain, 0, n)
			for rows.Next() {
				var d TopDomain
				if err := rows.Scan(&d.QName, &d.Count); err != nil {
					return nil, fmt.Errorf("client top domains scan: %w", err)
				}
				doms = append(doms, d)
			}
			return doms, rows.Err()
		}
		if out.TopAllowed, err = top(VerdictAllowed); err != nil {
			return out, err
		}
		if out.TopBlocked, err = top(VerdictBlocked); err != nil {
			return out, err
		}
		return out, nil
	}

	// Ephemeral mode: one pass over the ring.
	want := make(map[string]bool, len(clients))
	for _, c := range clients {
		want[c] = true
	}
	allowed := make(map[string]int)
	blocked := make(map[string]int)
	l.scanRing(since, func(e Entry) {
		if !want[e.Client] {
			return
		}
		out.Total++
		if e.Verdict == VerdictBlocked {
			out.Blocked++
			blocked[e.QName]++
		} else {
			allowed[e.QName]++
		}
	})
	rank := func(counts map[string]int) []TopDomain {
		doms := make([]TopDomain, 0, len(counts))
		for q, c := range counts {
			doms = append(doms, TopDomain{QName: q, Count: c})
		}
		sort.Slice(doms, func(i, j int) bool {
			if doms[i].Count != doms[j].Count {
				return doms[i].Count > doms[j].Count
			}
			return doms[i].QName < doms[j].QName
		})
		if len(doms) > n {
			doms = doms[:n]
		}
		return doms
	}
	out.TopAllowed = rank(allowed)
	out.TopBlocked = rank(blocked)
	return out, nil
}

// ClientSummary is per-client lifetime state from the persisted log, used to
// rehydrate the device registry after a restart.
type ClientSummary struct {
	Client  string
	Total   uint64
	Blocked uint64
	First   time.Time
	Last    time.Time
}

// ClientsSummary aggregates the persisted log per client, counting only
// rows older than `before`. Returns nil in ephemeral mode.
//
// The bound is not a nicety: hydration no longer runs before the listeners
// (it stalled every restart for as long as this scan took), so live traffic
// is already being counted by the registry when this lands. Passing the
// process start time makes the two halves disjoint — history here, live
// there — so the caller can add them without double-counting.
//
// It is still a full scan of the retained log, but a covering one:
// idx_querylog_client_ts_verdict carries client, ts and verdict, which is
// every column this touches. That is the difference between 39s and 1.0s on
// a 7.4M-row log. Off the hot path, and on the reader pool, so even the
// slow case blocks nothing.
func (l *Log) ClientsSummary(ctx context.Context, before time.Time) ([]ClientSummary, error) {
	if l.reader() == nil {
		return nil, nil
	}
	rows, err := l.reader().QueryContext(ctx, `SELECT client, COUNT(*),
		SUM(verdict = ?), MIN(ts), MAX(ts) FROM querylog
		WHERE ts < ? GROUP BY client`,
		VerdictBlocked, before.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("clients summary: %w", err)
	}
	defer rows.Close()
	var out []ClientSummary
	for rows.Next() {
		var c ClientSummary
		var first, last int64
		if err := rows.Scan(&c.Client, &c.Total, &c.Blocked, &first, &last); err != nil {
			return nil, fmt.Errorf("clients summary scan: %w", err)
		}
		c.First, c.Last = time.UnixMilli(first), time.UnixMilli(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// The methods below are the digest's data source (internal/notify declares
// the matching interface with builtin-typed signatures so it never has to
// import this package; *Log satisfies it structurally at the wiring site).

// Totals counts queries and blocks since the given time.
func (l *Log) Totals(ctx context.Context, since time.Time) (total, blocked int, err error) {
	if l.useRollups() {
		return l.totalsRolled(ctx, since)
	}
	if l.reader() != nil {
		err = l.reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(verdict = ?), 0)
			FROM querylog WHERE ts >= ?`, VerdictBlocked, since.UnixMilli()).
			Scan(&total, &blocked)
		if err != nil {
			return 0, 0, fmt.Errorf("totals query: %w", err)
		}
		return total, blocked, nil
	}
	l.scanRing(since, func(e Entry) {
		total++
		if e.Verdict == VerdictBlocked {
			blocked++
		}
	})
	return total, blocked, nil
}

// TopBlockedSummary is TopBlockedDomains flattened to parallel slices of
// builtin types, for the digest interface.
func (l *Log) TopBlockedSummary(ctx context.Context, since time.Time, n int) (domains []string, counts []int, err error) {
	top, err := l.TopBlockedDomains(ctx, since, n)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range top {
		domains = append(domains, d.QName)
		counts = append(counts, d.Count)
	}
	return domains, counts, nil
}

// BusiestClient names the client with the most queries since the given time.
// Empty when there was no traffic.
func (l *Log) BusiestClient(ctx context.Context, since time.Time) (client string, queries int, err error) {
	top, err := l.TopClients(ctx, since, 1)
	if err != nil {
		return "", 0, err
	}
	if len(top) == 0 {
		return "", 0, nil
	}
	return top[0].Client, top[0].Total, nil
}

// NewClientsSince counts clients whose first-ever logged query falls at or
// after since. In ephemeral mode "first-ever" is bounded by the ring.
func (l *Log) NewClientsSince(ctx context.Context, since time.Time) (int, error) {
	if l.reader() != nil {
		var n int
		err := l.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM
			(SELECT client, MIN(ts) AS first FROM querylog GROUP BY client)
			WHERE first >= ?`, since.UnixMilli()).Scan(&n)
		if err != nil {
			return 0, fmt.Errorf("new clients query: %w", err)
		}
		return n, nil
	}
	// Ring mode: walk everything (not just >= since) so a client with older
	// in-ring traffic doesn't count as new.
	first := make(map[string]time.Time)
	for _, e := range l.Recent(0) {
		if t, ok := first[e.Client]; !ok || e.Time.Before(t) {
			first[e.Client] = e.Time
		}
	}
	n := 0
	for _, t := range first {
		if !t.Before(since) {
			n++
		}
	}
	return n, nil
}

// RecentQNames returns up to n distinct query names the client asked for,
// newest first, from the in-memory ring only — the bounded read behind the
// device-registry traffic hints. Enrichment-ticker cadence, never the hot
// path, and deliberately not a SQLite query: the ring's recent window is
// exactly the "what is this device doing right now" signal a hint wants.
func (l *Log) RecentQNames(client string, n int) []string {
	if n <= 0 || n > 1000 {
		n = 200
	}
	seen := make(map[string]bool, n)
	out := make([]string, 0, n)
	l.ringMu.RLock()
	defer l.ringMu.RUnlock()
	for i := 0; i < l.count && len(out) < n; i++ {
		idx := (l.head - 1 - i + len(l.ring)*2) % len(l.ring)
		e := l.ring[idx]
		if e.Client != client || seen[e.QName] {
			continue
		}
		seen[e.QName] = true
		out = append(out, e.QName)
	}
	return out
}

// scanRing visits ring entries at or after since, under the read lock.
func (l *Log) scanRing(since time.Time, visit func(Entry)) {
	l.ringMu.RLock()
	defer l.ringMu.RUnlock()
	for i := 0; i < l.count; i++ {
		idx := (l.head - 1 - i + len(l.ring)*2) % len(l.ring)
		e := l.ring[idx]
		if e.Time.Before(since) {
			break // ring is time-ordered; everything older follows
		}
		visit(e)
	}
}
