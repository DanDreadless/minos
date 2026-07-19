package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/clients"
	"minos/internal/config"
)

func TestGroupCRUD(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Add.
	rec := doJSON(t, r, "POST", "/api/groups",
		`{"name":"kids","mode":"filter","denylist":["tiktok.com"]}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: status = %d: %s", rec.Code, rec.Body)
	}
	if len(store.Get().Groups) != 1 || store.Get().Groups[0].Name != "kids" {
		t.Fatalf("groups = %+v", store.Get().Groups)
	}

	// Reserved and duplicate names, bad mode.
	for _, body := range []string{
		`{"name":"default","mode":"filter"}`,
		`{"name":"kids","mode":"filter"}`,
		`{"name":"x","mode":"invisible"}`,
	} {
		if rec := doJSON(t, r, "POST", "/api/groups", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("add %s: status = %d, want 400", body, rec.Code)
		}
	}

	// Update mode.
	if rec := doJSON(t, r, "PUT", "/api/groups/kids", `{"mode":"bypass"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body)
	}
	if store.Get().Groups[0].Mode != "bypass" {
		t.Errorf("mode = %q, want bypass", store.Get().Groups[0].Mode)
	}
	if rec := doJSON(t, r, "PUT", "/api/groups/nope", `{"mode":"filter"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("update missing: status = %d, want 404", rec.Code)
	}

	// Assign a client, then delete the group: assignment falls back to default.
	if rec := doJSON(t, r, "PUT", "/api/clients/10.0.0.9", `{"group":"kids"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("assign: status = %d: %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "DELETE", "/api/groups/kids", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d: %s", rec.Code, rec.Body)
	}
	cfg := store.Get()
	if len(cfg.Groups) != 0 {
		t.Errorf("groups after delete = %+v", cfg.Groups)
	}
	if cfg.Clients[0].Group != "" {
		t.Errorf("client group after group delete = %q, want cleared", cfg.Clients[0].Group)
	}
}

func TestClientAssignmentAndBlock(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Assigning to a nonexistent group fails validation and changes nothing.
	if rec := doJSON(t, r, "PUT", "/api/clients/10.0.0.5", `{"group":"ghost"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad group: status = %d, want 400", rec.Code)
	}
	if len(store.Get().Clients) != 0 {
		t.Error("failed update still persisted a client")
	}

	// Label + block a device.
	rec := doJSON(t, r, "PUT", "/api/clients/10.0.0.5", `{"name":"smart tv","blocked":true}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body)
	}
	var devices []clients.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "smart tv" || !devices[0].Blocked || devices[0].Seen {
		t.Fatalf("devices = %+v", devices)
	}

	// The policy table sees the block immediately (OnChange wiring).
	if p := s.clients.PolicyFor("10.0.0.5"); !p.Refuses() {
		t.Errorf("policy = %+v, want refuse", p)
	}

	// Unblock via the same endpoint.
	if rec := doJSON(t, r, "PUT", "/api/clients/10.0.0.5", `{"blocked":false}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("unblock: status = %d", rec.Code)
	}
	if p := s.clients.PolicyFor("10.0.0.5"); p.Refuses() {
		t.Errorf("policy after unblock = %+v", p)
	}

	// Invalid IP in the path is rejected by validation.
	if rec := doJSON(t, r, "PUT", "/api/clients/not-an-ip", `{"blocked":true}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad ip: status = %d, want 400", rec.Code)
	}

	// Delete the assignment.
	if rec := doJSON(t, r, "DELETE", "/api/clients/10.0.0.5", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d", rec.Code)
	}
	if rec := doJSON(t, r, "DELETE", "/api/clients/10.0.0.5", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: status = %d, want 404", rec.Code)
	}
}

