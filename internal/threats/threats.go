// Package threats watches DNS behaviour for signs of an infected device,
// beyond what blocklists know:
//
//   - dga: a device looking up many different machine-generated domain
//     names in a few minutes — how malware searches for its control server
//     when the old one is taken down.
//   - tunnel: many long, random names under one domain — data carried out
//     (or commands carried in) inside DNS lookups.
//   - known_threat: a lookup blocked by a threat-intelligence list (malware,
//     phishing) — the device tried to reach a known malicious domain.
//
// Beaconing (check-ins on a regular clock) is deliberately not attempted:
// from DNS alone it is indistinguishable from ordinary polling, because
// devices cache answers and re-ask on a timer.
//
// It reads the query-log stream in its own goroutine, so the query path
// pays nothing. Findings live in memory.
package threats

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"minos/internal/querylog"
)

// Kind is what a finding is about.
type Kind string

const (
	KindDGA     Kind = "dga"
	KindTunnel  Kind = "tunnel"
	KindKnown   Kind = "known_threat"
	sampleLimit      = 5
	maxTracked       = 20000
)

// Tuning; variables so tests (and the replay tool) can change them.
var (
	DGAWindow       = 10 * time.Minute
	DGAThreshold    = 12 // Distinct generated-looking registrable domains in DGAWindow
	TunnelWindow    = 5 * time.Minute
	TunnelThreshold = 20                 // Distinct tunnel-like names under one domain in TunnelWindow
	KeepFor         = 24 * time.Hour     // A finding lasts this long after its last evidence
	QuietFor        = 7 * 24 * time.Hour // A dismissed finding stays quiet this long
	// QuietKnownFor is shorter: a dismissed known-threat alert must not hide
	// a new incident on that device for a week.
	QuietKnownFor = 24 * time.Hour
)

// builtinIgnore are services that look like tunnelling by design: antivirus
// and spam-reputation lookups encode a file hash or address in the name.
var builtinIgnore = []string{
	"sophosxl.net", "mcafee.com", "mcafee.net", "eset.com", "e5.sk", "trendmicro.com",
	"brightcloud.com", "webroot.com", "bitdefender.net", "avast.com", "avg.com",
	"kaspersky-labs.com", "kaspersky.com", "norton.com", "symantec.com", "spamhaus.org",
	"spamhaus.net", "spamcop.net", "surbl.org", "uribl.com", "sorbs.net",
	"barracudacentral.org", "mailspike.net", "dnswl.org", "abuseat.org", "senderscore.com",
	"cymru.com", "team-cymru.org", "malwarebytes.com", "f-secure.com", "fsapi.com",
}

// Finding is one device behaving like malware.
type Finding struct {
	Kind   Kind   `json:"kind"`
	Client string `json:"client"`
	Domain string `json:"domain,omitempty"` // The domain concerned (known_threat: the first); empty for dga
	List   string `json:"list,omitempty"`   // known_threat: the list that blocked it
	Count  int    `json:"count"`            // Lookups that make up the evidence
	// Distinct is how many different domains a known_threat finding covers.
	Distinct int       `json:"distinct,omitempty"`
	Samples  []string  `json:"samples"` // Up to a few example names
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
}

type fkey struct {
	kind   Kind
	client string
	domain string
}

type window struct {
	seen map[string]time.Time // Name (or registrable domain) → last seen
}

func (w *window) add(name string, at time.Time, span time.Duration) int {
	if w.seen == nil {
		w.seen = map[string]time.Time{}
	}
	w.seen[name] = at
	for n, t := range w.seen {
		if at.Sub(t) > span {
			delete(w.seen, n)
		}
	}
	return len(w.seen)
}

func (w *window) samples() []string {
	type st struct {
		n string
		t time.Time
	}
	all := make([]st, 0, len(w.seen))
	for n, t := range w.seen {
		all = append(all, st{n, t})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.After(all[j].t) })
	out := []string{}
	for i := 0; i < len(all) && i < sampleLimit; i++ {
		out = append(out, all[i].n)
	}
	return out
}

// Options wire the detector to the rest of Minos.
type Options struct {
	// Enabled and Ignore read the live config (threats.enabled /
	// threats.ignore).
	Enabled func() bool
	Ignore  func() []string
	// ThreatList reports whether a list is a threat-intelligence feed.
	ThreatList func(name string) bool
	// OnNew is called for each new finding (a notification).
	OnNew func(Finding)
}

// Detector finds threats. Safe for concurrent use.
type Detector struct {
	opt Options

	mu       sync.Mutex
	dga      map[string]*window // By client
	tunnel   map[fkey]*window   // By client + registrable domain
	findings map[fkey]*Finding
	quiet    map[fkey]time.Time
}

// New returns a detector.
func New(opt Options) *Detector {
	return &Detector{
		opt:      opt,
		dga:      map[string]*window{},
		tunnel:   map[fkey]*window{},
		findings: map[fkey]*Finding{},
		quiet:    map[fkey]time.Time{},
	}
}

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
			if d.opt.Enabled == nil || d.opt.Enabled() {
				d.Observe(e)
			}
		case now := <-sweep.C:
			d.sweep(now)
		}
	}
}

// ignored reports whether domain (a registrable domain or a name) is on the
// built-in or configured ignore list.
func (d *Detector) ignored(domain string) bool {
	match := func(list []string) bool {
		for _, s := range list {
			if domain == s || strings.HasSuffix(domain, "."+s) {
				return true
			}
		}
		return false
	}
	if match(builtinIgnore) {
		return true
	}
	return d.opt.Ignore != nil && match(d.opt.Ignore())
}

