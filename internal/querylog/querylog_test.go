package querylog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testEntry(qname, verdict string) Entry {
	return Entry{
		Time:    time.Now(),
		Client:  "192.168.1.10",
		QName:   qname,
		QType:   "A",
		Verdict: verdict,
	}
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestRingRecentNewestFirst(t *testing.T) {
	l, err := Open(Options{RingSize: 4, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	for _, name := range []string{"a.example", "b.example", "c.example", "d.example", "e.example"} {
		l.Record(testEntry(name, VerdictAllowed))
	}
	waitFor(t, func() bool { return len(l.Recent(0)) == 4 })

	got := l.Recent(2)
	if len(got) != 2 || got[0].QName != "e.example" || got[1].QName != "d.example" {
		t.Errorf("Recent(2) = %+v, want e.example then d.example", got)
	}
	// Ring wrapped: oldest entry (a.example) must be gone.
	all := l.Recent(0)
	if len(all) != 4 || all[3].QName != "b.example" {
		t.Errorf("Recent(0) tail = %+v, want b.example", all)
	}
}

func TestStatsCounters(t *testing.T) {
	l, err := Open(Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	l.Record(testEntry("x.example", VerdictBlocked))
	l.Record(testEntry("y.example", VerdictAllowed))
	l.Record(testEntry("z.example", VerdictBlocked))

	total, blocked, dropped := l.Stats()
	if total != 3 || blocked != 2 || dropped != 0 {
		t.Errorf("Stats() = %d,%d,%d want 3,2,0", total, blocked, dropped)
	}
}

func TestSubscribeReceivesLiveEntries(t *testing.T) {
	l, err := Open(Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	ch, cancel := l.Subscribe()
	defer cancel()

	l.Record(testEntry("live.example", VerdictBlocked))
	select {
	case e := <-ch:
		if e.QName != "live.example" {
			t.Errorf("got %q, want live.example", e.QName)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live entry received")
	}

	cancel()
	l.Record(testEntry("after-cancel.example", VerdictAllowed))
	// Must not panic or deliver after cancel; drain whatever raced in.
	time.Sleep(20 * time.Millisecond)
}

func TestPersistAndHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	l, err := Open(Options{RingSize: 10, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}

	l.Record(testEntry("persisted.example", VerdictBlocked))
	// Close performs the final flush.
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(Options{RingSize: 10, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()

	got, err := l2.QueryHistory(context.Background(), 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].QName != "persisted.example" || got[0].Verdict != VerdictBlocked {
		t.Errorf("QueryHistory = %+v, want one persisted.example blocked entry", got)
	}
}

func TestEphemeralWritesNothing(t *testing.T) {
	l, err := Open(Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	l.Record(testEntry("mem-only.example", VerdictAllowed))
	got, err := l.QueryHistory(context.Background(), 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("ephemeral log returned history: %+v", got)
	}
}
