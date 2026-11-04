package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"minos/internal/config"
)

func TestRulesAddFlipAndDelete(t *testing.T) {
	srv, store := newTestServer(t, "")
	if err := store.Update(func(c *config.Config) error {
		c.Groups = []config.Group{{Name: "kids", Mode: "filter"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := srv.Router()
	post := func(body string) *http.Response {
		t.Helper()
		return doJSON(t, r, "POST", "/api/rules", body, nil).Result()
	}

	// A device rule creates the device's entry, keyed as given.
	if res := post(`{"action":"allow","kind":"domain","value":"Shop.Example.","scope":"device","target":"10.0.0.7"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("add device allow: %d", res.StatusCode)
	}
	c := store.Get()
	if len(c.Clients) != 1 || c.Clients[0].IP != "10.0.0.7" || !slices.Equal(c.Clients[0].Allowlist, []string{"shop.example"}) {
		t.Fatalf("clients = %+v, want 10.0.0.7 allowing shop.example", c.Clients)
	}
	// The same value blocked at the same scope replaces the allow.
	post(`{"action":"block","kind":"domain","value":"shop.example","scope":"device","target":"10.0.0.7"}`)
	cl := store.Get().Clients[0]
	if len(cl.Allowlist) != 0 || !slices.Equal(cl.Denylist, []string{"shop.example"}) {
		t.Errorf("after flip: allow %v deny %v, want only the block", cl.Allowlist, cl.Denylist)
	}
	// Adding it again changes nothing.
	if res := post(`{"action":"block","kind":"domain","value":"shop.example","scope":"device","target":"10.0.0.7"}`); res.StatusCode != http.StatusOK {
		t.Errorf("repeat add: %d, want 200", res.StatusCode)
	}

	// Group and everyone scopes, domains and services.
	post(`{"action":"block","kind":"service","value":"youtube","scope":"group","target":"kids"}`)
	post(`{"action":"allow","kind":"domain","value":"/^ads[0-9]+\\./","scope":"everyone"}`)
	c = store.Get()
	if !slices.Equal(c.Groups[0].Services, []string{"youtube"}) || !slices.Equal(c.Blocking.Allowlist, []string{`/^ads[0-9]+\./`}) {
		t.Errorf("group services %v, global allowlist %v", c.Groups[0].Services, c.Blocking.Allowlist)
	}

	var rules []Rule
	if err := json.Unmarshal(doJSON(t, r, "GET", "/api/rules", "", nil).Body.Bytes(), &rules); err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{Action: "allow", Kind: "domain", Value: `/^ads[0-9]+\./`, Scope: "everyone"},
		{Action: "block", Kind: "service", Value: "youtube", Scope: "group", Target: "kids"},
		{Action: "block", Kind: "domain", Value: "shop.example", Scope: "device", Target: "10.0.0.7"},
	}
	if !slices.Equal(rules, want) {
		t.Errorf("rules = %+v\nwant %+v", rules, want)
	}

	// Delete takes the rule in the body.
	if rec := doJSON(t, r, "DELETE", "/api/rules", `{"action":"block","kind":"domain","value":"shop.example","scope":"device","target":"10.0.0.7"}`, nil); rec.Code != http.StatusOK {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "DELETE", "/api/rules", `{"action":"block","kind":"domain","value":"shop.example","scope":"device","target":"10.0.0.7"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("second delete: %d, want 404", rec.Code)
	}

	// A wildcard is kept in its normal form.
	if res := post(`{"action":"block","kind":"domain","value":"*.Tracker.Example.","scope":"device","target":"10.0.0.7"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("add wildcard: %d", res.StatusCode)
	}
	if cl := store.Get().Clients[0]; !slices.Equal(cl.Denylist, []string{"*.tracker.example"}) {
		t.Errorf("wildcard saved as %v, want *.tracker.example", cl.Denylist)
	}

	// Mistakes are refused plainly.
	for body, code := range map[string]int{
		`{"action":"block","kind":"domain","value":"*.*","scope":"everyone"}`:                     http.StatusBadRequest,
		`{"action":"allow","kind":"domain","value":"x.example","scope":"group","target":"ghost"}`: http.StatusNotFound,
		`{"action":"allow","kind":"service","value":"nope","scope":"everyone"}`:                   http.StatusBadRequest,
		`{"action":"maybe","kind":"domain","value":"x.example","scope":"everyone"}`:               http.StatusBadRequest,
		`{"action":"allow","kind":"domain","value":"x.example","scope":"device"}`:                 http.StatusBadRequest,
		// A MAC Minos has never seen has no address to record.
		`{"action":"allow","kind":"domain","value":"x.example","scope":"device","target":"aa:bb:cc:dd:ee:ff"}`: http.StatusNotFound,
	} {
		if rec := doJSON(t, r, "POST", "/api/rules", body, nil); rec.Code != code {
			t.Errorf("%s: %d %s, want %d", body, rec.Code, rec.Body, code)
		}
	}
	// With an address hint, a MAC-keyed entry is created.
	if rec := doJSON(t, r, "POST", "/api/rules", `{"action":"allow","kind":"domain","value":"x.example","scope":"device","target":"AA-BB-CC-DD-EE-FF","ip":"10.0.0.8"}`, nil); rec.Code != http.StatusCreated {
		t.Fatalf("mac rule: %d %s", rec.Code, rec.Body)
	}
	if cl := store.Get().Clients; cl[len(cl)-1].MAC != "aa:bb:cc:dd:ee:ff" || cl[len(cl)-1].IP != "10.0.0.8" {
		t.Errorf("mac entry = %+v", cl[len(cl)-1])
	}
}
