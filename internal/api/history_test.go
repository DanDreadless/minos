package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/lists"
	"minos/internal/querylog"
)

// newDBTestServer is newTestServer over an on-disk query log holding rows.
// Close flushes what Record buffered, so the rows are written by reopening.
func newDBTestServer(t *testing.T, rows []querylog.Entry) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "q.db")
	ql, err := querylog.Open(querylog.Options{RingSize: 100, DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range rows {
		ql.Record(e)
	}
	if err := ql.Close(); err != nil {
		t.Fatal(err)
	}
	ql, err = querylog.Open(querylog.Options{RingSize: 100, DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ql.Close() })
	<-ql.Ready()
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	return New(engine, ql, store, lists.NewManager(engine, store), clients.NewRegistry(),
		nil, nil, nil, nil, "test", "")
}

func TestQueryLogExport(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	s := newDBTestServer(t, []querylog.Entry{
		{Time: now, Client: "10.0.0.1", QName: "a.example", QType: "A", Verdict: querylog.VerdictAllowed, Upstream: "1.1.1.1:853"},
		{Time: now.Add(-time.Second), Client: "10.0.0.2", QName: "ads.example", QType: "AAAA",
			Verdict: querylog.VerdictBlocked, List: "x", Rule: "@@||ads.example^"},
	})
	r := s.Router()

	rec := doJSON(t, r, "GET", "/api/querylog/export", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("csv: status = %d: %s", rec.Code, rec.Body)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	recs, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || recs[0][2] != "domain" || recs[1][2] != "a.example" {
		t.Fatalf("csv = %v", recs)
	}
	if recs[2][6] != "'@@||ads.example^" {
		t.Errorf("a formula-like cell was not defused: %q", recs[2][6])
	}

	rec = doJSON(t, r, "GET", "/api/querylog/export?format=json&verdict=blocked&qtype=aaaa", "", nil)
	var got []querylog.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v: %s", err, rec.Body)
	}
	if len(got) != 1 || got[0].QName != "ads.example" {
		t.Errorf("filtered json = %+v", got)
	}

	rec = doJSON(t, r, "GET", "/api/querylog/export?format=json&domain=nothing.example", "", nil)
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("empty json export = %q, want []", body)
	}
	rec = doJSON(t, r, "GET", "/api/querylog/export?domain=nothing.example", "", nil)
	if recs, _ := csv.NewReader(rec.Body).ReadAll(); len(recs) != 1 {
		t.Errorf("empty csv export should be just the header, got %v", recs)
	}
}

func TestQueryLogFilterParams(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()
	for _, ok := range []string{
		"?domain=Example.COM.", "?qtype=https", "?upstream=1.1.1.1:853",
		"?exclude=cdn,akamai", "?since=1700000000000", "?verdict=all",
	} {
		if rec := doJSON(t, r, "GET", "/api/querylog/history"+ok, "", nil); rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200: %s", ok, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{
		"/api/querylog/history?domain=..", "/api/querylog/history?qtype=A%3BDROP",
		"/api/querylog/history?since=yesterday", "/api/querylog/history?verdict=condemned",
		"/api/querylog/export?format=xml", "/api/querylog/export?limit=0",
	} {
		if rec := doJSON(t, r, "GET", path, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rec.Code)
		}
	}
}
