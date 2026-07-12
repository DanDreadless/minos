package clients

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

var neighUnavailable sync.Once

// readIPv6Neighbors shells out to iproute2 (present on every Linux target,
// Raspberry Pi OS included). A missing binary or a failing exec logs once
// at Debug and yields nothing — enrichment is best-effort, never an error.
func readIPv6Neighbors() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "-6", "neigh", "show").Output()
	if err != nil {
		neighUnavailable.Do(func() {
			slog.Debug("ipv6 neighbour table unavailable; IPv6 clients stay untagged", "err", err)
		})
		return nil
	}
	return parseNeighOutput(string(out))
}
