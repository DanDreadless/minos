package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// DigestStats is the digest's data source. It is declared here with
// builtin-typed signatures so this package never imports internal/querylog;
// *querylog.Log satisfies it structurally at the wiring site in main.
type DigestStats interface {
	Totals(ctx context.Context, since time.Time) (total, blocked int, err error)
	TopBlockedSummary(ctx context.Context, since time.Time, n int) (domains []string, counts []int, err error)
	BusiestClient(ctx context.Context, since time.Time) (client string, queries int, err error)
	NewClientsSince(ctx context.Context, since time.Time) (int, error)
}

// digestCheckInterval is how often the scheduler re-reads the config and
// checks whether a fire instant has passed — so enabling the digest in
// Settings never waits for a stale timer, and cadence changes apply live.
const digestCheckInterval = time.Minute

// RunDigest sends periodic summaries ("here's what your network did")
// through the normal event queue until ctx ends. Cadence, delivery time,
// and (for weekly) delivery day come from notifications.digest /
// digest_time / digest_day, re-read on every check so Settings changes
// apply live. Blocks; run in a goroutine beside Run.
func (n *Notifier) RunDigest(ctx context.Context, stats DigestStats) {
	ticker := time.NewTicker(digestCheckInterval)
	defer ticker.Stop()
	last := time.Now() // startup is "just checked": no catch-up sends
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		cfg := n.store.Get().Notifications
		if cadence := cfg.Digest; cadence == "daily" || cadence == "weekly" {
			hour, minute, day := cfg.DigestSchedule()
			if fire := nextDigestFire(cadence, hour, minute, day, last); !now.Before(fire) {
				n.sendDigest(ctx, stats, cadence, now)
			}
		}
		last = now
	}
}

// nextDigestFire returns the first fire instant strictly after `after`:
// the next hh:mm for daily, the next `day` at hh:mm for weekly, in after's
// location. time.Date normalization keeps it correct across DST changes.
func nextDigestFire(cadence string, hour, minute int, day time.Weekday, after time.Time) time.Time {
	y, m, d := after.Date()
	for add := 0; add <= 7; add++ {
		cand := time.Date(y, m, d+add, hour, minute, 0, 0, after.Location())
		if !cand.After(after) {
			continue
		}
		if cadence == "weekly" && cand.Weekday() != day {
			continue
		}
		return cand
	}
	// Unreachable: any 8-day span contains every weekday at hh:mm.
	return after.AddDate(0, 0, 7)
}

func (n *Notifier) sendDigest(ctx context.Context, stats DigestStats, cadence string, now time.Time) {
	window := 24 * time.Hour
	title := "Minos daily digest"
	period := "24 hours"
	if cadence == "weekly" {
		window = 7 * 24 * time.Hour
		title = "Minos weekly digest"
		period = "7 days"
	}
	msg, err := digestMessage(ctx, stats, now.Add(-window), period)
	if err != nil {
		slog.Debug("digest skipped", "err", err)
		return
	}
	n.Publish("digest", title, msg)
}

// digestMessage assembles the plain-text summary. Copy stays literal —
// notifications land outside the UI, where themed labels have no hover text.
func digestMessage(ctx context.Context, stats DigestStats, since time.Time, period string) (string, error) {
	total, blocked, err := stats.Totals(ctx, since)
	if err != nil {
		return "", fmt.Errorf("digest totals: %w", err)
	}
	var b strings.Builder
	if total == 0 {
		fmt.Fprintf(&b, "No queries in the last %s.", period)
		return b.String(), nil
	}
	pct := 100 * float64(blocked) / float64(total)
	fmt.Fprintf(&b, "%s queries in the last %s — %s blocked (%.1f%%).",
		thousands(total), period, thousands(blocked), pct)

	if domains, counts, err := stats.TopBlockedSummary(ctx, since, 3); err == nil && len(domains) > 0 {
		parts := make([]string, len(domains))
		for i := range domains {
			parts[i] = fmt.Sprintf("%s (%s)", domains[i], thousands(counts[i]))
		}
		fmt.Fprintf(&b, "\nTop blocked: %s.", strings.Join(parts, ", "))
	}
	if client, queries, err := stats.BusiestClient(ctx, since); err == nil && client != "" {
		fmt.Fprintf(&b, "\nBusiest client: %s (%s queries).", client, thousands(queries))
	}
	if newDevices, err := stats.NewClientsSince(ctx, since); err == nil && newDevices > 0 {
		plural := "s"
		if newDevices == 1 {
			plural = ""
		}
		fmt.Fprintf(&b, "\n%d new device%s.", newDevices, plural)
	}
	return b.String(), nil
}

// thousands formats n with comma separators (12,041) for readability in
// plain-text notifications.
func thousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
