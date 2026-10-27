package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/querylog"
)

func TestWeeklySummary(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	if err := store.Update(func(c *config.Config) error {
		c.Lists.Sources = []config.ListSource{
			{Name: "URLhaus", URL: "https://urlhaus.example/hosts.txt", Format: "hosts", Enabled: true},
			{Name: "StevenBlack", URL: "https://sb.example/hosts", Format: "hosts", Enabled: true},
		}
		c.Clients = []config.Client{{IP: "10.0.0.9", Name: "Kids tablet"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A settings change this week (by a person, so it is recorded).
	if err := store.UpdateAs("Password sign-in from 10.0.0.2", "", func(c *config.Config) error {
		c.Blocking.Denylist = []string{"bad.example"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	s.clients.Touch("10.0.0.9", false, now) // First seen now: new this week
	for _, e := range []querylog.Entry{
		{Client: "10.0.0.9", QName: "evil.example", Verdict: querylog.VerdictBlocked, List: "URLhaus"},
		{Client: "10.0.0.9", QName: "ads.example", Verdict: querylog.VerdictBlocked, List: "StevenBlack"},
		{Client: "10.0.0.9", QName: "pixel.facebook.com", Verdict: querylog.VerdictAllowed},
		{Client: "10.0.0.9", QName: "example.org", Verdict: querylog.VerdictAllowed},
	} {
		e.Time, e.QType = now, "A"
		s.qlog.Record(e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(s.qlog.Recent(0)) < 4 {
		time.Sleep(5 * time.Millisecond)
	}

	rec := doJSON(t, r, "GET", "/api/summary", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got weekSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Lookups != 4 || got.Blocked != 2 || got.PrevLookups != 0 {
		t.Errorf("lookups %d blocked %d prev %d", got.Lookups, got.Blocked, got.PrevLookups)
	}
	if got.NewDeviceCount != 1 || got.NewDevices[0].Name != "Kids tablet" || got.Busiest == nil || got.Busiest.Lookups != 4 {
		t.Errorf("devices: new %+v busiest %+v", got.NewDevices, got.Busiest)
	}
	// Only the threat feed counts as security, not every blocklist.
	if got.Security.Blocked != 1 || len(got.Security.Devices) != 1 || len(got.SecurityLists) != 1 || got.SecurityLists[0] != "URLhaus" {
		t.Errorf("security %+v lists %v", got.Security, got.SecurityLists)
	}
	if got.Tracking.Companies != 1 || got.Tracking.Allowed != 1 || len(got.Tracking.Top) != 1 || got.Tracking.Top[0].ID != "meta" {
		t.Errorf("tracking %+v", got.Tracking)
	}
	if got.SettingsChanges != 1 || got.LastChange == nil || got.LastChange.Actor != "Password sign-in from 10.0.0.2" {
		t.Errorf("settings %d %+v", got.SettingsChanges, got.LastChange)
	}
}
