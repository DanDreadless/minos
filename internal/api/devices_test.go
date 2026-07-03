package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/clients"
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
