//go:build fieldbench

// Field benchmark: time the query-log reads that users wait on, against a
// real database rather than a synthetic one — on the machine Minos runs on.
// It never touches the live file except to read a consistent snapshot of it.
//
// Build for the Pi and run it there (the snapshot stays on the Pi; nobody's
// query history leaves the machine):
//
//	GOOS=linux GOARCH=arm64 go test -c -tags fieldbench -o qlbench ./internal/querylog
//	scp qlbench pi:/tmp/
//	ssh pi 'sudo MINOS_LIVE_DB=/var/lib/minos/minos.db MINOS_BENCH_DB=/tmp/bench.db \
//	        /tmp/qlbench -test.run TestFieldSnapshot -test.v'
//	ssh pi 'MINOS_BENCH_DB=/tmp/bench.db /tmp/qlbench -test.run TestFieldBench -test.v'
//	ssh pi 'rm -f /tmp/bench.db* /tmp/qlbench'
//
// The first TestFieldBench run against a fresh snapshot also times whatever
// migrations Open performs, which is what an upgrade costs in the field.
package querylog

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestFieldSnapshot copies MINOS_LIVE_DB to MINOS_BENCH_DB with VACUUM INTO:
// a consistent read of a live WAL database that never writes to it.
func TestFieldSnapshot(t *testing.T) {
	live, dst := os.Getenv("MINOS_LIVE_DB"), os.Getenv("MINOS_BENCH_DB")
	if live == "" || dst == "" {
		t.Skip("set MINOS_LIVE_DB and MINOS_BENCH_DB")
	}
	db, err := sql.Open("sqlite", "file:"+live+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Now()
	if _, err := db.Exec(`VACUUM INTO ?`, dst); err != nil {
		t.Fatal(err)
	}
	var rows int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM querylog`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot of %d rows in %s", rows, time.Since(start).Round(time.Millisecond))
}

func TestFieldBench(t *testing.T) {
	path := os.Getenv("MINOS_BENCH_DB")
	if path == "" {
		t.Skip("set MINOS_BENCH_DB to a snapshot (never the live file)")
	}
	start := time.Now()
	l, err := Open(Options{RingSize: 1000, DBPath: path, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Logf("open: %s", time.Since(start).Round(time.Millisecond))
	<-l.Ready()
	t.Logf("ready (migrations done): %s", time.Since(start).Round(time.Millisecond))
	if w, ok := any(l).(interface{ waitBackfill() }); ok {
		w.waitBackfill()
		t.Logf("backfill done: %s", time.Since(start).Round(time.Millisecond))
	}

	ctx := context.Background()
	timeIt := func(name string, f func() (int, error)) {
		t.Helper()
		start := time.Now()
		n, err := f()
		took := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Logf("%-34s %8s  ERROR %v", name, took, err)
			return
		}
		t.Logf("%-34s %8s  %d rows", name, took, n)
	}
	search := func(f HistoryFilter) func() (int, error) {
		return func() (int, error) {
			e, err := l.QueryHistory(ctx, f, 200, time.Time{})
			return len(e), err
		}
	}
	for _, term := range []string{"zzzqqqnomatch", "google", "apple.com", "netflix", "doubleclick", "ntp.org", "192.168.68.1"} {
		timeIt("search "+term, search(HistoryFilter{Search: term}))
	}
	timeIt("search netflix, blocked", search(HistoryFilter{Search: "netflix", Verdict: VerdictBlocked}))
	timeIt("search zzzqqqnomatch, blocked", search(HistoryFilter{Search: "zzzqqqnomatch", Verdict: VerdictBlocked}))
	timeIt("blocked only (no search)", search(HistoryFilter{Verdict: VerdictBlocked}))

	for _, hours := range []int{24, 168, 720, 2160} {
		since := time.Now().Add(-time.Duration(hours) * time.Hour)
		bucket := time.Hour
		if hours > 168 {
			bucket = 24 * time.Hour
		}
		timeIt("timeline "+time.Duration(hours*int(time.Hour)).String(), func() (int, error) {
			b, err := l.Timeline(ctx, since, bucket)
			return len(b), err
		})
		timeIt("top blocked "+time.Duration(hours*int(time.Hour)).String(), func() (int, error) {
			d, err := l.TopBlockedDomains(ctx, since, 10)
			return len(d), err
		})
		timeIt("top clients "+time.Duration(hours*int(time.Hour)).String(), func() (int, error) {
			c, err := l.TopClients(ctx, since, 10)
			return len(c), err
		})
		timeIt("blocks by list "+time.Duration(hours*int(time.Hour)).String(), func() (int, error) {
			s, err := l.BlocksByList(ctx, since)
			return len(s), err
		})
	}
}