func TestMACKeyedAssignment(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Assign a block keyed by MAC (the frontend sends d.mac as the key). The
	// device isn't in the ARP table right now, so the body carries the
	// last-known IP hint that the stored client needs to stay valid.
	rec := doJSON(t, r, "PUT", "/api/clients/AA:BB:CC:11:22:33",
		`{"blocked":true,"ip":"192.168.1.40"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign: status = %d: %s", rec.Code, rec.Body)
	}
	// Stored MAC-keyed, canonicalised, with the hint as its last-known IP.
	cfg := store.Get()
	if len(cfg.Clients) != 1 || cfg.Clients[0].MAC != "aa:bb:cc:11:22:33" || cfg.Clients[0].IP != "192.168.1.40" {
		t.Fatalf("stored client = %+v", cfg.Clients)
	}
	// The block applies to the last-known lease immediately.
	if p := s.clients.PolicyFor("192.168.1.40"); !p.Refuses() {
		t.Errorf("policy = %+v, want refuse", p)
	}
	// One merged device row, addressed by MAC.
	var devices []clients.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || !devices[0].Blocked || devices[0].MAC != "aa:bb:cc:11:22:33" {
		t.Fatalf("devices = %+v", devices)
	}
	// A second update addressed by the same MAC (any notation) updates in place.
	if rec := doJSON(t, r, "PUT", "/api/clients/aa-bb-cc-11-22-33", `{"name":"printer"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("relabel: status = %d: %s", rec.Code, rec.Body)
	}
	if len(store.Get().Clients) != 1 || store.Get().Clients[0].Name != "printer" {
		t.Errorf("clients after relabel = %+v", store.Get().Clients)
	}
	// Forget by MAC removes it.
	if rec := doJSON(t, r, "DELETE", "/api/clients/aa:bb:cc:11:22:33", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("forget: status = %d", rec.Code)
	}
	if len(store.Get().Clients) != 0 {
		t.Errorf("clients after forget = %+v", store.Get().Clients)
	}
}

func TestMACKeyEncodedPath(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// The browser sends a MAC key percent-encoded (encodeURIComponent turns
	// each ':' into %3A). The handler must decode it, not treat it as an IP.
	rec := doJSON(t, r, "PUT", "/api/clients/28%3Acd%3Ac1%3A00%3A11%3A22",
		`{"name":"pi","ip":"192.168.1.42"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("encoded MAC PUT: status = %d: %s", rec.Code, rec.Body)
	}
	cfg := store.Get()
	if len(cfg.Clients) != 1 || cfg.Clients[0].MAC != "28:cd:c1:00:11:22" || cfg.Clients[0].IP != "192.168.1.42" {
		t.Fatalf("stored client = %+v, want one MAC-keyed entry", cfg.Clients)
	}
	// Forget through the encoded path too.
	if rec := doJSON(t, r, "DELETE", "/api/clients/28%3Acd%3Ac1%3A00%3A11%3A22", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("encoded MAC DELETE: status = %d: %s", rec.Code, rec.Body)
	}
	if len(store.Get().Clients) != 0 {
		t.Errorf("clients after forget = %+v", store.Get().Clients)
	}
}

func TestMACUpdateAbsorbsLegacyIPClient(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	// A pre-v0.10 assignment: IP-keyed, no MAC.
	if err := store.Update(func(c *config.Config) error {
		c.Clients = []config.Client{{IP: "192.168.68.117", Name: "old-label"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The device now has a known MAC, so the UI addresses it by MAC and sends
	// the current IP as a hint. Clearing the label must reconcile onto the
	// existing entry, not append a duplicate IP (the reported crash).
	rec := doJSON(t, r, "PUT", "/api/clients/aa:bb:cc:00:11:22",
		`{"name":"","ip":"192.168.68.117"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body)
	}
	cfg := store.Get()
	if len(cfg.Clients) != 1 {
		t.Fatalf("clients = %+v, want exactly one (absorbed, not duplicated)", cfg.Clients)
	}
	if cfg.Clients[0].MAC != "aa:bb:cc:00:11:22" || cfg.Clients[0].IP != "192.168.68.117" || cfg.Clients[0].Name != "" {
		t.Errorf("client = %+v, want MAC-keyed, IP kept, label cleared", cfg.Clients[0])
	}
	// Forget by MAC removes the (now MAC-keyed) entry.
	if rec := doJSON(t, r, "DELETE", "/api/clients/aa:bb:cc:00:11:22", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("forget: status = %d: %s", rec.Code, rec.Body)
	}
	if len(store.Get().Clients) != 0 {
		t.Errorf("clients after forget = %+v", store.Get().Clients)
	}
}

func TestClientsListsSeenDevices(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.clients.Seed("192.168.1.77", 42, 7, timeNowMinus(3600), timeNowMinus(60))

	rec := doJSON(t, s.Router(), "GET", "/api/clients", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var devices []clients.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || !devices[0].Seen || devices[0].Queries != 42 || devices[0].QBlocked != 7 {
		t.Fatalf("devices = %+v", devices)
	}
	if devices[0].Group != "default" {
		t.Errorf("unassigned device group = %q, want default", devices[0].Group)
	}
}

func timeNowMinus(sec int) time.Time { return time.Now().Add(-time.Duration(sec) * time.Second) }

// Notes ride the same upsert as name/group and persist in config.
func TestClientNotesPersist(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	rec := doJSON(t, r, "PUT", "/api/clients/192.168.1.77",
		`{"notes":"kid's tablet, lives in the kitchen"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set notes: status = %d: %s", rec.Code, rec.Body)
	}
	cl := store.Get().Clients
	if len(cl) != 1 || cl[0].Notes != "kid's tablet, lives in the kitchen" {
		t.Fatalf("persisted clients = %+v, want one with notes", cl)
	}
	// Partial update: changing the group leaves notes alone.
	rec = doJSON(t, r, "PUT", "/api/clients/192.168.1.77", `{"name":"tablet"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set name: status = %d: %s", rec.Code, rec.Body)
	}
	cl = store.Get().Clients
	if cl[0].Notes == "" || cl[0].Name != "tablet" {
		t.Errorf("after name update: %+v, want notes preserved", cl[0])
	}
	// The merged device view carries them through.
	var devices []struct {
		IP    string `json:"ip"`
		Notes string `json:"notes"`
	}
	rec = doJSON(t, r, "GET", "/api/clients", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range devices {
		if d.IP == "192.168.1.77" && d.Notes != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("device view missing notes: %+v", devices)
	}
}
