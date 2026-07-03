package querylog

import (
	"context"
	"fmt"
	"sort"
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
