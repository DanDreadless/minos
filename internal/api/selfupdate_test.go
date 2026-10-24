package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixedLatest struct {
	v     string
	newer bool
}

func (f fixedLatest) Latest() (string, bool) { return f.v, f.newer }

// selfUpdateServer is a test server that passes the platform check, with a
// helper present and a newer release known.
func selfUpdateServer(t *testing.T, token string) (*Server, string) {
	t.Helper()
	old := updatePlatform
	updatePlatform = func(string) string { return "" }
	t.Cleanup(func() { updatePlatform = old })
	s, store := newTestServer(t, token)
	helper := filepath.Join(t.TempDir(), "minos-update")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.updateHelper = helper
	s.updates = fixedLatest{"0.31.0", true}
	return s, filepath.Dir(store.Path())
}

func TestSelfUpdateNeedsALogin(t *testing.T) {
	s, dir := selfUpdateServer(t, "")
	r := s.Router()
	var info updateResponse
	rec := do(t, r, "GET", "/api/update", "", nil, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if info.SelfUpdate || !strings.Contains(info.SelfUpdateBlocked, "password") {
		t.Fatalf("an open interface offered the update: %+v", info)
	}
	if rec := do(t, r, "POST", "/api/update/install", "", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("install on an open interface: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, updateRequestFile)); err == nil {
		t.Fatal("a request file was written for an open interface")
	}
}

func TestSelfUpdateRequest(t *testing.T) {
	s, dir := selfUpdateServer(t, "tok")
	r := s.Router()
	hdr := map[string]string{"X-Api-Token": "tok"}
	var info updateResponse
	_ = json.Unmarshal(do(t, r, "GET", "/api/update", "", nil, hdr).Body.Bytes(), &info)
	if !info.SelfUpdate || info.SelfUpdateBlocked != "" {
		t.Fatalf("update not offered: %+v", info)
	}
	if rec := do(t, r, "POST", "/api/update/install", "", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("install without credentials: %d", rec.Code)
	}
	if rec := do(t, r, "POST", "/api/update/install", "", nil, hdr); rec.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	b, err := os.ReadFile(filepath.Join(dir, updateRequestFile))
	if err != nil || string(b) != "v0.31.0\n" {
		t.Fatalf("request file = %q, %v", b, err)
	}
	var p UpdateProgress
	_ = json.Unmarshal(do(t, r, "GET", "/api/update/progress", "", nil, hdr).Body.Bytes(), &p)
	if p.State != "requested" || p.Version != "v0.31.0" {
		t.Fatalf("progress = %+v", p)
	}
	// One at a time.
	if rec := do(t, r, "POST", "/api/update/install", "", nil, hdr); rec.Code != http.StatusConflict {
		t.Fatalf("second install while one runs: %d", rec.Code)
	}
	// Without the helper there is no button.
	s.updateHelper = filepath.Join(dir, "absent")
	_ = json.Unmarshal(do(t, r, "GET", "/api/update", "", nil, hdr).Body.Bytes(), &info)
	if info.SelfUpdate || !strings.Contains(info.SelfUpdateBlocked, "not installed") {
		t.Fatalf("offered without the helper: %+v", info)
	}
}

func TestSelfUpdateStaleProgressFails(t *testing.T) {
	s, dir := selfUpdateServer(t, "tok")
	old, _ := json.Marshal(UpdateProgress{State: "downloading", Version: "v0.31.0", Time: time.Now().Add(-time.Hour).Unix()})
	if err := os.WriteFile(filepath.Join(dir, updateStatusFile), old, 0o644); err != nil {
		t.Fatal(err)
	}
	if p := s.readUpdateProgress(); p.State != "failed" {
		t.Fatalf("a helper that died mid-run still reads as %q", p.State)
	}
}

func TestWatchSelfUpdateAnnouncesOnce(t *testing.T) {
	s, dir := selfUpdateServer(t, "tok")
	done, _ := json.Marshal(UpdateProgress{State: "done", Version: "v0.31.0", From: "0.30.0", Time: time.Now().Unix()})
	if err := os.WriteFile(filepath.Join(dir, updateStatusFile), done, 0o644); err != nil {
		t.Fatal(err)
	}
	got := make(chan UpdateProgress, 4)
	for range 2 { // A restart must not announce the same update again.
		ctx, cancel := context.WithCancel(context.Background())
		go s.WatchSelfUpdate(ctx, func(p UpdateProgress) { got <- p })
		time.Sleep(200 * time.Millisecond)
		cancel()
	}
	if n := len(got); n != 1 {
		t.Fatalf("announced %d times, want 1", n)
	}
	if p := <-got; p.State != "done" || p.Version != "v0.31.0" {
		t.Fatalf("announced %+v", p)
	}
}
