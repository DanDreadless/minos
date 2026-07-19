package querylog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// openRawForTest opens the SQLite file directly (the driver is registered by
// the package's blank import), for building pre-migration schemas.
func openRawForTest(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }

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

// TestMigrateAddsAuditColumns: a query-log database created before the audit
// columns existed upgrades in place on open, and would-block entries then
// persist and filter.
func TestMigrateAddsAuditColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")

	// Recreate the pre-audit schema by hand.
	old, err := openRawForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE querylog (
		id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, client TEXT NOT NULL,
		qname TEXT NOT NULL, qtype TEXT NOT NULL, verdict TEXT NOT NULL,
		list TEXT NOT NULL DEFAULT '', rule TEXT NOT NULL DEFAULT '',
		upstream TEXT NOT NULL DEFAULT '', duration_ms REAL NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO querylog (ts, client, qname, qtype, verdict)
		VALUES (?, '10.0.0.1', 'legacy.example', 'A', 'allowed')`,
		time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	l, err := Open(Options{RingSize: 10, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatalf("open pre-audit db: %v", err)
	}
	e := testEntry("strict.example", VerdictAllowed)
	e.AuditList, e.AuditRule = "hagezi-pro", "strict.example"
	l.Record(e)
	if err := l.Close(); err != nil { // flushes the batch
		t.Fatal(err)
	}

	reopened, err := Open(Options{RingSize: 10, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.QueryHistory(context.Background(), HistoryFilter{WouldBlock: true}, 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].QName != "strict.example" ||
		got[0].AuditList != "hagezi-pro" || got[0].AuditRule != "strict.example" {
		t.Errorf("would-block history = %+v, want the audited strict.example entry", got)
	}
	// The legacy row survives with empty audit fields and stays out of the
	// would-block view.
	all, err := reopened.QueryHistory(context.Background(), HistoryFilter{}, 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("full history = %d rows, want 2 (legacy + audited)", len(all))
	}
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

	got, err := l2.QueryHistory(context.Background(), HistoryFilter{}, 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].QName != "persisted.example" || got[0].Verdict != VerdictBlocked {
		t.Errorf("QueryHistory = %+v, want one persisted.example blocked entry", got)
	}
}

func TestQueryHistoryFilters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	l, err := Open(Options{RingSize: 50, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Entry{Client: "10.0.0.5", QName: "ads.example", Verdict: VerdictBlocked, Time: time.Now()})
	l.Record(Entry{Client: "10.0.0.5", QName: "news.example", Verdict: VerdictAllowed, Time: time.Now()})
	l.Record(Entry{Client: "10.0.0.9", QName: "ads.example", Verdict: VerdictBlocked, Time: time.Now()})
	if err := l.Close(); err != nil { // flush to SQLite
		t.Fatal(err)
	}
	l2, err := Open(Options{RingSize: 50, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()

	count := func(f HistoryFilter) int {
		got, err := l2.QueryHistory(context.Background(), f, 100, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if n := count(HistoryFilter{Search: "10.0.0.5"}); n != 2 {
		t.Errorf("client filter: got %d, want 2", n)
	}
	if n := count(HistoryFilter{Search: "ads.example"}); n != 2 {
		t.Errorf("qname filter: got %d, want 2", n)
	}
	if n := count(HistoryFilter{Search: "10.0.0.5", Verdict: VerdictBlocked}); n != 1 {
		t.Errorf("client+verdict filter: got %d, want 1", n)
	}
	if n := count(HistoryFilter{Verdict: VerdictBlocked}); n != 2 {
		t.Errorf("verdict filter: got %d, want 2", n)
	}
	// LIKE metacharacters in the search must be matched literally.
	if n := count(HistoryFilter{Search: "%"}); n != 0 {
		t.Errorf("literal %% search: got %d, want 0", n)
	}
	// Exact multi-client filter (a device's IP set) is distinct from Search:
	// it matches whole client addresses, not substrings.
	if n := count(HistoryFilter{Clients: []string{"10.0.0.5", "10.0.0.9"}}); n != 3 {
		t.Errorf("two-client filter: got %d, want 3", n)
	}
	if n := count(HistoryFilter{Clients: []string{"10.0.0.9"}}); n != 1 {
		t.Errorf("single-client filter: got %d, want 1", n)
	}
	if n := count(HistoryFilter{Clients: []string{"10.0.0.5"}, Verdict: VerdictBlocked}); n != 1 {
		t.Errorf("client+verdict filter: got %d, want 1", n)
	}
	// An exact filter does not substring-match: "10.0.0" hits nothing.
	if n := count(HistoryFilter{Clients: []string{"10.0.0"}}); n != 0 {
		t.Errorf("exact filter must not substring-match: got %d, want 0", n)
	}
}

func TestQueryHistoryListFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	l, err := Open(Options{RingSize: 50, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	l.Record(Entry{Client: "10.0.0.5", QName: "ads.example", Verdict: VerdictBlocked, List: "StevenBlack", Rule: "ads.example", Time: now})
	l.Record(Entry{Client: "10.0.0.5", QName: "cdn.example", Verdict: VerdictAllowed, List: "service:netflix", Rule: "cdn.example", Time: now})
	l.Record(Entry{Client: "10.0.0.5", QName: "maybe.example", Verdict: VerdictAllowed, AuditList: "strict-audit", AuditRule: "maybe.example", Time: now})
	l.Record(Entry{Client: "10.0.0.5", QName: "plain.example", Verdict: VerdictAllowed, Time: now})
	if err := l.Close(); err != nil { // flush to SQLite
		t.Fatal(err)
	}
	l2, err := Open(Options{RingSize: 50, DBPath: dbPath, RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()

	count := func(f HistoryFilter) int {
		got, err := l2.QueryHistory(context.Background(), f, 100, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	// The filter matches the condemning list, a pardoning list on an allowed
	// entry, and an audit ("would block") attribution alike.
	if n := count(HistoryFilter{List: "StevenBlack"}); n != 1 {
		t.Errorf("blocked-list filter: got %d, want 1", n)
	}
	if n := count(HistoryFilter{List: "service:netflix"}); n != 1 {
		t.Errorf("pardon-list filter: got %d, want 1", n)
	}
	if n := count(HistoryFilter{List: "strict-audit"}); n != 1 {
		t.Errorf("audit-list filter: got %d, want 1", n)
	}
	if n := count(HistoryFilter{List: "no-such-list"}); n != 0 {
		t.Errorf("unknown list: got %d, want 0", n)
	}
	// Exact match, never substring.
	if n := count(HistoryFilter{List: "Steven"}); n != 0 {
		t.Errorf("list filter must not substring-match: got %d, want 0", n)
	}
	if n := count(HistoryFilter{List: "StevenBlack", Verdict: VerdictAllowed}); n != 0 {
		t.Errorf("list+verdict compose: got %d, want 0", n)
	}
}

func TestEphemeralWritesNothing(t *testing.T) {
	l, err := Open(Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	l.Record(testEntry("mem-only.example", VerdictAllowed))
	got, err := l.QueryHistory(context.Background(), HistoryFilter{}, 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("ephemeral log returned history: %+v", got)
	}
}
