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

// Custom-service CRUD: create (with slugify + de-collision), partial
// update, group references, and delete-with-scrub.
func TestCustomServiceCRUD(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Create with an explicit name.
	rec := doJSON(t, r, "POST", "/api/services/custom",
		`{"name":"my-game","label":"My Game","domains":["mygame.example"],"blocked":true}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d: %s", rec.Code, rec.Body)
	}
	// Create by label alone: slugified, and de-collided against customs.
	rec = doJSON(t, r, "POST", "/api/services/custom",
		`{"label":"My Game","domains":["othergame.example"]}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("slugified create: status = %d: %s", rec.Code, rec.Body)
	}
	var view struct {
		Custom []struct {
			Name    string   `json:"name"`
			Domains []string `json:"domains"`
			Blocked bool     `json:"blocked"`
			Allowed bool     `json:"allowed"`
		} `json:"custom"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Custom) != 2 || view.Custom[0].Name != "my-game" || view.Custom[1].Name != "my-game-2" {
		t.Fatalf("customs = %+v, want my-game + de-collided my-game-2", view.Custom)
	}
	if !view.Custom[0].Blocked || view.Custom[1].Blocked {
		t.Errorf("blocked flags = %+v, want first true, second false", view.Custom)
	}

	// Catalog collisions and duplicate explicit names are rejected.
	rec = doJSON(t, r, "POST", "/api/services/custom", `{"name":"netflix","domains":["x.example"]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("catalog collision: status = %d, want 400", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/services/custom", `{"name":"my-game","domains":["x.example"]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("duplicate name: status = %d, want 400", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/services/custom", `{"name":"bad","domains":[]}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no domains: status = %d, want 400", rec.Code)
	}

	// Partial update: toggling allowed leaves everything else alone.
	rec = doJSON(t, r, "PUT", "/api/services/custom/my-game", `{"allowed":true}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body)
	}
	cs := store.Get().Blocking.CustomServices[0]
	if !cs.Blocked || !cs.Allowed || len(cs.Domains) != 1 {
		t.Errorf("after partial update: %+v, want blocked+allowed with domains intact", cs)
	}
	if rec := doJSON(t, r, "PUT", "/api/services/custom/ghost", `{"blocked":true}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("update unknown: status = %d, want 404", rec.Code)
	}

	// Group references ride their own fields; delete scrubs them.
	rec = doJSON(t, r, "POST", "/api/groups",
		`{"name":"kids","mode":"filter","custom_services":["my-game"],"allowed_custom_services":["my-game-2"]}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("group with customs: status = %d: %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/groups",
		`{"name":"bad","mode":"filter","custom_services":["ghost"]}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("dangling group custom: status = %d, want 400", rec.Code)
	}
	rec = doJSON(t, r, "DELETE", "/api/services/custom/my-game", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d: %s", rec.Code, rec.Body)
	}
	cfg := store.Get()
	if len(cfg.Blocking.CustomServices) != 1 || cfg.Blocking.CustomServices[0].Name != "my-game-2" {
		t.Errorf("after delete: %+v, want only my-game-2", cfg.Blocking.CustomServices)
	}
	if len(cfg.Groups[0].CustomServices) != 0 {
		t.Errorf("group still references deleted custom: %v", cfg.Groups[0].CustomServices)
	}
	if len(cfg.Groups[0].AllowedCustomServices) != 1 {
		t.Errorf("unrelated group reference scrubbed: %v", cfg.Groups[0].AllowedCustomServices)
	}
	if rec := doJSON(t, r, "DELETE", "/api/services/custom/ghost", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete unknown: status = %d, want 404", rec.Code)
	}
}
