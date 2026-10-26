package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/querylog"
	"minos/internal/threats"
)

func TestThreatEndpoints(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	var list []threatView
	_ = json.Unmarshal(do(t, r, "GET", "/api/threats", "", nil, nil).Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("no detector: %+v", list)
	}

	d := threats.New(threats.Options{
		ThreatList: func(l string) bool { return threats.ThreatFeed(l, "") },
		Ignore:     func() []string { return store.Get().Threats.Ignore },
	})
	s.SetThreats(d)
	e := querylog.Entry{Time: time.Now(), Client: "192.168.1.21", QName: "evil.example.com", Verdict: querylog.VerdictBlocked, List: "URLhaus"}
	d.Observe(e)
	_ = json.Unmarshal(do(t, r, "GET", "/api/threats", "", nil, nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0].Kind != threats.KindKnown || list[0].Client != "192.168.1.21" {
		t.Fatalf("threats %+v", list)
	}

	if rec := do(t, r, "POST", "/api/threats/dismiss", `{"kind":"known_threat","client":"192.168.1.21","domain":""}`, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rec.Code)
	}
	_ = json.Unmarshal(do(t, r, "GET", "/api/threats", "", nil, nil).Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("after dismiss: %+v", list)
	}

	if rec := do(t, r, "POST", "/api/threats/ignore", `{"domain":"Example.NET."}`, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("ignore: %d %s", rec.Code, rec.Body)
	}
	if got := store.Get().Threats.Ignore; len(got) != 1 || got[0] != "example.net" {
		t.Fatalf("ignore list %v", got)
	}
	if rec := do(t, r, "POST", "/api/threats/ignore", `{"domain":"not a domain"}`, nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad domain: %d", rec.Code)
	}
	// Settings round-trip: enabled and ignore are editable, and cleaned.
	if rec := do(t, r, "PUT", "/api/config", `{"threats":{"enabled":false,"ignore":[" A.example ", "a.example", ""]}}`, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	if c := store.Get().Threats; c.Enabled || len(c.Ignore) != 1 || c.Ignore[0] != "a.example" {
		t.Fatalf("threats config %+v", c)
	}
}
