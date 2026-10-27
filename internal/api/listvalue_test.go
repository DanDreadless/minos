package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/filter"
	"minos/internal/querylog"
)

func TestListValueReport(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()

	// Fewer than two lists: nothing to compare.
	rec := doJSON(t, r, "GET", "/api/lists/value", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("empty report: %d %s", rec.Code, rec.Body)
	}

	b := filter.NewBuilder()
	b.Track([]string{"big", "small"})
	b.AddAllow("allowlist", "allowed.ads.com")
	for _, d := range []string{"ads.com", "tracker.net", "only-big.org"} {
		b.AddDeny("big", d)
	}
	b.AddDeny("small", "x.ads.com")     // Covered by big's ads.com
	b.AddDeny("small", "only-small.io") // Small's one unique rule
	s.engine.Swap(b.Build())

	now := time.Now()
	for _, e := range []struct {
		name string
		n    int
	}{{"x.ads.com", 5}, {"only-big.org", 3}, {"only-small.io", 2}, {"allowed.ads.com", 4}, {"denylisted.example", 7}} {
		for range e.n {
			s.qlog.Record(querylog.Entry{Time: now, Client: "10.0.0.9", QName: e.name, QType: "A", Verdict: querylog.VerdictBlocked})
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(s.qlog.Recent(0)) < 21 {
		time.Sleep(5 * time.Millisecond)
	}

	rec = doJSON(t, r, "GET", "/api/lists/value?days=7", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Days           int         `json:"days"`
		Compared       []string    `json:"compared"`
		BlockedLookups int         `json:"blocked_lookups"`
		Lists          []listValue `json:"lists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Days != 7 || len(got.Compared) != 2 || got.BlockedLookups != 21 || len(got.Lists) != 2 {
		t.Fatalf("report %+v", got)
	}
	big, small := got.Lists[0], got.Lists[1]
	// Big blocks x.ads.com (5) and only-big.org (3); allowed.ads.com is
	// allowed now, and the denylisted name is no list's.
	if big.Rules != 3 || big.UniqueRules != 3 || big.Blocks != 8 || big.OnlyBlocks != 3 ||
		len(big.OnlyExamples) != 1 || big.OnlyExamples[0].QName != "only-big.org" {
		t.Errorf("big %+v", big)
	}
	if small.Rules != 2 || small.UniqueRules != 1 || small.Blocks != 7 || small.OnlyBlocks != 2 ||
		len(small.CoveredBy) != 1 || small.CoveredBy[0] != (listCoverage{List: "big", Rules: 1}) {
		t.Errorf("small %+v", small)
	}

	if big.Security || small.Security {
		t.Error("ordinary lists marked as security lists")
	}

	if rec := doJSON(t, r, "GET", "/api/lists/value?days=0", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("days=0: %d", rec.Code)
	}
}
