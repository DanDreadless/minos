// Package breakage spots apps that blocking has probably broken: a device
// retrying a name it used to reach, which a blocklist has started blocking.
//
// Two things must both be true. The device is retrying — the same blocked
// name dozens of times in a couple of minutes. And the name used to work
// for it: answered normally (no allow rule, blocking not paused) at least
// MinWorked times in the week before. The second test is what separates a
// broken app from page tags and trackers: Google Tag Manager is asked for
// on every page load and so arrives in bursts too, but it was blocked all
// along — it never "worked" — so it is never suggested. (The first version
// asked only that the device hadn't been hammering the name the day
// before, and alerted on exactly that kind of tag after a quiet day.)
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
	// same device in the 24 hours before the burst means it has been blocked
	// and retried for a day already — chatter, not a fresh breakage.
	ChronicLimit = 3 * Threshold
	// MinWorked and WorkedWindow: the name must have been answered normally
	// for the device at least MinWorked times in the WorkedWindow before the
	// burst.
	MinWorked    = 3
	WorkedWindow = 7 * 24 * time.Hour
	// KeepFor is how long a suggestion stays after its last blocked lookup.
	KeepFor = time.Hour
	// QuietFor is how long a pair that failed the evidence test is left
	// alone; DismissFor how long one the user said is not broken is.
	QuietFor   = 24 * time.Hour
	DismissFor = 7 * 24 * time.Hour
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
	// Worked is how often the name was answered normally for the device in
	// the week before (capped at WorkedCap), and WorkedLast the latest time.
	Worked     int       `json:"worked"`
	WorkedLast time.Time `json:"worked_last"`
}

// WorkedCap bounds the "used to work" count the history is asked for.
const WorkedCap = 1000

// Evidence is what the query log says about a device and a name before a
// burst.
type Evidence struct {
	// Blocked: blocked lookups in the 24 hours before (up to ChronicLimit+1).
	Blocked int
	// Worked and WorkedLast: normal answers in the WorkedWindow before.
	Worked     int
	WorkedLast time.Time
}

// History gathers the Evidence for a burst that began at start.
type History func(ctx context.Context, client, domain string, start time.Time) (Evidence, error)

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

// New returns a detector. Without a history nothing is ever suggested:
// a burst alone is not evidence of breakage.
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
		d.quiet[k] = e.Time.Add(QuietFor)
		delete(d.bursts, k)
		return
	}
	// The evidence comes from the query log: off the lock, in its own
	// goroutine, so the stream keeps flowing.
	d.pending[k] = true
	go d.check(ctx, k, s)
}

// check asks the query log whether the name used to work for the device
// and isn't long-standing chatter, and suggests it only if both hold.
func (d *Detector) check(ctx context.Context, k key, s Suggestion) {
	ev, err := d.history(ctx, k.client, k.domain, s.Since)
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.pending, k)
	switch {
	case err != nil:
		// No evidence, no alert; the next burst asks again.
		slog.Debug("breakage: history check failed", "err", err)
		delete(d.bursts, k)
	case ev.Worked < MinWorked || ev.Blocked > ChronicLimit:
		// Never worked (a tag or tracker blocked all along), or blocked and
		// retried for a day already: leave the pair be for a day.
		d.quiet[k] = s.Last.Add(QuietFor)
		delete(d.bursts, k)
	default:
		s.Worked, s.WorkedLast = ev.Worked, ev.WorkedLast
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

// Dismiss hides a suggestion and stops it returning for DismissFor.
func (d *Detector) Dismiss(client, domain string) bool {
	k := key{client, domain}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.active[k]
	delete(d.active, k)
	delete(d.bursts, k)
	d.quiet[k] = time.Now().Add(DismissFor)
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
