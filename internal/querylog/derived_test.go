package querylog

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// openReady opens a log and waits for indexes and the backfill.
func openReady(t *testing.T, path string) *Log {
	t.Helper()
	l, err := Open(Options{RingSize: 10, DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	<-l.Ready()
	l.waitBackfill()
	return l
}

// sampleRows: names with known frequencies — a common one, a rare one, one
// only ever blocked, one only ever allowed.
func sampleRows(n int, start time.Time) []Entry {
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		e := Entry{Time: start.Add(time.Duration(i) * time.Second), Client: "10.0.0.1", QType: "A", Verdict: VerdictAllowed}
		switch {
		case i%50 == 0:
			e.QName = "rare-tracker.example"
			e.Verdict, e.List = VerdictBlocked, "test"
		case i%7 == 0:
			e.QName = fmt.Sprintf("cdn%d.common.example", i%5)
		default:
			e.QName = fmt.Sprintf("site%d.example.org", i%300)
		}
		out = append(out, e)
	}
	return out
}

// dictionary returns qnames as name → {hits, blocked}.
func dictionary(t *testing.T, l *Log) map[string][2]int64 {
	t.Helper()
	rows, err := l.db.Query(`SELECT name, hits, blocked FROM qnames`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var n string
		var h, b int64
		if err := rows.Scan(&n, &h, &b); err != nil {
			t.Fatal(err)
		}
		out[n] = [2]int64{h, b}
	}
	return out
}

// truth recomputes the dictionary from the raw log.
func truth(t *testing.T, l *Log) map[string][2]int64 {
	t.Helper()
	rows, err := l.db.Query(`SELECT qname, COUNT(*), SUM(verdict = 'blocked') FROM querylog GROUP BY qname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var n string
		var h, b int64
		if err := rows.Scan(&n, &h, &b); err != nil {
			t.Fatal(err)
		}
		out[n] = [2]int64{h, b}
	}
	return out
}

// rawInsert writes rows the way an older Minos did: no derived tables.
func rawInsert(t *testing.T, l *Log, rows []Entry) {
	t.Helper()
	for _, e := range rows {
		if _, err := l.db.Exec(`INSERT INTO querylog (ts, client, qname, qtype, verdict, list) VALUES (?, ?, ?, ?, ?, ?)`,
			e.Time.UnixMilli(), e.Client, e.QName, e.QType, e.Verdict, e.List); err != nil {
			t.Fatal(err)
		}
	}
}

func assertExact(t *testing.T, l *Log, when string) {
	t.Helper()
	if got, want := dictionary(t, l), truth(t, l); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: dictionary disagrees with the log (%d vs %d names)", when, len(got), len(want))
	}
}

// Upgrading onto an existing log: everything already there is backfilled,
// and later live writes are added on top — counted exactly once each.
func TestDictionaryExactAcrossUpgradeAndLiveWrites(t *testing.T) {
	old := backfillChunkRows
	backfillChunkRows = 97 // many chunks, odd boundaries
	defer func() { backfillChunkRows = old }()
	path := filepath.Join(t.TempDir(), "q.db")

	l := openReady(t, path)
	rawInsert(t, l, sampleRows(1500, time.Now().Add(-48*time.Hour)))  // "the log before the upgrade"
	if _, err := l.db.Exec(`DELETE FROM querylog_meta`); err != nil { // as if derived never existed
		t.Fatal(err)
	}
	l.Close()

	l = openReady(t, path)
	defer l.Close()
	assertExact(t, l, "after the upgrade backfill")
	if err := l.writeBatch(sampleRows(700, time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	assertExact(t, l, "after live writes")
}

// A downgrade writes rows the derived tables never saw; the next upgrade
// backfills exactly that gap — no double counting of what was already done.
func TestDictionaryCoversRowsAnOlderMinosWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")
	l := openReady(t, path)
	if err := l.writeBatch(sampleRows(800, time.Now().Add(-10*time.Hour))); err != nil {
		t.Fatal(err)
	}
	rawInsert(t, l, sampleRows(400, time.Now().Add(-5*time.Hour))) // the older binary's rows
	l.Close()

	l = openReady(t, path)
	defer l.Close()
	assertExact(t, l, "after re-upgrading")
}

// A crash part-way through the backfill resumes where it stopped: the chunk
// and its progress commit together, so nothing is skipped or repeated.
func TestBackfillResumesExactlyAfterACrash(t *testing.T) {
	old := backfillChunkRows
	backfillChunkRows = 100
	defer func() { backfillChunkRows = old }()
	path := filepath.Join(t.TempDir(), "q.db")

	l := openReady(t, path)
	rawInsert(t, l, sampleRows(1000, time.Now().Add(-48*time.Hour)))
	if _, err := l.db.Exec(`DELETE FROM querylog_meta`); err != nil {
		t.Fatal(err)
	}
	l.Close()

	// Reopen without the background goroutine finishing: run three chunks
	// by hand, then "crash".
	db, err := openDB(path, DefaultMemoryMB)
	if err != nil {
		t.Fatal(err)
	}
	partial := &Log{db: db}
	for i := 0; i < 3; i++ {
		if _, _, err := partial.backfillChunk(); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	l = openReady(t, path)
	defer l.Close()
	assertExact(t, l, "after resuming")
}

// The dictionary plans must return exactly what the old scan returned.
func TestSearchPlansMatchTheScan(t *testing.T) {
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch(sampleRows(3000, time.Now().Add(-3*time.Hour))); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, f := range []HistoryFilter{
		{Search: "rare-tracker"},                          // rare: names plan
		{Search: "rare-tracker", Verdict: VerdictBlocked}, // rare + verdict
		{Search: "rare-tracker", Verdict: VerdictAllowed}, // never allowed: empty plan
		{Search: "nothing-like-this"},                     // absent: empty plan
		{Search: "common"},                                // names plan or scan
		{Search: "example"},                               // matches everything
		{Search: "10.0.0"},                                // a client address
		{Search: "site12.", Verdict: VerdictBlocked},      // never blocked: empty
	} {
		l.derivedReady.Store(false)
		want, err := l.QueryHistory(ctx, f, 50, time.Time{})
		if err != nil {
			t.Fatalf("%+v scan: %v", f, err)
		}
		l.derivedReady.Store(true)
		plan, _, err := l.planSearch(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := l.QueryHistory(ctx, f, 50, time.Time{})
		if err != nil {
			t.Fatalf("%+v plan %d: %v", f, plan, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%+v: plan %d returned %d rows, the scan %d", f, plan, len(got), len(want))
		}
		for i := range got {
			if got[i].QName != want[i].QName || !got[i].Time.Equal(want[i].Time) {
				t.Fatalf("%+v: row %d differs: %+v vs %+v", f, i, got[i], want[i])
			}
		}
	}
}

func TestSearchPlansChooseTheCheapPath(t *testing.T) {
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch(sampleRows(3000, time.Now().Add(-3*time.Hour))); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		f    HistoryFilter
		want searchPlan
	}{
		{HistoryFilter{Search: "zzz-absent"}, planEmpty},
		{HistoryFilter{Search: "rare-tracker"}, planNames},
		{HistoryFilter{Search: "rare-tracker", Verdict: VerdictAllowed}, planEmpty},
		{HistoryFilter{Search: "10.0.0"}, planScan}, // a device's rows are plentiful
		{HistoryFilter{Search: "rare-tracker", Clients: []string{"10.0.0.1"}}, planScan},
	} {
		got, _, err := l.planSearch(ctx, tc.f)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%+v: plan %d, want %d", tc.f, got, tc.want)
		}
	}
}

// A brand-new name is searchable as soon as its batch is flushed.
func TestNewNameSearchableAfterFlush(t *testing.T) {
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch([]Entry{{Time: time.Now(), Client: "10.0.0.9", QName: "fresh.example", QType: "A", Verdict: VerdictAllowed}}); err != nil {
		t.Fatal(err)
	}
	got, err := l.QueryHistory(context.Background(), HistoryFilter{Search: "fresh.ex"}, 10, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("fresh name: %d rows, err %v; want 1", len(got), err)
	}
}

func TestPruneDropsOldNames(t *testing.T) {
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch([]Entry{
		{Time: time.Now().Add(-40 * 24 * time.Hour), Client: "10.0.0.9", QName: "ancient.example", QType: "A", Verdict: VerdictAllowed},
		{Time: time.Now(), Client: "10.0.0.9", QName: "recent.example", QType: "A", Verdict: VerdictAllowed},
	}); err != nil {
		t.Fatal(err)
	}
	l.prune() // retention 30 days
	d := dictionary(t, l)
	if _, ok := d["ancient.example"]; ok {
		t.Error("a name last seen before retention survived the prune")
	}
	if _, ok := d["recent.example"]; !ok {
		t.Error("a recent name was pruned")
	}
}

func TestClientLike(t *testing.T) {
	for term, want := range map[string]bool{
		"192.168.1": true, "fe80::1": true, "cafe": true, "apple.com": false, "10.0.0.x": false,
	} {
		if got := clientLike(term); got != want {
			t.Errorf("clientLike(%q) = %v, want %v", term, got, want)
		}
	}
}
