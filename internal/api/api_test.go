package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/lists"
	"minos/internal/querylog"
)

func newTestServer(t *testing.T, token string) (*Server, *config.Store) {
	t.Helper()
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		if err := store.Update(func(c *config.Config) error {
			c.API.Token = token
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	engine := filter.NewEngine()
	qlog, err := querylog.Open(querylog.Options{RingSize: 100, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = qlog.Close() })
	mgr := lists.NewManager(engine, store)
	return New(engine, qlog, store, mgr, nil, "test"), store
}

func doJSON(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStatusEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	rec := doJSON(t, s.Router(), "GET", "/api/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != "test" || got["paused"] != false {
		t.Errorf("unexpected status body: %v", got)
	}
}

func TestAuthToken(t *testing.T) {
	s, _ := newTestServer(t, "sekrit")
	r := s.Router()

	if rec := doJSON(t, r, "GET", "/api/status", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/status", "", map[string]string{"X-Api-Token": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/status", "", map[string]string{"X-Api-Token": "sekrit"}); rec.Code != http.StatusOK {
		t.Errorf("header token: status = %d, want 200", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/status", "", map[string]string{"Authorization": "Bearer sekrit"}); rec.Code != http.StatusOK {
		t.Errorf("bearer token: status = %d, want 200", rec.Code)
	}
}

func TestAllowlistCRUD(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "POST", "/api/allowlist", `{"domain":"Good.Example.COM"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: status = %d body=%s", rec.Code, rec.Body)
	}
	// Normalized, persisted in config.
	if got := store.Get().Blocking.Allowlist; len(got) != 1 || got[0] != "good.example.com" {
		t.Fatalf("allowlist in config = %v", got)
	}

	// Duplicate add is idempotent.
	rec = doJSON(t, r, "POST", "/api/allowlist", `{"domain":"good.example.com"}`, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("duplicate add: status = %d", rec.Code)
	}

	rec = doJSON(t, r, "GET", "/api/allowlist", "", nil)
	var domains []string
	if err := json.Unmarshal(rec.Body.Bytes(), &domains); err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 || domains[0] != "good.example.com" {
		t.Errorf("GET allowlist = %v", domains)
	}

	rec = doJSON(t, r, "DELETE", "/api/allowlist/good.example.com", "", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("delete: status = %d", rec.Code)
	}
	if got := store.Get().Blocking.Allowlist; len(got) != 0 {
		t.Errorf("allowlist after delete = %v", got)
	}
	rec = doJSON(t, r, "DELETE", "/api/allowlist/good.example.com", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: status = %d, want 404", rec.Code)
	}

	rec = doJSON(t, r, "POST", "/api/allowlist", `{"domain":"not a domain"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid domain: status = %d, want 400", rec.Code)
	}
}

func TestPauseResume(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "POST", "/api/pause", `{"duration":"5m"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: status = %d body=%s", rec.Code, rec.Body)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["paused"] != true || got["paused_until"] == nil {
		t.Errorf("pause body = %v", got)
	}

	if paused, _ := s.engine.Paused(); !paused {
		t.Error("engine should be paused")
	}
	rec = doJSON(t, r, "DELETE", "/api/pause", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: status = %d", rec.Code)
	}
	if paused, _ := s.engine.Paused(); paused {
		t.Error("engine should have resumed")
	}

	rec = doJSON(t, r, "POST", "/api/pause", `{"duration":"banana"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad duration: status = %d, want 400", rec.Code)
	}
}

func TestQueryLogEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	rec := doJSON(t, s.Router(), "GET", "/api/querylog", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("empty log body = %q, want []", body)
	}
	rec = doJSON(t, s.Router(), "GET", "/api/querylog?limit=0", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0: status = %d, want 400", rec.Code)
	}
}
