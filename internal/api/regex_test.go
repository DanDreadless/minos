package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRegexDomainEntries(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	body := func(v string) string {
		b, _ := json.Marshal(map[string]string{"domain": v})
		return string(b)
	}

	rec := doJSON(t, r, "POST", "/api/denylist", body(`/^ad[0-9]+\./`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add regex: %d %s", rec.Code, rec.Body)
	}
	if got := store.Get().Blocking.Denylist; len(got) != 1 || got[0] != `/^ad[0-9]+\./` {
		t.Fatalf("stored %v, want the pattern as written", got)
	}
	// A pattern RE2 cannot run is refused with the reason, not stored.
	rec = doJSON(t, r, "POST", "/api/denylist", body(`/(a)\1/`), nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not a supported regex") {
		t.Errorf("backreference: %d %s", rec.Code, rec.Body)
	}
	// Deleting by ?domain= works for a pattern that cannot be a path segment.
	rec = doJSON(t, r, "DELETE", "/api/denylist?domain="+url.QueryEscape(`/^ad[0-9]+\./`), "", nil)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("delete regex: %d %s", rec.Code, rec.Body)
	}
	if got := store.Get().Blocking.Denylist; len(got) != 0 {
		t.Errorf("after delete: %v", got)
	}
	// Plain domains still go through either delete form.
	doJSON(t, r, "POST", "/api/allowlist", body("Example.COM"), nil)
	if rec := doJSON(t, r, "DELETE", "/api/allowlist/example.com", "", nil); rec.Code >= 300 {
		t.Errorf("path delete: %d %s", rec.Code, rec.Body)
	}
}
