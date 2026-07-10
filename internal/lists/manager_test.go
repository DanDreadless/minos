package lists

import (
	"context"
	"testing"

	"minos/internal/config"
	"minos/internal/filter"
)

// TestGlobalBlockedServices proves a service picked in config compiles into
// the global matcher with a "service:<name>" list attribution.
func TestGlobalBlockedServices(t *testing.T) {
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Blocking.Services = []string{"tiktok"}
		c.Blocking.Allowlist = []string{"tiktokcdn.com"} // pardon beats service
		c.Lists.Sources = nil                            // no network in tests
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	m := NewManager(engine, store)
	m.rebuild(context.Background(), false)

	res := engine.Match("www.tiktok.com")
	if !res.Blocked || res.List != "service:tiktok" {
		t.Errorf("www.tiktok.com = %+v, want blocked by service:tiktok", res)
	}
	if res := engine.Match("tiktokcdn.com"); res.Blocked {
		t.Errorf("tiktokcdn.com blocked despite allowlist pardon: %+v", res)
	}
	if res := engine.Match("example.org"); res.Blocked {
		t.Errorf("example.org unexpectedly blocked: %+v", res)
	}
}

// TestUnknownServiceRejected: config validation refuses names not in the
// catalog, so a typo can't silently block nothing.
func TestUnknownServiceRejected(t *testing.T) {
	cfg := config.Default()
	cfg.Blocking.Services = []string{"myspace"}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted unknown service name")
	}
	cfg.Blocking.Services = nil
	cfg.Groups = []config.Group{{Name: "kids", Mode: "filter", Services: []string{"nope"}}}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted unknown group service name")
	}
}

// TestGlobalAllowedServices proves a pardoned service compiles into the
// global matcher as allow rules that beat denies — including the same
// service being blocked — and that its playback extras are covered.
func TestGlobalAllowedServices(t *testing.T) {
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Blocking.Denylist = []string{"nflxvideo.net", "atv-ps.amazon.com"}
		c.Blocking.Services = []string{"netflix"} // allowed wins over blocked
		c.Blocking.AllowedServices = []string{"netflix", "primevideo"}
		c.Lists.Sources = nil // no network in tests
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	m := NewManager(engine, store)
	m.rebuild(context.Background(), false)

	res := engine.Match("occ-1.nflxvideo.net")
	if res.Blocked || res.List != "service:netflix" {
		t.Errorf("nflxvideo.net = %+v, want passed by service:netflix", res)
	}
	// The allow bundle's extras cover hosts the deny bundle never names.
	res = engine.Match("atv-ps.amazon.com")
	if res.Blocked || res.List != "service:primevideo" {
		t.Errorf("atv-ps.amazon.com = %+v, want passed by service:primevideo", res)
	}
	if res := engine.Match("amazon.com"); res.Rule != "" {
		t.Errorf("amazon.com matched %+v, want untouched (extras are precise hosts)", res)
	}
}

// TestUnknownAllowedServiceRejected mirrors the blocked-service check.
func TestUnknownAllowedServiceRejected(t *testing.T) {
	cfg := config.Default()
	cfg.Blocking.AllowedServices = []string{"myspace"}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted unknown allowed service name")
	}
	cfg.Blocking.AllowedServices = nil
	cfg.Groups = []config.Group{{Name: "kids", Mode: "filter", AllowedServices: []string{"nope"}}}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted unknown group allowed service name")
	}
}
