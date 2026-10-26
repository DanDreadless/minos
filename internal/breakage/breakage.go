// Package breakage spots apps that blocking has probably broken. When a
// lookup is blocked and the app needs it, the app retries — the same device
// asks for the same blocked name dozens of times a minute. Ad and tracker
// SDKs retry too, but they do it all day; a breakage starts suddenly. So a
// burst counts only when the device was not already hammering that name in
// the previous 24 hours.
//
// It reads the query-log stream in its own goroutine: nothing here runs on
// the query path. Blocks the user chose on purpose (their own blocked
// domains, blocked services, group rules, a blocked device) are never
// suggested — retries there are the point.
package breakage

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"minos/internal/querylog"
)

// Tuning; variables so tests can shrink them.
var (
	// Window and Threshold: Threshold blocked lookups of one name by one
	// device within Window is a burst.
	Window    = 2 * time.Minute
	Threshold = 15
	// ChronicLimit: more blocked lookups than this of the same name by the
	// same device in the 24 hours before the burst means chatter, not
	// breakage.
	ChronicLimit = 3 * Threshold
	// KeepFor is how long a suggestion stays after its last blocked lookup.
	KeepFor = time.Hour
	// QuietFor is how long a dismissed or chronic pair is not suggested.
	QuietFor = 24 * time.Hour
)

// maxTracked bounds the burst table; past it, the oldest pairs are dropped.
const maxTracked = 20000

// Suggestion is one probably-broken app: a device retrying a blocked name.
type Suggestion struct {
	Client string    `json:"client"`
	Domain string    `json:"domain"`
	List   string    `json:"list"`
	Rule   string    `json:"rule,omitempty"`
	Count  int       `json:"count"` // Blocked lookups since the burst began
	Since  time.Time `json:"since"`
	Last   time.Time `json:"last"`
}

// History counts a device's blocked lookups of domain in [since, before).
// The query log answers it; nil (ephemeral mode) skips the chronic check.
type History func(ctx context.Context, client, domain string, since, before time.Time) (int, error)

type key struct{ client, domain string }

type burst struct {
	hits []time.Time // Within Window, oldest first
	list string
	rule string
}

// Detector finds bursts. Safe for concurrent use.
type Detector struct {
	history History
	onNew   func(Suggestion)

	mu      sync.Mutex
	bursts  map[key]*burst
	active  map[key]*Suggestion
	quiet   map[key]time.Time // Not suggested until then
	pending map[key]bool      // Chronic check in flight
}

// New returns a detector; history may be nil.
func New(history History) *Detector {
	return &Detector{
		history: history,
		bursts:  map[key]*burst{},
		active:  map[key]*Suggestion{},
		quiet:   map[key]time.Time{},
		pending: map[key]bool{},
	}
}

// OnNew registers a callback for each new suggestion (a notification).
// Call before Run.
func (d *Detector) OnNew(fn func(Suggestion)) { d.onNew = fn }

// Run consumes entries until ctx ends or the channel closes.
func (d *Detector) Run(ctx context.Context, entries <-chan querylog.Entry) {
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-entries:
			if !ok {
				return
			}
			d.observe(ctx, e)
		case now := <-sweep.C:
			d.sweep(now)
		}
	}
}

// considered reports whether a block could be an unintended breakage: one
// from a subscribed blocklist, CNAME cloaking or rebinding protection, not
// a block the user chose for that name or device.
func considered(list string) bool {
	switch list {
	case "", "denylist", "clients", "firefox-doh-canary", "rate-limit", "dnssec",
		"safesearch", "icloud-private-relay":
		return false
	}
	return !strings.HasPrefix(list, "service:") && !strings.HasPrefix(list, "group:")
}

