package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"minos/internal/config"
)

// buildGravityDB writes a minimal Pi-hole gravity.db and returns its bytes.
func buildGravityDB(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gravity.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE adlist (id INTEGER PRIMARY KEY, address TEXT, enabled BOOLEAN, comment TEXT)`,
		`CREATE TABLE domainlist (id INTEGER PRIMARY KEY, type INTEGER, domain TEXT, enabled BOOLEAN)`,
		`INSERT INTO adlist (address, enabled, comment) VALUES ('https://example.com/list.txt', 1, 'family')`,
		`INSERT INTO domainlist (type, domain, enabled) VALUES (0, 'ok.example.com', 1), (1, 'bad.example.net', 1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// multipartBody builds a multipart form with one file field.
func multipartBody(t *testing.T, field, filename string, data []byte) (string, *bytes.Buffer) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return mw.FormDataContentType(), body
}

func TestImportPiholeUpload(t *testing.T) {
	s, store := newTestServer(t, "")
	// Nothing to fetch during the test.
	if err := store.Update(func(c *config.Config) error { c.Lists.Sources = nil; return nil }); err != nil {
		t.Fatal(err)
	}

	ct, body := multipartBody(t, "gravity", "gravity.db", buildGravityDB(t))
	req := httptest.NewRequest(http.MethodPost, "/api/import/pihole", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var report importResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Lists != 1 || report.Allow != 1 || report.Deny != 1 {
		t.Errorf("report = %+v, want 1 list / 1 allow / 1 deny", report)
	}
	// The import must have landed in the live config.
	cfg := store.Get()
	if len(cfg.Lists.Sources) != 1 || cfg.Lists.Sources[0].Name != "family" {
		t.Errorf("sources = %+v, want the imported 'family' list", cfg.Lists.Sources)
	}
	if len(cfg.Blocking.Denylist) != 1 || cfg.Blocking.Denylist[0] != "bad.example.net" {
		t.Errorf("denylist = %v", cfg.Blocking.Denylist)
	}
}

func TestImportPiholeMissingFile(t *testing.T) {
	s, _ := newTestServer(t, "")
	ct, body := multipartBody(t, "wrongfield", "x.db", []byte("junk"))
	req := httptest.NewRequest(http.MethodPost, "/api/import/pihole", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a missing gravity.db", rec.Code)
	}
}

func TestImportAdGuardUpload(t *testing.T) {
	s, store := newTestServer(t, "")
	if err := store.Update(func(c *config.Config) error { c.Lists.Sources = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	yaml := []byte("filters:\n  - enabled: true\n    url: https://example.com/ag.txt\n    name: AG\nuser_rules:\n  - '||ads.example.org^'\n")
	ct, body := multipartBody(t, "config", "AdGuardHome.yaml", yaml)
	req := httptest.NewRequest(http.MethodPost, "/api/import/adguard", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var report importResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &report)
	if report.Lists != 1 || report.Deny != 1 {
		t.Errorf("report = %+v, want 1 list / 1 deny", report)
	}
}

func TestImportConfigRestore(t *testing.T) {
	s, store := newTestServer(t, "")
	// Capture the file-only listen addresses that a restore must preserve.
	before := store.Get()
	dnsListen := before.DNS.Listen

	backup := []byte(`
dns:
  listen: "1.2.3.4:5555"
  upstreams:
    - address: 9.9.9.9:53
      protocol: udp
blocking:
  mode: nxdomain
  denylist: [restored.example.com]
lists:
  sources: []
  refresh_interval: 24h
querylog:
  ephemeral: true
  ring_size: 5000
  retention_days: 30
api:
  listen: "9.9.9.9:9999"
`)
	req := httptest.NewRequest(http.MethodPost, "/api/config/import", bytes.NewReader(backup))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	cfg := store.Get()
	if cfg.Blocking.Mode != "nxdomain" || len(cfg.Blocking.Denylist) != 1 {
		t.Errorf("restore did not apply blocking settings: %+v", cfg.Blocking)
	}
	// Listen addresses are file-only and must NOT change from a backup.
	if cfg.DNS.Listen != dnsListen {
		t.Errorf("dns.listen = %q, want it preserved as %q", cfg.DNS.Listen, dnsListen)
	}
	if cfg.API.Listen == "9.9.9.9:9999" {
		t.Error("api.listen was overwritten by the backup; it is file-only")
	}
}

func TestImportConfigRejectsGarbage(t *testing.T) {
	s, _ := newTestServer(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/config/import",
		bytes.NewReader([]byte("this: is: not: valid: minos: config")))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid config", rec.Code)
	}
}
