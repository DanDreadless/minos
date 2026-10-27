package querylog

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// randomRows spreads n rows over the last `span`, with a handful of clients,
// lists and names, roughly 1 in 5 blocked.
func randomRows(rng *rand.Rand, n int, span time.Duration) []Entry {
	now := time.Now()
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		e := Entry{
			Time:    now.Add(-time.Duration(rng.Int63n(int64(span)))),
			Client:  fmt.Sprintf("10.0.0.%d", rng.Intn(6)),
			QName:   fmt.Sprintf("host%d.example", rng.Intn(40)),
			QType:   "A",
			Verdict: VerdictAllowed,
		}
		if rng.Intn(5) == 0 {
			e.Verdict = VerdictBlocked
			e.List = []string{"stevenblack", "hagezi", "denylist"}[rng.Intn(3)]
		}
		out = append(out, e)
	}
	return out
}

// TestRollupsAnswerExactlyLikeTheLog is the contract: every aggregate gives
// the same answer from the rollups as from the raw log, for windows whose
// start falls mid-bucket — with half the rows arriving by backfill and half
// by live writes, so both write paths are covered.
func TestRollupsAnswerExactlyLikeTheLog(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	path := filepath.Join(t.TempDir(), "q.db")
	l := openReady(t, path)
	rawInsert(t, l, randomRows(rng, 4000, 6*24*time.Hour)) // pre-upgrade rows
	if _, err := l.db.Exec(`DELETE FROM querylog_meta`); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l = openReady(t, path) // backfills the rows above
	defer l.Close()
	for i := 0; i < 4; i++ { // live rows
		if err := l.writeBatch(randomRows(rng, 1000, 6*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if !l.derivedReady.Load() {
		t.Fatal("backfill did not finish")
	}

	ctx := context.Background()
	both := func(name string, f func() (any, error)) {
		t.Helper()
		l.derivedReady.Store(false)
		want, err := f()
		if err != nil {
			t.Fatalf("%s raw: %v", name, err)
		}
		l.derivedReady.Store(true)
		got, err := f()
		if err != nil {
			t.Fatalf("%s rolled: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rollups disagree with the log\n got %v\nwant %v", name, got, want)
		}
	}
	for _, window := range []time.Duration{
		37 * time.Minute, 3*time.Hour + 17*time.Minute, 24 * time.Hour,
		47*time.Hour + 5*time.Minute, 72 * time.Hour, 5*24*time.Hour + 11*time.Minute,
	} {
		since := time.Now().Add(-window)
		w := window.String()
		bucket := 10 * time.Minute
		if window > 24*time.Hour {
			bucket = time.Hour
		}
		if window > 72*time.Hour {
			bucket = 24 * time.Hour
		}
		both("timeline "+w, func() (any, error) { return l.Timeline(ctx, since, bucket) })
		both("top blocked "+w, func() (any, error) { return l.TopBlockedDomains(ctx, since, 50) })
		both("top clients "+w, func() (any, error) { return l.TopClients(ctx, since, 50) })
		both("blocks by list "+w, func() (any, error) { return l.BlocksByList(ctx, since) })
		both("totals "+w, func() (any, error) {
			total, blocked, err := l.Totals(ctx, since)
			return [2]int{total, blocked}, err
		})
		both("client names "+w, func() (any, error) {
			names, start, err := l.ClientNames(ctx, []string{"10.0.0.1", "10.0.0.3"}, since)
			sort.Slice(names, func(i, j int) bool {
				a, b := names[i], names[j]
				return a.Client+a.QName+a.Verdict < b.Client+b.QName+b.Verdict
			})
			return struct {
				Names []ClientName
				Start time.Time
			}{names, start}, err
		})
	}
}

// Long device windows come from the daily rollups: exact for the whole UTC
// days they cover, and they say where that window starts.
func TestClientOverviewLongWindowsSnapToDays(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch(randomRows(rng, 3000, 6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clients := []string{"10.0.0.1", "10.0.0.2"}
	since := time.Now().Add(-72*time.Hour - 3*time.Hour)

	got, err := l.ClientOverview(ctx, clients, since, 20)
	if err != nil {
		t.Fatal(err)
	}
	dayStart := time.UnixMilli(since.UnixMilli() / dayMs * dayMs)
	if !got.WindowStart.Equal(dayStart) {
		t.Fatalf("window start = %v, want the UTC day start %v", got.WindowStart, dayStart)
	}
	l.derivedReady.Store(false) // the raw answer over the snapped window
	want, err := l.ClientOverview(ctx, clients, dayStart, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != want.Total || got.Blocked != want.Blocked ||
		!reflect.DeepEqual(got.TopAllowed, want.TopAllowed) || !reflect.DeepEqual(got.TopBlocked, want.TopBlocked) {
		t.Errorf("day rollup %+v\n differs from the raw window %+v", got, want)
	}

	l.derivedReady.Store(true)
	short, err := l.ClientOverview(ctx, clients, time.Now().Add(-6*time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(short.WindowStart) > 6*time.Hour+time.Minute {
		t.Errorf("a short window was snapped: start %v", short.WindowStart)
	}
}

// Rollup rows past retention are pruned with the log.
func TestPruneTrimsRollups(t *testing.T) {
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch([]Entry{
		{Time: time.Now().Add(-40 * 24 * time.Hour), Client: "10.0.0.1", QName: "old.example", QType: "A", Verdict: VerdictBlocked, List: "x"},
		{Time: time.Now(), Client: "10.0.0.1", QName: "new.example", QType: "A", Verdict: VerdictBlocked, List: "x"},
	}); err != nil {
		t.Fatal(err)
	}
	l.prune() // retention 30 days
	for _, table := range []string{"roll_hour", "roll_hour_blocked", "roll_day_client"} {
		var n int
		if err := l.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s holds %d rows after the prune, want 1 (the recent one)", table, n)
		}
	}
}
