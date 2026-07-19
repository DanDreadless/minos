package querylog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Aggregates for the dashboard. When SQLite is enabled these run against the
// database (off the hot path; the single connection serializes with the
// writer). In ephemeral mode they scan the ring buffer, so the window is
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

	if l.db != nil {
		rows, err := l.db.QueryContext(ctx, `SELECT ts/? * ? AS bucket,
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
	if l.db != nil {
		rows, err := l.db.QueryContext(ctx, `SELECT qname, COUNT(*) AS c
			FROM querylog WHERE ts >= ? AND verdict = ?
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

// BlocksByList groups blocked queries by the list that condemned them,
// busiest first — "is this list earning its keep" on the lists page. Every
// list is small in number (subscriptions plus a few built-in pseudo-lists
// like "denylist" and "service:<name>"), so there is no paging, just a
// defensive cap.
func (l *Log) BlocksByList(ctx context.Context, since time.Time) ([]ListStat, error) {
	const maxLists = 200
	if l.db != nil {
		rows, err := l.db.QueryContext(ctx, `SELECT list, COUNT(*) AS c
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
	if l.db != nil {
		rows, err := l.db.QueryContext(ctx, `SELECT list FROM querylog
			WHERE ts >= ? AND list != ''
			UNION SELECT audit_list FROM querylog
			WHERE ts >= ? AND audit_list != ''
			ORDER BY 1 LIMIT ?`,
			since.UnixMilli(), since.UnixMilli(), maxLists)
		if err != nil {
			return nil, fmt.Errorf("list names query: %w", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, fmt.Errorf("list names scan: %w", err)
			}
			out = append(out, name)
		}
		return out, rows.Err()
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

// TopClients returns the busiest clients since the given time.
func (l *Log) TopClients(ctx context.Context, since time.Time, n int) ([]ClientStat, error) {
	if n <= 0 || n > 100 {
		n = 10
	}
	if l.db != nil {
		rows, err := l.db.QueryContext(ctx, `SELECT client, COUNT(*) AS c,
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
	Total      int         `json:"total"`
	Blocked    int         `json:"blocked"`
	TopAllowed []TopDomain `json:"top_allowed"`
	TopBlocked []TopDomain `json:"top_blocked"`
}

// ClientOverview reports what the given client addresses resolved since the
// given time. Off the hot path, like every aggregate here.
func (l *Log) ClientOverview(ctx context.Context, clients []string, since time.Time, n int) (ClientOverview, error) {
	if n <= 0 || n > 100 {
		n = 10
	}
	out := ClientOverview{TopAllowed: []TopDomain{}, TopBlocked: []TopDomain{}}
	if len(clients) == 0 {
		return out, nil
	}

	if l.db != nil {
		in := strings.Repeat("?,", len(clients))
		in = in[:len(in)-1]
		args := make([]any, 0, len(clients)+2)
		for _, c := range clients {
			args = append(args, c)
		}

		err := l.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(verdict = ?), 0)
			FROM querylog WHERE client IN (`+in+`) AND ts >= ?`,
			append([]any{VerdictBlocked}, append(args, since.UnixMilli())...)...,
		).Scan(&out.Total, &out.Blocked)
		if err != nil {
			return out, fmt.Errorf("client totals query: %w", err)
		}

		top := func(verdict string) ([]TopDomain, error) {
			rows, err := l.db.QueryContext(ctx, `SELECT qname, COUNT(*) AS c
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

// ClientsSummary aggregates the persisted log per client. Returns nil in
// ephemeral mode. Called once at startup, never on the hot path.
func (l *Log) ClientsSummary(ctx context.Context) ([]ClientSummary, error) {
	if l.db == nil {
		return nil, nil
	}
	rows, err := l.db.QueryContext(ctx, `SELECT client, COUNT(*),
		SUM(verdict = ?), MIN(ts), MAX(ts) FROM querylog GROUP BY client`,
		VerdictBlocked)
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
	if l.db != nil {
		err = l.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(verdict = ?), 0)
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
	if l.db != nil {
		var n int
		err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM
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
