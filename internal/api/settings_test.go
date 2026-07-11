package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/querylog"
)

func TestGetConfigRedactsToken(t *testing.T) {
	s, _ := newTestServer(t, "sekrit")
	rec := doJSON(t, s.Router(), "GET", "/api/config", "", map[string]string{"X-Api-Token": "sekrit"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, "sekrit") {
		t.Error("config response leaked the API token")
	}
	var got struct {
		API struct {
			TokenSet bool `json:"token_set"`
		} `json:"api"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.API.TokenSet {
		t.Error("token_set = false, want true")
	}
}

func TestUpdateConfig(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "PUT", "/api/config",
		`{"blocking":{"mode":"nxdomain"},"dns":{"block_ttl":300},"querylog":{"retention_days":30}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	cfg := store.Get()
	if cfg.Blocking.Mode != "nxdomain" {
		t.Errorf("mode = %q, want nxdomain", cfg.Blocking.Mode)
	}
	if cfg.DNS.BlockTTL != 300 {
		t.Errorf("block_ttl = %d, want 300", cfg.DNS.BlockTTL)
	}
	if cfg.QueryLog.RetentionDays != 30 {
		t.Errorf("retention_days = %d, want 30", cfg.QueryLog.RetentionDays)
	}
	// Untouched fields keep their values.
	if len(cfg.DNS.Upstreams) == 0 {
		t.Error("upstreams were clobbered by a partial update")
	}
}

func TestUpdateConfigRejectsInvalid(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	before := store.Get().Blocking.Mode

	cases := []struct{ name, body string }{
		{"bad mode", `{"blocking":{"mode":"redirect"}}`},
		{"unknown field", `{"blocking":{"fate":"condemned"}}`},
		{"empty upstreams", `{"dns":{"upstreams":[]}}`},
		{"bad refresh interval", `{"lists":{"refresh_interval":"1m"}}`},
		{"bad ring size", `{"querylog":{"ring_size":0}}`},
	}
	for _, tc := range cases {
		rec := doJSON(t, r, "PUT", "/api/config", tc.body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", tc.name, rec.Code, rec.Body)
		}
	}
	if store.Get().Blocking.Mode != before {
		t.Error("rejected update still changed the config")
	}
}

func TestUpdateConfigSetsAndClearsToken(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	if rec := doJSON(t, r, "PUT", "/api/config", `{"api":{"token":"newtoken"}}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("set token: status = %d: %s", rec.Code, rec.Body)
	}
	if store.Get().API.Token != "newtoken" {
		t.Fatalf("token = %q, want newtoken", store.Get().API.Token)
	}
	// Further requests now need the token.
	if rec := doJSON(t, r, "GET", "/api/config", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated after set: status = %d, want 401", rec.Code)
	}
	hdr := map[string]string{"X-Api-Token": "newtoken"}
	if rec := doJSON(t, r, "PUT", "/api/config", `{"api":{"token":""}}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("clear token: status = %d: %s", rec.Code, rec.Body)
	}
	if store.Get().API.Token != "" {
		t.Error("token was not cleared")
	}
}

func TestExportConfig(t *testing.T) {
	s, _ := newTestServer(t, "")
	rec := doJSON(t, s.Router(), "GET", "/api/config/export", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "minos.yaml") {
		t.Errorf("Content-Disposition = %q, want attachment minos.yaml", cd)
	}
	if !strings.Contains(rec.Body.String(), "upstreams:") {
		t.Errorf("export does not look like the config YAML: %s", rec.Body)
	}
}

func TestListSourceCRUD(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Add (URL is unreachable; the source is still configured, fetch just fails).
	rec := doJSON(t, r, "POST", "/api/lists",
		`{"name":"extra","url":"http://127.0.0.1:1/list.txt","format":"plain","enabled":false}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: status = %d, want 201: %s", rec.Code, rec.Body)
	}
	found := false
	for _, src := range store.Get().Lists.Sources {
		if src.Name == "extra" {
			found = true
			if src.Format != "plain" || src.Enabled {
				t.Errorf("added source = %+v, want plain/disabled", src)
			}
		}
	}
	if !found {
		t.Fatal("added source missing from config")
	}

	// Duplicate name rejected.
	if rec := doJSON(t, r, "POST", "/api/lists",
		`{"name":"extra","url":"http://127.0.0.1:1/other.txt"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("duplicate add: status = %d, want 400", rec.Code)
	}

	// Update.
	if rec := doJSON(t, r, "PUT", "/api/lists/extra", `{"enabled":false,"format":"hosts"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body)
	}
	for _, src := range store.Get().Lists.Sources {
		if src.Name == "extra" && src.Format != "hosts" {
			t.Errorf("format = %q, want hosts", src.Format)
		}
	}

	// Update unknown → 404.
	if rec := doJSON(t, r, "PUT", "/api/lists/nope", `{"enabled":true}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("update missing: status = %d, want 404", rec.Code)
	}

	// Invalid edit rejected by validation.
	if rec := doJSON(t, r, "PUT", "/api/lists/extra", `{"format":"csv"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad format: status = %d, want 400", rec.Code)
	}

	// Delete.
	if rec := doJSON(t, r, "DELETE", "/api/lists/extra", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d: %s", rec.Code, rec.Body)
	}
	for _, src := range store.Get().Lists.Sources {
		if src.Name == "extra" {
			t.Error("deleted source still in config")
		}
	}
	if rec := doJSON(t, r, "DELETE", "/api/lists/extra", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: status = %d, want 404", rec.Code)
	}
}

// action:"allow" routes a source into Lists.AllowSources, an action change
// moves it between the slices, and delete finds it in either.
func TestListSourceAudit(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "POST", "/api/lists",
		`{"name":"trial","url":"http://127.0.0.1:1/strict.txt","format":"plain","audit":true,"enabled":false}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add audit list: status = %d: %s", rec.Code, rec.Body)
	}
	auditOf := func() (bool, bool) { // (found, audit)
		for _, src := range store.Get().Lists.Sources {
			if src.Name == "trial" {
				return true, src.Audit
			}
		}
		return false, false
	}
	if found, audit := auditOf(); !found || !audit {
		t.Fatalf("sources = %+v, want trial with audit:true", store.Get().Lists.Sources)
	}
	if !strings.Contains(rec.Body.String(), `"audit":true`) {
		t.Errorf("status should report audit: %s", rec.Body)
	}

	// Enforce with one click: the toggle flips audit off in place.
	if rec := doJSON(t, r, "PUT", "/api/lists/trial", `{"audit":false}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("audit off: status = %d: %s", rec.Code, rec.Body)
	}
	if found, audit := auditOf(); !found || audit {
		t.Errorf("sources = %+v, want trial audit:false after toggle", store.Get().Lists.Sources)
	}

	// Auditing an allowlist is rejected by validation.
	if rec := doJSON(t, r, "POST", "/api/lists",
		`{"name":"nope","url":"http://127.0.0.1:1/a.txt","format":"plain","action":"allow","audit":true}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("audit allowlist: status = %d, want 400", rec.Code)
	}

	// The history endpoint validates would_block.
	if rec := doJSON(t, r, "GET", "/api/querylog/history?would_block=maybe", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("would_block=maybe: status = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/querylog/history?would_block=true", "", nil); rec.Code != http.StatusOK {
		t.Errorf("would_block=true: status = %d, want 200", rec.Code)
	}
}

func TestListSourceAllowAction(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "POST", "/api/lists",
		`{"name":"unbreak","url":"http://127.0.0.1:1/allow.txt","format":"plain","action":"allow","enabled":false}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add allow: status = %d: %s", rec.Code, rec.Body)
	}
	if got := store.Get().Lists.AllowSources; len(got) != 1 || got[0].Name != "unbreak" {
		t.Fatalf("allow_sources = %+v, want the new list", got)
	}
	if !strings.Contains(rec.Body.String(), `"action":"allow"`) {
		t.Errorf("status should report the action: %s", rec.Body)
	}

	// A duplicate name is rejected across the two slices.
	if rec := doJSON(t, r, "POST", "/api/lists",
		`{"name":"unbreak","url":"http://127.0.0.1:1/x.txt"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("duplicate across slices: status = %d, want 400", rec.Code)
	}

	// Flipping the action moves the source to the block slice.
	if rec := doJSON(t, r, "PUT", "/api/lists/unbreak", `{"action":"block"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("action flip: status = %d: %s", rec.Code, rec.Body)
	}
	cfg := store.Get()
	if len(cfg.Lists.AllowSources) != 0 {
		t.Errorf("allow_sources = %+v, want empty after the flip", cfg.Lists.AllowSources)
	}
	moved := false
	for _, src := range cfg.Lists.Sources {
		if src.Name == "unbreak" {
			moved = true
		}
	}
	if !moved {
		t.Error("flipped source missing from sources")
	}

	// An unknown action is rejected.
	if rec := doJSON(t, r, "PUT", "/api/lists/unbreak", `{"action":"maybe"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad action: status = %d, want 400", rec.Code)
	}

	// Flip back and delete out of the allow slice.
	if rec := doJSON(t, r, "PUT", "/api/lists/unbreak", `{"action":"allow"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("action flip back: status = %d: %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "DELETE", "/api/lists/unbreak", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d: %s", rec.Code, rec.Body)
	}
	cfg = store.Get()
	if len(cfg.Lists.AllowSources) != 0 {
		t.Errorf("allow_sources = %+v, want empty after delete", cfg.Lists.AllowSources)
	}
}

func TestCheckDomain(t *testing.T) {
	s, store := newTestServer(t, "")
	if err := store.Update(func(c *config.Config) error {
		c.Blocking.Denylist = append(c.Blocking.Denylist, "doubleclick.net")
		c.Blocking.Allowlist = append(c.Blocking.Allowlist, "good.doubleclick.net")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The manager rebuild is async via OnChange; build the matcher directly.
	s.lists.Refresh(t.Context())
	r := s.Router()

	cases := []struct {
		domain, verdict, rule string
	}{
		{"ads.doubleclick.net", "blocked", "doubleclick.net"},
		{"good.doubleclick.net", "allowed", "good.doubleclick.net"},
		{"example.com", "allowed", ""},
	}
	for _, tc := range cases {
		rec := doJSON(t, r, "GET", "/api/check?domain="+tc.domain, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", tc.domain, rec.Code, rec.Body)
		}
		var got map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["verdict"] != tc.verdict || got["rule"] != tc.rule {
			t.Errorf("%s: got verdict=%q rule=%q, want %q/%q",
				tc.domain, got["verdict"], got["rule"], tc.verdict, tc.rule)
		}
	}

	if rec := doJSON(t, r, "GET", "/api/check?domain=not..valid", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid domain: status = %d, want 400", rec.Code)
	}
}

func TestStatsEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "GET", "/api/stats", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		WindowHours int              `json:"window_hours"`
		Timeline    []map[string]any `json:"timeline"`
		TopBlocked  []map[string]any `json:"top_blocked"`
		TopClients  []map[string]any `json:"top_clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WindowHours != 24 {
		t.Errorf("window_hours = %d, want 24", got.WindowHours)
	}
	if len(got.Timeline) == 0 {
		t.Error("timeline should contain filled empty buckets")
	}
	if got.TopBlocked == nil || got.TopClients == nil {
		t.Error("top lists must be [] not null")
	}

	if rec := doJSON(t, r, "GET", "/api/stats?hours=999", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("hours=999: status = %d, want 400", rec.Code)
	}
}

func TestClientStatsEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()

	// Seed the (ephemeral) log through the public Record path.
	now := time.Now()
	s.qlog.Record(querylog.Entry{Time: now, Client: "10.0.0.9", QName: "ads.example.com", QType: "A", Verdict: "blocked"})
	s.qlog.Record(querylog.Entry{Time: now, Client: "10.0.0.9", QName: "github.com", QType: "A", Verdict: "allowed"})
	s.qlog.Record(querylog.Entry{Time: now, Client: "10.0.0.10", QName: "github.com", QType: "A", Verdict: "allowed"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(s.qlog.Recent(0)) < 3 {
		time.Sleep(5 * time.Millisecond)
	}

	// A device spanning both IPs, comma-separated like the history filter.
	rec := doJSON(t, r, "GET", "/api/stats/client?client=10.0.0.9,10.0.0.10", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		WindowHours int              `json:"window_hours"`
		Total       int              `json:"total"`
		Blocked     int              `json:"blocked"`
		TopAllowed  []map[string]any `json:"top_allowed"`
		TopBlocked  []map[string]any `json:"top_blocked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WindowHours != 24 || got.Total != 3 || got.Blocked != 1 {
		t.Errorf("overview = %+v, want 24h window, total 3, blocked 1", got)
	}
	if len(got.TopAllowed) != 1 || len(got.TopBlocked) != 1 {
		t.Errorf("top lists = %+v / %+v, want one entry each", got.TopAllowed, got.TopBlocked)
	}

	// Parameter validation mirrors the stats endpoint.
	if rec := doJSON(t, r, "GET", "/api/stats/client", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing client: status = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/stats/client?client=10.0.0.9&hours=999", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("hours=999: status = %d, want 400", rec.Code)
	}
}
