package querylog

import (
	"testing"
	"time"
)

func record(l *Log, qname, client, verdict string, at time.Time) {
	l.Record(Entry{Time: at, Client: client, QName: qname, QType: "A", Verdict: verdict})
}

// drain waits until the writer goroutine has consumed everything Record sent.
func drain(t *testing.T, l *Log, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.Recent(0)) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("writer did not consume %d entries in time", want)
}

func seed(t *testing.T, l *Log) {
	t.Helper()
	now := time.Now()
	record(l, "ads.example.com", "10.0.0.1", VerdictBlocked, now.Add(-30*time.Minute))
	record(l, "ads.example.com", "10.0.0.2", VerdictBlocked, now.Add(-20*time.Minute))
	record(l, "tracker.example.com", "10.0.0.1", VerdictBlocked, now.Add(-10*time.Minute))
	record(l, "github.com", "10.0.0.1", VerdictAllowed, now.Add(-5*time.Minute))
	drain(t, l, 4)
}

func assertAggregates(t *testing.T, l *Log) {
	t.Helper()
	since := time.Now().Add(-time.Hour)

	top, err := l.TopBlockedDomains(t.Context(), since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].QName != "ads.example.com" || top[0].Count != 2 {
		t.Errorf("top blocked = %+v, want ads.example.com x2 first", top)
	}

	clients, err := l.TopClients(t.Context(), since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 2 || clients[0].Client != "10.0.0.1" ||
		clients[0].Total != 3 || clients[0].Blocked != 2 {
		t.Errorf("top clients = %+v, want 10.0.0.1 total=3 blocked=2 first", clients)
	}

	// The client drill-down spans a device's whole IP set…
	ov, err := l.ClientOverview(t.Context(), []string{"10.0.0.1", "10.0.0.2"}, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Total != 4 || ov.Blocked != 3 {
		t.Errorf("overview totals = %d/%d, want 4/3", ov.Total, ov.Blocked)
	}
	if len(ov.TopBlocked) != 2 || ov.TopBlocked[0].QName != "ads.example.com" || ov.TopBlocked[0].Count != 2 {
		t.Errorf("overview top blocked = %+v, want ads.example.com x2 first", ov.TopBlocked)
	}
	if len(ov.TopAllowed) != 1 || ov.TopAllowed[0].QName != "github.com" {
		t.Errorf("overview top allowed = %+v, want github.com", ov.TopAllowed)
	}
	// …and scopes to exactly the addresses asked for.
	ov, err = l.ClientOverview(t.Context(), []string{"10.0.0.2"}, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Total != 1 || ov.Blocked != 1 || len(ov.TopAllowed) != 0 {
		t.Errorf("single-client overview = %+v, want 1/1 with no allowed", ov)
	}
	// No clients = empty result, not an error (and not everyone's traffic).
	ov, err = l.ClientOverview(t.Context(), nil, since, 10)
	if err != nil || ov.Total != 0 {
		t.Errorf("empty client set = %+v (err %v), want zero overview", ov, err)
	}

	// The digest's data-source methods (builtin-typed, for notify's interface).
	dTotal, dBlocked, err := l.Totals(t.Context(), since)
	if err != nil || dTotal != 4 || dBlocked != 3 {
		t.Errorf("Totals = %d/%d (err %v), want 4/3", dTotal, dBlocked, err)
	}
	domains, counts, err := l.TopBlockedSummary(t.Context(), since, 3)
	if err != nil || len(domains) != 2 || domains[0] != "ads.example.com" || counts[0] != 2 {
		t.Errorf("TopBlockedSummary = %v/%v (err %v), want ads.example.com x2 first", domains, counts, err)
	}
	busiest, queries, err := l.BusiestClient(t.Context(), since)
	if err != nil || busiest != "10.0.0.1" || queries != 3 {
		t.Errorf("BusiestClient = %s/%d (err %v), want 10.0.0.1/3", busiest, queries, err)
	}
	newClients, err := l.NewClientsSince(t.Context(), since)
	if err != nil || newClients != 2 {
		t.Errorf("NewClientsSince = %d (err %v), want 2 (both clients are new)", newClients, err)
	}
	// A client seen before the window is not "new" inside it.
	if n, err := l.NewClientsSince(t.Context(), time.Now().Add(-15*time.Minute)); err != nil || n != 0 {
		t.Errorf("NewClientsSince(-15m) = %d (err %v), want 0 (all first-seen earlier)", n, err)
	}

	timeline, err := l.Timeline(t.Context(), since, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// 1h window at 10min buckets: 6-7 buckets including empty fill.
	if len(timeline) < 6 {
		t.Fatalf("timeline has %d buckets, want >= 6 (empty fill)", len(timeline))
	}
	var total, blocked int
	for _, b := range timeline {
		total += b.Total
		blocked += b.Blocked
	}
	if total != 4 || blocked != 3 {
		t.Errorf("timeline sums total=%d blocked=%d, want 4/3", total, blocked)
	}
}

func TestAggregatesFromRing(t *testing.T) {
	l, err := Open(Options{RingSize: 100, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	seed(t, l)
	assertAggregates(t, l)
}

func TestAggregatesFromSQLite(t *testing.T) {
	path := t.TempDir() + "/q.db"
	l, err := Open(Options{RingSize: 100, DBPath: path, RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, l)
	if err := l.Close(); err != nil { // Close flushes the batch to SQLite
		t.Fatal(err)
	}
	// A fresh Log with an empty ring proves the aggregates read the database.
	reopened, err := Open(Options{RingSize: 100, DBPath: path, RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertAggregates(t, reopened)
}

func seedListBlocks(t *testing.T, l *Log) {
	t.Helper()
	now := time.Now()
	rec := func(qname, list, verdict string, at time.Time) {
		l.Record(Entry{Time: at, Client: "10.0.0.1", QName: qname, QType: "A", Verdict: verdict, List: list})
	}
	rec("ads.example.com", "hagezi", VerdictBlocked, now.Add(-40*time.Minute))
	rec("ads2.example.com", "hagezi", VerdictBlocked, now.Add(-30*time.Minute))
	rec("tracker.example.com", "oisd", VerdictBlocked, now.Add(-20*time.Minute))
	// A pardon attribution (allowed verdict with a list) is not a block…
	rec("cdn.example.com", "allowlist", VerdictAllowed, now.Add(-10*time.Minute))
	// …and an unattributed block counts toward no list.
	rec("odd.example.com", "", VerdictBlocked, now.Add(-5*time.Minute))
	drain(t, l, 5)
}

func assertBlocksByList(t *testing.T, l *Log) {
	t.Helper()
	got, err := l.BlocksByList(t.Context(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []ListStat{{List: "hagezi", Count: 2}, {List: "oisd", Count: 1}}
	if len(got) != len(want) {
		t.Fatalf("blocks by list = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("blocks by list[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBlocksByListFromRing(t *testing.T) {
	l, err := Open(Options{RingSize: 100, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	seedListBlocks(t, l)
	assertBlocksByList(t, l)
}

func TestBlocksByListFromSQLite(t *testing.T) {
	path := t.TempDir() + "/q.db"
	l, err := Open(Options{RingSize: 100, DBPath: path, RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	seedListBlocks(t, l)
	if err := l.Close(); err != nil { // Close flushes the batch to SQLite
		t.Fatal(err)
	}
	reopened, err := Open(Options{RingSize: 100, DBPath: path, RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertBlocksByList(t, reopened)
}

func TestResizePreservesNewest(t *testing.T) {
	l, err := Open(Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	now := time.Now()
	for i := 0; i < 8; i++ {
		record(l, "d"+string(rune('0'+i))+".example.com", "c", VerdictAllowed, now.Add(time.Duration(i)*time.Second))
	}
	drain(t, l, 8)

	l.Resize(3) // shrink: keep the 3 newest
	got := l.Recent(0)
	if len(got) != 3 {
		t.Fatalf("after shrink Recent = %d entries, want 3", len(got))
	}
	if got[0].QName != "d7.example.com" || got[2].QName != "d5.example.com" {
		t.Errorf("shrink kept wrong entries: %v, %v", got[0].QName, got[2].QName)
	}

	l.Resize(50) // grow: nothing lost
	got = l.Recent(0)
	if len(got) != 3 || got[0].QName != "d7.example.com" {
		t.Fatalf("after grow Recent = %d entries first=%v, want 3/d7", len(got), got[0].QName)
	}
	// Ring still functions after resizes.
	record(l, "new.example.com", "c", VerdictAllowed, now.Add(time.Minute))
	drain(t, l, 4)
	if l.Recent(1)[0].QName != "new.example.com" {
		t.Error("ring broken after resize: newest entry missing")
	}
}
