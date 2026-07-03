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
