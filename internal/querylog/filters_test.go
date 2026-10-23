package querylog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func filterRows(now time.Time) []Entry {
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }
	return []Entry{
		{Time: at(time.Minute), Client: "10.0.0.1", QName: "a.example", QType: "A", Verdict: VerdictAllowed, Upstream: "1.1.1.1:853"},
		{Time: at(2 * time.Minute), Client: "10.0.0.1", QName: "a.example", QType: "AAAA", Verdict: VerdictAllowed, Upstream: "9.9.9.9:853"},
		{Time: at(3 * time.Minute), Client: "10.0.0.2", QName: "sub.a.example", QType: "A", Verdict: VerdictAllowed, Upstream: "1.1.1.1:853"},
		{Time: at(4 * time.Minute), Client: "10.0.0.2", QName: "ads.cdn.example", QType: "A", Verdict: VerdictBlocked, List: "x"},
		{Time: at(5 * 24 * time.Hour), Client: "10.0.0.1", QName: "a.example", QType: "A", Verdict: VerdictBlocked, List: "x"},
	}
}

func names(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.QName + "/" + e.QType
	}
	return out
}

// Every structured filter narrows the same way whether the dictionary plan
// or the plain scan answers, so each case runs both.
func TestHistoryStructuredFilters(t *testing.T) {
	now := time.Now()
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	if err := l.writeBatch(filterRows(now)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		f    HistoryFilter
		want []string
	}{
		{"exact domain, not its subdomains", HistoryFilter{Domain: "a.example"},
			[]string{"a.example/A", "a.example/AAAA", "a.example/A"}},
		{"exact domain, blocked", HistoryFilter{Domain: "a.example", Verdict: VerdictBlocked}, []string{"a.example/A"}},
		{"unknown domain", HistoryFilter{Domain: "nope.example"}, []string{}},
		{"since", HistoryFilter{Domain: "a.example", Since: now.Add(-time.Hour)},
			[]string{"a.example/A", "a.example/AAAA"}},
		{"qtype", HistoryFilter{QType: "AAAA"}, []string{"a.example/AAAA"}},
		{"upstream", HistoryFilter{Upstream: "1.1.1.1:853"}, []string{"a.example/A", "sub.a.example/A"}},
		{"exclude", HistoryFilter{Search: "example", Exclude: []string{"cdn", "sub."}},
			[]string{"a.example/A", "a.example/AAAA", "a.example/A"}},
		{"domain plus search", HistoryFilter{Domain: "a.example", Search: "10.0.0.2"}, []string{}},
	}
	ctx := context.Background()
	for _, c := range cases {
		for _, ready := range []bool{true, false} {
			l.derivedReady.Store(ready)
			got, err := l.QueryHistory(ctx, c.f, 100, time.Time{})
			if err != nil {
				t.Fatalf("%s (dictionary %v): %v", c.name, ready, err)
			}
			if g := names(got); len(g) != len(c.want) || (len(g) > 0 && !equal(g, c.want)) {
				t.Errorf("%s (dictionary %v): got %v, want %v", c.name, ready, g, c.want)
			}
		}
	}
	l.derivedReady.Store(true)
	if plan, _, _ := l.planSearch(ctx, HistoryFilter{Domain: "nope.example"}); plan != planEmpty {
		t.Errorf("an unknown exact domain planned %v, want the instant empty answer", plan)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Export reads in one statement: rows sharing a millisecond — which a
// ts cursor would skip at a page boundary — all arrive, and past a page.
func TestExportHistoryLosesNothing(t *testing.T) {
	l := openReady(t, filepath.Join(t.TempDir(), "q.db"))
	defer l.Close()
	ts := time.Now().Add(-time.Minute)
	rows := make([]Entry, 2500)
	for i := range rows {
		rows[i] = Entry{Time: ts.Add(-time.Duration(i/5) * time.Millisecond), Client: "10.0.0.1",
			QName: "same.example", QType: "A", Verdict: VerdictAllowed}
	}
	if err := l.writeBatch(rows); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := l.ExportHistory(context.Background(), HistoryFilter{Domain: "same.example"}, 0, func(Entry) error {
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != len(rows) {
		t.Errorf("exported %d rows, want %d", n, len(rows))
	}
}