func (d *Detector) observe(ctx context.Context, e querylog.Entry) {
	k := key{e.Client, e.QName}
	d.mu.Lock()
	defer d.mu.Unlock()
	if e.Verdict != querylog.VerdictBlocked {
		// The name now passes (allowed, or the list changed): the app is
		// no longer broken by Minos.
		delete(d.active, k)
		delete(d.bursts, k)
		return
	}
	if !considered(e.List) {
		return
	}
	if s := d.active[k]; s != nil {
		s.Count++
		s.Last = e.Time
		return
	}
	if until, ok := d.quiet[k]; ok && e.Time.Before(until) {
		return
	}
	b := d.bursts[k]
	if b == nil {
		if len(d.bursts) >= maxTracked {
			d.dropOldest()
		}
		b = &burst{}
		d.bursts[k] = b
	}
	b.list, b.rule = e.List, e.Rule
	b.hits = append(b.hits, e.Time)
	cut := 0
	for cut < len(b.hits) && e.Time.Sub(b.hits[cut]) > Window {
		cut++
	}
	b.hits = b.hits[cut:]
	if len(b.hits) < Threshold || d.pending[k] {
		return
	}
	s := Suggestion{Client: e.Client, Domain: e.QName, List: b.list, Rule: b.rule,
		Count: len(b.hits), Since: b.hits[0], Last: e.Time}
	if d.history == nil {
		d.raise(k, s)
		return
	}
	// The chronic check reads the query log: off the lock, in its own
	// goroutine, so the stream keeps flowing.
	d.pending[k] = true
	go d.checkChronic(ctx, k, s)
}

func (d *Detector) checkChronic(ctx context.Context, k key, s Suggestion) {
	n, err := d.history(ctx, k.client, k.domain, s.Since.Add(-24*time.Hour), s.Since)
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pending, k)
	switch {
	case err != nil:
		slog.Debug("breakage: history check failed; suggesting anyway", "err", err)
		d.raise(k, s)
	case n > ChronicLimit:
		d.quiet[k] = s.Last.Add(QuietFor) // Chatter: leave it be for a day
		delete(d.bursts, k)
	default:
		d.raise(k, s)
	}
}

// raise records a suggestion; the caller holds mu.
func (d *Detector) raise(k key, s Suggestion) {
	if _, ok := d.active[k]; ok {
		return
	}
	d.active[k] = &s
	delete(d.bursts, k)
	slog.Info("an app looks broken by blocking", "client", s.Client, "domain", s.Domain, "list", s.List, "count", s.Count)
	if d.onNew != nil {
		go d.onNew(s)
	}
}

func (d *Detector) dropOldest() {
	var oldest key
	var at time.Time
	for k, b := range d.bursts {
		if len(b.hits) > 0 && (at.IsZero() || b.hits[len(b.hits)-1].Before(at)) {
			oldest, at = k, b.hits[len(b.hits)-1]
		}
	}
	delete(d.bursts, oldest)
}

// sweep drops idle bursts, finished suggestions and ended quiet periods.
func (d *Detector) sweep(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, b := range d.bursts {
		if len(b.hits) == 0 || now.Sub(b.hits[len(b.hits)-1]) > Window {
			delete(d.bursts, k)
		}
	}
	for k, s := range d.active {
		if now.Sub(s.Last) > KeepFor {
			delete(d.active, k)
		}
	}
	for k, until := range d.quiet {
		if now.After(until) {
			delete(d.quiet, k)
		}
	}
}

// Suggestions returns the current suggestions, most recent first.
func (d *Detector) Suggestions() []Suggestion {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Suggestion, 0, len(d.active))
	for _, s := range d.active {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// Dismiss hides a suggestion and stops it returning for QuietFor.
func (d *Detector) Dismiss(client, domain string) bool {
	k := key{client, domain}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.active[k]
	delete(d.active, k)
	delete(d.bursts, k)
	d.quiet[k] = time.Now().Add(QuietFor)
	return ok
}

// Resolve drops every suggestion for domain (or one of its subdomains) —
// called when the user allows it, whichever device was affected.
func (d *Detector) Resolve(domain string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.active {
		if k.domain == domain || strings.HasSuffix(k.domain, "."+domain) {
			delete(d.active, k)
		}
	}
}
