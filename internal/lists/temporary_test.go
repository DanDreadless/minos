package lists

import (
	"context"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/filter"
)

// A temporary allow beats a block until it expires, is attributed to its own
// list, and pruning ends it.
func TestTemporaryAllow(t *testing.T) {
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.Update(func(c *config.Config) error {
		c.Blocking.Denylist = []string{"ads.example.com", "tracker.example.net"}
		c.Blocking.TemporaryAllows = []config.TemporaryAllow{
			{Domain: "ads.example.com", Until: now.Add(time.Hour)},
			{Domain: "tracker.example.net", Until: now.Add(-time.Minute)}, // already over
		}
		c.Lists.Sources = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	m := NewManager(engine, store)
	m.rebuild(context.Background(), false)

	if res := engine.Match("ads.example.com"); res.Blocked || res.List != ListTemporaryAllow {
		t.Errorf("ads.example.com = %+v, want allowed by %s", res, ListTemporaryAllow)
	}
	if res := engine.Match("cdn.ads.example.com"); res.Blocked {
		t.Errorf("a subdomain of a temporary allow was blocked: %+v", res)
	}
	if res := engine.Match("tracker.example.net"); !res.Blocked {
		t.Errorf("an expired temporary allow still allowed: %+v", res)
	}

	// Pruning removes only what has expired.
	if n := m.pruneTemporaryAllows(now); n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if got := store.Get().Blocking.TemporaryAllows; len(got) != 1 || got[0].Domain != "ads.example.com" {
		t.Fatalf("left %+v", got)
	}
	if n := m.pruneTemporaryAllows(now); n != 0 {
		t.Errorf("a second prune removed %d", n)
	}
	// When the hour is up, the block is back.
	if n := m.pruneTemporaryAllows(now.Add(2 * time.Hour)); n != 1 {
		t.Fatalf("pruned %d after expiry, want 1", n)
	}
	m.rebuild(context.Background(), false)
	if res := engine.Match("ads.example.com"); !res.Blocked {
		t.Errorf("still allowed after the temporary allow ended: %+v", res)
	}
}
