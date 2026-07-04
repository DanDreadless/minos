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
