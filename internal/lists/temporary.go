package lists

import (
	"context"
	"log/slog"
	"time"

	"minos/internal/config"
)

// ListTemporaryAllow is the list name a temporary allow ("Allow for 1 hour")
// is attributed to in the Query Log.
const ListTemporaryAllow = "temporary-allow"

// temporaryAllowTick is how often expired temporary allows are removed; a
// variable so tests can shrink it. An allow therefore ends up to this long
// after its time — fine for "an hour".
var temporaryAllowTick = 15 * time.Second

// RunTemporaryAllows removes expired temporary allows until ctx ends.
// Removing one is a config change, which rebuilds the matcher, which is what
// actually ends the allow.
func (m *Manager) RunTemporaryAllows(ctx context.Context) {
	t := time.NewTicker(temporaryAllowTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.pruneTemporaryAllows(time.Now())
	}
}

// pruneTemporaryAllows drops expired entries from the config and reports
// how many it removed.
func (m *Manager) pruneTemporaryAllows(now time.Time) int {
	cfg := m.store.Get()
	if len(cfg.Blocking.ActiveTemporaryAllows(now)) == len(cfg.Blocking.TemporaryAllows) {
		return 0
	}
	removed := 0
	err := m.store.Update(func(c *config.Config) error {
		kept := c.Blocking.ActiveTemporaryAllows(now)
		removed = len(c.Blocking.TemporaryAllows) - len(kept)
		c.Blocking.TemporaryAllows = kept
		return nil
	})
	if err != nil {
		slog.Warn("could not remove expired temporary allows", "err", err)
		return 0
	}
	slog.Info("temporary allows ended", "count", removed)
	return removed
}