// Observe judges one query-log entry. Exported for the replay tool.
func (d *Detector) Observe(e querylog.Entry) {
	switch e.List {
	case "local", "clients", "rate-limit", "firefox-doh-canary":
		return // Answered locally or refused: not the device's own lookups
	}
	reg, sub, ok := split(e.QName)
	if !ok || d.ignored(reg) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if e.Verdict == querylog.VerdictBlocked && d.opt.ThreatList != nil && d.opt.ThreatList(e.List) {
		// One finding per device, accumulating: a phone previewing a batch
		// of phishing links made 150 lookups of different known-bad domains
		// in a week on a real network — that is one incident, not 150.
		k := fkey{KindKnown, e.Client, ""}
		f := d.findings[k]
		if f == nil {
			f = &Finding{Kind: KindKnown, Client: e.Client, First: e.Time}
			defer d.raise(k, f)
		}
		f.Count++
		f.Last, f.List = e.Time, e.List
		if f.Domain == "" {
			f.Domain = e.QName // The first one, for the headline
		}
		if !slices.Contains(f.Samples, e.QName) {
			f.Samples = append([]string{e.QName}, f.Samples...)
			if len(f.Samples) > sampleLimit {
				f.Samples = f.Samples[:sampleLimit]
			}
			f.Distinct++
		}
	}

	if randomLabel(label(reg)) {
		w := d.dga[e.Client]
		if w == nil {
			if len(d.dga) >= maxTracked {
				return
			}
			w = &window{}
			d.dga[e.Client] = w
		}
		if n := w.add(reg, e.Time, DGAWindow); n >= DGAThreshold {
			k := fkey{KindDGA, e.Client, ""}
			if f := d.findings[k]; f != nil {
				f.Count, f.Last, f.Samples = max(f.Count, n), e.Time, w.samples()
			} else {
				f = &Finding{Kind: KindDGA, Client: e.Client, Count: n, Samples: w.samples(), First: e.Time, Last: e.Time}
				d.raise(k, f)
			}
		}
	}

	if tunnelLike(sub) {
		tk := fkey{KindTunnel, e.Client, reg}
		w := d.tunnel[tk]
		if w == nil {
			if len(d.tunnel) >= maxTracked {
				return
			}
			w = &window{}
			d.tunnel[tk] = w
		}
		if n := w.add(e.QName, e.Time, TunnelWindow); n >= TunnelThreshold {
			if f := d.findings[tk]; f != nil {
				f.Count, f.Last, f.Samples = max(f.Count, n), e.Time, w.samples()
			} else {
				f = &Finding{Kind: KindTunnel, Client: e.Client, Domain: reg, Count: n, Samples: w.samples(), First: e.Time, Last: e.Time}
				d.raise(tk, f)
			}
		}
	}
}

// raise records a new finding unless it is quiet; the caller holds mu.
func (d *Detector) raise(k fkey, f *Finding) {
	if until, ok := d.quiet[k]; ok && f.Last.Before(until) {
		return
	}
	d.findings[k] = f
	slog.Warn("possible threat on a device", "kind", f.Kind, "client", f.Client, "domain", f.Domain, "count", f.Count)
	if d.opt.OnNew != nil {
		c := *f
		c.Samples = append([]string(nil), f.Samples...)
		go d.opt.OnNew(c)
	}
}

func (d *Detector) sweep(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for c, w := range d.dga {
		w.add("", now, DGAWindow)
		delete(w.seen, "")
		if len(w.seen) == 0 {
			delete(d.dga, c)
		}
	}
	for k, w := range d.tunnel {
		w.add("", now, TunnelWindow)
		delete(w.seen, "")
		if len(w.seen) == 0 {
			delete(d.tunnel, k)
		}
	}
	for k, f := range d.findings {
		if now.Sub(f.Last) > KeepFor {
			delete(d.findings, k)
		}
	}
	for k, until := range d.quiet {
		if now.After(until) {
			delete(d.quiet, k)
		}
	}
}

// Findings returns current findings, most recent first.
func (d *Detector) Findings() []Finding {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Finding, 0, len(d.findings))
	for _, f := range d.findings {
		c := *f
		c.Samples = append([]string(nil), f.Samples...)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// Dismiss hides a finding and keeps it quiet for QuietFor.
func (d *Detector) Dismiss(kind Kind, client, domain string) bool {
	k := fkey{kind, client, domain}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.findings[k]
	delete(d.findings, k)
	quiet := QuietFor
	if kind == KindKnown {
		quiet = QuietKnownFor
	}
	d.quiet[k] = time.Now().Add(quiet)
	if kind == KindDGA {
		delete(d.dga, client)
	}
	return ok
}

// Forget drops findings about domain (or its subdomains), for any device —
// called when the user adds it to threats.ignore.
func (d *Detector) Forget(domain string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.findings {
		if k.domain == domain || strings.HasSuffix(k.domain, "."+domain) {
			delete(d.findings, k)
		}
	}
}

// ThreatFeed reports whether a blocklist is a threat-intelligence feed
// (malware, phishing, scams) by its name or URL: lookups it blocks are
// worth telling someone about, unlike ad blocks.
func ThreatFeed(name, url string) bool {
	s := strings.ToLower(name + " " + url)
	for _, w := range []string{"tif", "threat", "urlhaus", "malware", "phish", "scam", "ransom"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
