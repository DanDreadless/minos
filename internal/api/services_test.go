package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// PUT /api/services is a partial update: each of blocked/allowed replaces
// its set only when present, so an external caller written before "allowed"
// existed can't silently clear service pardons.
func TestServicesPartialUpdate(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "PUT", "/api/services", `{"blocked":["tiktok"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set blocked: status = %d: %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "PUT", "/api/services", `{"allowed":["netflix"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set allowed: status = %d: %s", rec.Code, rec.Body)
	}
	var view struct {
		Blocked []string `json:"blocked"`
		Allowed []string `json:"allowed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Blocked) != 1 || view.Blocked[0] != "tiktok" {
		t.Errorf("blocked = %v, want [tiktok] untouched by the allowed-only PUT", view.Blocked)
	}
	if len(view.Allowed) != 1 || view.Allowed[0] != "netflix" {
		t.Errorf("allowed = %v, want [netflix]", view.Allowed)
	}
	b := store.Get().Blocking
	if len(b.Services) != 1 || len(b.AllowedServices) != 1 {
		t.Errorf("persisted services = %v / %v", b.Services, b.AllowedServices)
	}

	// Both at once, and clearing with an explicit empty list.
	rec = doJSON(t, r, "PUT", "/api/services", `{"blocked":[],"allowed":[]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear both: status = %d: %s", rec.Code, rec.Body)
	}
	b = store.Get().Blocking
	if len(b.Services) != 0 || len(b.AllowedServices) != 0 {
		t.Errorf("after clearing: %v / %v, want empty", b.Services, b.AllowedServices)
	}

	// Neither field is a client error.
	if rec := doJSON(t, r, "PUT", "/api/services", `{}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: status = %d, want 400", rec.Code)
	}
	// Unknown names are rejected by validation and nothing applies.
	if rec := doJSON(t, r, "PUT", "/api/services", `{"allowed":["myspace"]}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown allowed service: status = %d, want 400", rec.Code)
	}

	// GET reflects both sets.
	rec = doJSON(t, r, "GET", "/api/services", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d", rec.Code)
	}
	var got struct {
		Catalog []struct {
			Name string `json:"name"`
		} `json:"catalog"`
		Blocked []string `json:"blocked"`
		Allowed []string `json:"allowed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Catalog) == 0 || got.Blocked == nil || got.Allowed == nil {
		t.Errorf("view = %+v, want catalog plus non-null blocked/allowed", got)
	}
}

// Group allowed_services round-trips through the group endpoints.
func TestGroupAllowedServicesRoundTrip(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	rec := doJSON(t, r, "POST", "/api/groups", `{"name":"kids","allowed_services":["netflix"]}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add group: status = %d: %s", rec.Code, rec.Body)
	}
	if g := store.Get().Groups[0]; len(g.AllowedServices) != 1 || g.AllowedServices[0] != "netflix" {
		t.Fatalf("group = %+v", g)
	}
	rec = doJSON(t, r, "PUT", "/api/groups/kids", `{"allowed_services":["netflix","disneyplus"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("update group: status = %d: %s", rec.Code, rec.Body)
	}
	if g := store.Get().Groups[0]; len(g.AllowedServices) != 2 {
		t.Errorf("group after update = %+v", g)
	}
}
