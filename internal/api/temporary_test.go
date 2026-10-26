package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/config"
)

func TestTemporaryAllowAPI(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := do(t, r, "POST", "/api/allowlist", `{"domain":"Ads.Example.com.","minutes":60}`, nil, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var added struct {
		Domain string    `json:"domain"`
		Until  time.Time `json:"until"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.Domain != "ads.example.com" || time.Until(added.Until) < 59*time.Minute || time.Until(added.Until) > 61*time.Minute {
		t.Fatalf("added %+v", added)
	}
	// Allowing again for longer replaces the entry rather than adding one.
	do(t, r, "POST", "/api/allowlist", `{"domain":"ads.example.com","minutes":120}`, nil, nil)
	if got := store.Get().Blocking.TemporaryAllows; len(got) != 1 || time.Until(got[0].Until) < 119*time.Minute {
		t.Fatalf("after re-adding: %+v", got)
	}

	var list []config.TemporaryAllow
	_ = json.Unmarshal(do(t, r, "GET", "/api/allowlist/temporary", "", nil, nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0].Domain != "ads.example.com" {
		t.Fatalf("list %+v", list)
	}

	for body, want := range map[string]int{
		`{"domain":"x.example.com","minutes":-5}`:    http.StatusBadRequest,
		`{"domain":"x.example.com","minutes":20000}`: http.StatusBadRequest, // over a week
		`{"domain":"/ads[0-9]/","minutes":60}`:       http.StatusBadRequest, // regex rules are permanent only
	} {
		if rec := do(t, r, "POST", "/api/allowlist", body, nil, nil); rec.Code != want {
			t.Errorf("%s: %d, want %d", body, rec.Code, want)
		}
	}
	if rec := do(t, r, "POST", "/api/denylist", `{"domain":"x.example.com","minutes":60}`, nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("temporary block accepted: %d", rec.Code)
	}

	// A permanent allow supersedes the temporary one.
	do(t, r, "POST", "/api/allowlist", `{"domain":"ads.example.com"}`, nil, nil)
	if got := store.Get().Blocking.TemporaryAllows; len(got) != 0 {
		t.Fatalf("temporary allow kept beside a permanent one: %+v", got)
	}
	// And a temporary allow of a permanently allowed domain changes nothing.
	rec = do(t, r, "POST", "/api/allowlist", `{"domain":"ads.example.com","minutes":60}`, nil, nil)
	if rec.Code != http.StatusOK || len(store.Get().Blocking.TemporaryAllows) != 0 {
		t.Fatalf("temporary over permanent: %d %+v", rec.Code, store.Get().Blocking.TemporaryAllows)
	}

	do(t, r, "POST", "/api/allowlist", `{"domain":"once.example.com","minutes":5}`, nil, nil)
	if rec := do(t, r, "DELETE", "/api/allowlist/temporary?domain=once.example.com", "", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(t, r, "DELETE", "/api/allowlist/temporary?domain=once.example.com", "", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("second delete: %d", rec.Code)
	}
}
