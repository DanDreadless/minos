// Package updates implements the strictly opt-in release check. When (and
// only when) update_check is enabled in the config, it asks the GitHub
// releases API for the latest tag once a day. Nothing about the user or
// their network is transmitted — the request itself is the only signal —
// and it is off by default, keeping the no-telemetry promise literal.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"minos/internal/config"
)

const (
	defaultReleaseURL = "https://api.github.com/repos/DanDreadless/minos/releases/latest"
	checkInterval     = 24 * time.Hour
	// tickInterval is how often the enabled flag is re-read, so flipping
	// the toggle in Settings takes effect within a minute.
	tickInterval = time.Minute
	fetchTimeout = 15 * time.Second
	maxBody      = 1 << 20
)

// Checker polls for new releases while enabled. Safe for concurrent use.
type Checker struct {
	version string
	store   *config.Store
	client  *http.Client
	url     string // overridden in tests

	latest    atomic.Pointer[string] // trimmed tag, e.g. "0.3.0"
	lastCheck atomic.Int64           // unix nanos of the last fetch attempt

	// onUpdate, when set (before Run), fires once per newly seen newer
	// version — not on every daily re-confirmation.
	onUpdate     func(version string)
	notifyMu     sync.Mutex // Guards lastNotified: the loop and CheckNow
	lastNotified string
}

// OnUpdate registers the new-release callback. Call before Run.
func (c *Checker) OnUpdate(fn func(version string)) { c.onUpdate = fn }

func NewChecker(version string, store *config.Store) *Checker {
	return &Checker{
		version: version,
		store:   store,
		client:  &http.Client{Timeout: fetchTimeout},
		url:     defaultReleaseURL,
	}
}

// Latest returns the newest known release version and whether it is newer
// than the running one. Empty until an enabled check has succeeded.
// Dev builds (any non-release version like "0.1.0-dev") never report:
// update prompts are for people running releases, not people running code.
func (c *Checker) Latest() (version string, available bool) {
	if !isRelease(c.version) {
		return "", false
	}
	v := c.latest.Load()
	if v == nil {
		return "", false
	}
	return *v, IsNewer(*v, c.version)
}

// Run polls until ctx ends. The enabled flag is re-read every tick, so no
// request is ever made while update_check is off — and never at all from
// a dev build.
func (c *Checker) Run(ctx context.Context) {
	if !isRelease(c.version) {
		slog.Debug("update check disabled: not a release build", "version", c.version)
		return
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !c.store.Get().UpdateCheck {
				continue
			}
			if last := c.lastCheck.Load(); last != 0 &&
				time.Since(time.Unix(0, last)) < checkInterval {
				continue
			}
			c.check(ctx)
		}
	}
}

// check fetches the latest release tag for the daily loop; failures are
// logged quietly and retried on the next daily cycle.
func (c *Checker) check(ctx context.Context) {
	if !isRelease(c.version) {
		return // defense in depth; Run already refuses to get here
	}
	if _, err := c.fetch(ctx); err != nil {
		slog.Debug("update check failed", "err", err)
	}
}

// ErrNotRelease: a development build never checks for releases.
var ErrNotRelease = errors.New("this is a development build, which does not check for releases")

// CheckNow asks for the latest release immediately — the Settings button,
// so nobody waits for the daily check. It is the user's own request, so it
// runs whether or not the daily check is switched on; like it, it sends
// nothing but the request. It returns the newest version and whether it is
// newer than the one running.
func (c *Checker) CheckNow(ctx context.Context) (latest string, available bool, err error) {
	if !isRelease(c.version) {
		return "", false, ErrNotRelease
	}
	tag, err := c.fetch(ctx)
	if err != nil {
		return "", false, err
	}
	return tag, IsNewer(tag, c.version), nil
}

// fetch asks GitHub for the latest release tag, records it, and announces
// a newly seen newer version once.
func (c *Checker) fetch(ctx context.Context) (string, error) {
	c.lastCheck.Store(time.Now().UnixNano())
	reqCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Minos/"+c.version+" (+https://github.com/DanDreadless/minos)")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&release); err != nil {
		return "", fmt.Errorf("GitHub's answer could not be read: %w", err)
	}
	tag := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if tag == "" {
		return "", errors.New("GitHub reported no release")
	}
	c.latest.Store(&tag)
	if IsNewer(tag, c.version) {
		slog.Info("a newer minos release is available",
			"running", c.version, "latest", tag)
		// The daily loop and the button can both get here.
		c.notifyMu.Lock()
		notify := c.onUpdate != nil && tag != c.lastNotified
		if notify {
			c.lastNotified = tag
		}
		c.notifyMu.Unlock()
		if notify {
			c.onUpdate(tag)
		}
	}
	return tag, nil
}

// IsNewer reports whether latest is a strictly newer semantic version than
// current. Suffixes like "-dev" are ignored ("0.2.0-dev" compares as
// "0.2.0"); unparseable versions are never "newer".
func IsNewer(latest, current string) bool {
	l, okL := parseVersion(latest)
	c, okC := parseVersion(current)
	if !okL || !okC {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// isRelease reports whether v is a clean release version ("0.4.0" or
// "v0.4.0") — no -dev or other suffix.
func isRelease(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if strings.ContainsAny(v, "-+") {
		return false
	}
	_, ok := parseVersion(v)
	return ok
}

func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// String renders the check state for logs/debugging.
func (c *Checker) String() string {
	v, avail := c.Latest()
	return fmt.Sprintf("updates{latest=%q available=%v}", v, avail)
}
