package querylog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Rollup reads. Each dashboard aggregate is answered from the rollups for the
// whole buckets in its window plus a raw read of the partial leading bucket
// (at most one hour, through the time index), so the result is exactly what
// the raw query over the log returns — measured on the field Pi, the 90-day
// raw timeline took 91 s; this reads a few thousand rollup rows.
//
// The device page's long windows (48 h and over) are the one deliberate
// approximation: they snap to whole UTC days and say so via WindowStart.

// useRollups reports whether rollup reads are allowed: SQLite-backed, and the
// backfill has covered the whole log.
func (l *Log) useRollups() bool { return l.reader() != nil && l.derivedReady.Load() }

func alignUp(ms, grain int64) int64 { return (ms + grain - 1) / grain * grain }

// headRows visits raw rows in [sinceMs, untilMs): the partial bucket at the
// front of a window, never more than one bucket's worth.
func (l *Log) headRows(ctx context.Context, sinceMs, untilMs int64, visit func(ts int64, client, qname, verdict, list string)) error {
	if sinceMs >= untilMs {
		return nil
	}
	rows, err := l.reader().QueryContext(ctx, `SELECT ts, client, qname, verdict, list FROM querylog
		WHERE ts >= ? AND ts < ?`, sinceMs, untilMs)
	if err != nil {
		return fmt.Errorf("window head: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var c, q, v, list string
		if err := rows.Scan(&ts, &c, &q, &v, &list); err != nil {
			return fmt.Errorf("window head scan: %w", err)
		}
		visit(ts, c, q, v, list)
	}
	return rows.Err()
}

// timelineRolled answers Timeline from roll_10m (sub-hour buckets, last 48 h)
// or roll_hour. ok=false means the request is outside what the rollups hold
// (an odd bucket width, or 10-minute buckets older than roll_10m keeps).
func (l *Log) timelineRolled(ctx context.Context, since time.Time, bucketMs int64, add func(ts int64, total, blocked int)) (ok bool, err error) {
	sinceMs := since.UnixMilli()
	grain, table := hourMs, "roll_hour"
	if bucketMs < hourMs {
		grain, table = tenMinMs, "roll_10m"
		if time.Now().UnixMilli()-sinceMs > roll10mKeepMs-hourMs {
			return false, nil
		}
	}
	if bucketMs%grain != 0 {
		return false, nil
	}
	full := alignUp(sinceMs, grain)
	if err := l.headRows(ctx, sinceMs, full, func(ts int64, _, _, verdict, _ string) {
		b := 0
		if verdict == VerdictBlocked {
			b = 1
		}
		add(ts, 1, b)
	}); err != nil {
		return false, err
	}
	rows, err := l.reader().QueryContext(ctx, `SELECT bucket, verdict, SUM(n) FROM `+table+`
		WHERE bucket >= ? GROUP BY bucket, verdict`, full)
	if err != nil {
		return false, fmt.Errorf("timeline rollup: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket int64
		var verdict string
		var n int
		if err := rows.Scan(&bucket, &verdict, &n); err != nil {
			return false, fmt.Errorf("timeline rollup scan: %w", err)
		}
		b := 0
		if verdict == VerdictBlocked {
			b = n
		}
		add(bucket, n, b)
	}
	return true, rows.Err()
}

func rankDomains(counts map[string]int, n int) []TopDomain {
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
	return out
}

func (l *Log) topBlockedRolled(ctx context.Context, since time.Time, n int) ([]TopDomain, error) {
	counts, err := l.blockedCountsRolled(ctx, since)
	if err != nil {
		return nil, err
	}
	return rankDomains(counts, n), nil
}

// blockedCountsRolled counts every blocked name since the given time.
func (l *Log) blockedCountsRolled(ctx context.Context, since time.Time) (map[string]int, error) {
	sinceMs := since.UnixMilli()
	full := alignUp(sinceMs, hourMs)
	counts := make(map[string]int)
	if err := l.headRows(ctx, sinceMs, full, func(_ int64, _, qname, verdict, _ string) {
		if verdict == VerdictBlocked {
			counts[qname]++
		}
	}); err != nil {
		return nil, err
	}
	rows, err := l.reader().QueryContext(ctx, `SELECT qname, SUM(n) FROM roll_hour_blocked
		WHERE bucket >= ? GROUP BY qname`, full)
	if err != nil {
		return nil, fmt.Errorf("top blocked rollup: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var q string
		var c int
		if err := rows.Scan(&q, &c); err != nil {
			return nil, fmt.Errorf("top blocked rollup scan: %w", err)
		}
		counts[q] += c
	}
	return counts, rows.Err()
}

// hourly visits roll_hour over [full, ∞) plus the raw head, as
// (client, verdict, list, count) increments.
func (l *Log) hourly(ctx context.Context, since time.Time, visit func(client, verdict, list string, n int)) error {
	sinceMs := since.UnixMilli()
	full := alignUp(sinceMs, hourMs)
	if err := l.headRows(ctx, sinceMs, full, func(_ int64, client, _, verdict, list string) {
		visit(client, verdict, list, 1)
	}); err != nil {
		return err
	}
	rows, err := l.reader().QueryContext(ctx, `SELECT client, verdict, list, SUM(n) FROM roll_hour
		WHERE bucket >= ? GROUP BY client, verdict, list`, full)
	if err != nil {
		return fmt.Errorf("hourly rollup: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c, v, list string
		var n int
		if err := rows.Scan(&c, &v, &list, &n); err != nil {
			return fmt.Errorf("hourly rollup scan: %w", err)
		}
		visit(c, v, list, n)
	}
	return rows.Err()
}

func (l *Log) topClientsRolled(ctx context.Context, since time.Time, n int) ([]ClientStat, error) {
	counts := make(map[string]*ClientStat)
	if err := l.hourly(ctx, since, func(client, verdict, _ string, k int) {
		c := counts[client]
		if c == nil {
			c = &ClientStat{Client: client}
			counts[client] = c
		}
		c.Total += k
		if verdict == VerdictBlocked {
			c.Blocked += k
		}
	}); err != nil {
		return nil, err
	}
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

func (l *Log) blocksByListRolled(ctx context.Context, since time.Time, limit int) ([]ListStat, error) {
	counts := make(map[string]int)
	if err := l.hourly(ctx, since, func(_, verdict, list string, k int) {
		if verdict == VerdictBlocked && list != "" {
			counts[list] += k
		}
	}); err != nil {
		return nil, err
	}
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
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (l *Log) totalsRolled(ctx context.Context, since time.Time) (total, blocked int, err error) {
	err = l.hourly(ctx, since, func(_, verdict, _ string, k int) {
		total += k
		if verdict == VerdictBlocked {
			blocked += k
		}
	})
	return total, blocked, err
}

// clientDaysFrom is where the device page's day-snapped rollup windows
// start: from this window length on, whole UTC days are read.
const clientDaysFrom = 48 * time.Hour

// clientOverviewRolled answers a device's long window from roll_day_client,
// snapped down to the start of since's UTC day (reported as WindowStart).
func (l *Log) clientOverviewRolled(ctx context.Context, clients []string, since time.Time, n int) (ClientOverview, error) {
	start := since.UnixMilli() / dayMs * dayMs
	out := ClientOverview{WindowStart: time.UnixMilli(start), TopAllowed: []TopDomain{}, TopBlocked: []TopDomain{}}
	in := strings.Repeat("?,", len(clients))
	args := make([]any, 0, len(clients)+1)
	for _, c := range clients {
		args = append(args, c)
	}
	args = append(args, start)
	rows, err := l.reader().QueryContext(ctx, `SELECT verdict, qname, SUM(n) FROM roll_day_client
		WHERE client IN (`+in[:len(in)-1]+`) AND bucket >= ? GROUP BY verdict, qname`, args...)
	if err != nil {
		return out, fmt.Errorf("client rollup: %w", err)
	}
	defer rows.Close()
	allowed, blocked := make(map[string]int), make(map[string]int)
	for rows.Next() {
		var v, q string
		var k int
		if err := rows.Scan(&v, &q, &k); err != nil {
			return out, fmt.Errorf("client rollup scan: %w", err)
		}
		out.Total += k
		if v == VerdictBlocked {
			out.Blocked += k
			blocked[q] += k
		} else {
			allowed[q] += k
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.TopAllowed, out.TopBlocked = rankDomains(allowed, n), rankDomains(blocked, n)
	return out, nil
}

// ClientName is one device's lookups of one name with one verdict.
type ClientName struct {
	Client  string
	QName   string
	Verdict string
	Count   int
}

// ClientNames returns every name the given devices looked up since the
// start of since's UTC day (the returned window start), with counts by
// verdict — the privacy report's input. From roll_day_client one device at
// a time, so each read is a primary-key range; before the rollups are
// ready, the raw log (client index) or the ring.
func (l *Log) ClientNames(ctx context.Context, clients []string, since time.Time) ([]ClientName, time.Time, error) {
	start := time.UnixMilli(since.UnixMilli() / dayMs * dayMs)
	var out []ClientName
	if len(clients) == 0 {
		return out, start, nil
	}
	if l.useRollups() {
		for _, c := range clients {
			rows, err := l.reader().QueryContext(ctx, `SELECT verdict, qname, SUM(n) FROM roll_day_client
				WHERE client = ? AND bucket >= ? GROUP BY verdict, qname`, c, start.UnixMilli())
			if err != nil {
				return nil, start, fmt.Errorf("client names: %w", err)
			}
			for rows.Next() {
				cn := ClientName{Client: c}
				if err := rows.Scan(&cn.Verdict, &cn.QName, &cn.Count); err != nil {
					rows.Close()
					return nil, start, fmt.Errorf("client names scan: %w", err)
				}
				out = append(out, cn)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, start, err
			}
		}
		return out, start, nil
	}
	if l.reader() != nil {
		in := strings.Repeat("?,", len(clients))
		args := make([]any, 0, len(clients)+1)
		for _, c := range clients {
			args = append(args, c)
		}
		args = append(args, start.UnixMilli())
		rows, err := l.reader().QueryContext(ctx, `SELECT client, qname, verdict, COUNT(*) FROM querylog
			WHERE client IN (`+in[:len(in)-1]+`) AND ts >= ? GROUP BY client, qname, verdict`, args...)
		if err != nil {
			return nil, start, fmt.Errorf("client names: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var cn ClientName
			if err := rows.Scan(&cn.Client, &cn.QName, &cn.Verdict, &cn.Count); err != nil {
				return nil, start, fmt.Errorf("client names scan: %w", err)
			}
			out = append(out, cn)
		}
		return out, start, rows.Err()
	}
	want := map[string]bool{}
	for _, c := range clients {
		want[c] = true
	}
	counts := map[ClientName]int{}
	l.scanRing(start, func(e Entry) {
		if want[e.Client] {
			counts[ClientName{Client: e.Client, QName: e.QName, Verdict: e.Verdict}]++
		}
	})
	for k, n := range counts {
		k.Count = n
		out = append(out, k)
	}
	return out, start, nil
}
