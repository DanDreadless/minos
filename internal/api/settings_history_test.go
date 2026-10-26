package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"minos/internal/config"
)

func TestSettingsHistoryAPI(t *testing.T) {
	s, store := newTestServer(t, "tok")
	r := s.Router()
	hdr := map[string]string{"X-Api-Token": "tok"}

	if rec := do(t, r, "POST", "/api/allowlist", `{"domain":"first.example"}`, nil, hdr); rec.Code != http.StatusCreated {
		t.Fatalf("add: %d", rec.Code)
	}
	do(t, r, "POST", "/api/allowlist", `{"domain":"second.example"}`, nil, hdr)

	var h []config.HistoryEntry
	_ = json.Unmarshal(do(t, r, "GET", "/api/settings/history", "", nil, hdr).Body.Bytes(), &h)
	// Newest first; the token set by the test setup was housekeeping (not recorded).
	if len(h) != 3 || h[0].Actor != "API token from 192.168.1.20" || !slices.Equal(h[0].Keys, []string{"blocking.allowlist"}) {
		t.Fatalf("history %+v", h)
	}

	var diff []config.DiffLine
	_ = json.Unmarshal(do(t, r, "GET", "/api/settings/history/"+h[0].ID+"/diff", "", nil, hdr).Body.Bytes(), &diff)
	var text strings.Builder
	for _, l := range diff {
		text.WriteString(l.Op + l.Text + "\n")
	}
	if !strings.Contains(text.String(), "+        - second.example") || strings.Contains(text.String(), "tok\n") {
		t.Fatalf("diff:\n%s", text.String())
	}

	// Undo the last change: restore the version before it.
	if rec := do(t, r, "POST", "/api/settings/history/"+h[1].ID+"/restore", "", nil, hdr); rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if got := store.Get().Blocking.Allowlist; !slices.Equal(got, []string{"first.example"}) {
		t.Fatalf("after undo: %v", got)
	}
	if store.Get().API.Token != "tok" {
		t.Error("restore changed the API token")
	}
	if rec := do(t, r, "POST", "/api/settings/history/999/restore", "", nil, hdr); rec.Code != http.StatusNotFound {
		t.Errorf("unknown version: %d", rec.Code)
	}
	if rec := do(t, r, "GET", "/api/settings/history", "", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("history without credentials: %d", rec.Code)
	}

	// The minos command labels itself.
	cli := map[string]string{"X-Api-Token": "tok", "X-Minos-Client": "cli"}
	do(t, r, "POST", "/api/denylist", `{"domain":"cli.example"}`, nil, cli)
	_ = json.Unmarshal(do(t, r, "GET", "/api/settings/history", "", nil, hdr).Body.Bytes(), &h)
	if h[0].Actor != "minos command from 192.168.1.20" {
		t.Errorf("cli actor %q", h[0].Actor)
	}
}
