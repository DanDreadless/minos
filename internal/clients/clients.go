// Package clients tracks every device that queries the resolver and resolves
// the per-device policy (group membership, bypass, block).
//
// Hot-path discipline: Touch and PolicyFor are called per query, so they are
// a sync.Map access plus atomics and one atomic pointer load — no mutexes,
// no allocation on the steady state. Enrichment (ARP, reverse DNS) and policy
// table rebuilds happen off the hot path.
package clients

import (
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/services"
)

// Group modes (mirrors config validation).
const (
	ModeFilter = "filter"
	ModeBypass = "bypass"
	ModeBlock  = "block"
)

// Policy is the resolved judgment context for one device. Nil policy means
// default: the global rules apply.
type Policy struct {
	Group   string
	Mode    string
	Blocked bool // per-device override: refuse all DNS
	// SafeSearch enforces safe-search rewrites for this group's members
	// (on top of the global blocking.safe_search flag).
	SafeSearch bool
	// Overlay holds a filter-mode group's extra allow/deny domains,
	// layered over the global matcher. Nil when the group adds none.
	Overlay *filter.Matcher
}

// Refuses reports whether this device gets no DNS service at all.
func (p *Policy) Refuses() bool { return p != nil && (p.Blocked || p.Mode == ModeBlock) }

// Bypasses reports whether this device skips filtering entirely.
func (p *Policy) Bypasses() bool { return p != nil && !p.Blocked && p.Mode == ModeBypass }

// device is the live (hot-path-updated) state for one client IP.
type device struct {
	firstSeen atomic.Int64 // unix nanos
	lastSeen  atomic.Int64
	total     atomic.Uint64
	blocked   atomic.Uint64
	mac       atomic.Pointer[string] // from ARP enrichment
	hostname  atomic.Pointer[string] // from reverse DNS
	// fresh marks a device first discovered by live traffic (not seeded
	// from history); consumed once by the new-device notification.
	fresh atomic.Bool
}

// Device is the merged API view of a client: live traffic state plus any
// configured assignment.
type Device struct {
	IP        string     `json:"ip"`
	MAC       string     `json:"mac,omitempty"`
	Hostname  string     `json:"hostname,omitempty"`
	Name      string     `json:"name,omitempty"`
	Group     string     `json:"group"`
	Blocked   bool       `json:"blocked"`
	Seen      bool       `json:"seen"`
	Queries   uint64     `json:"queries"`
	QBlocked  uint64     `json:"queries_blocked"`
	FirstSeen *time.Time `json:"first_seen,omitempty"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

// Registry is safe for concurrent use.
type Registry struct {
	seen     sync.Map // ip string → *device
	policies atomic.Pointer[map[string]*Policy]
	cfg      atomic.Pointer[config.Config] // snapshot for scheduled rebuilds
	enrichCh chan string                   // newly seen IPs awaiting enrichment
	// onNewDevice, when set (before Run), is called from the enrichment
	// worker for devices first seen via live traffic — after enrichment,
	// so MAC/hostname are included when available.
	onNewDevice func(ip, mac, hostname string)
}

// OnNewDevice registers the new-device callback. Call before Run.
func (r *Registry) OnNewDevice(fn func(ip, mac, hostname string)) { r.onNewDevice = fn }

// emitNew fires the callback exactly once per live-discovered device.
func (r *Registry) emitNew(ip string) {
	if r.onNewDevice == nil {
		return
	}
	v, ok := r.seen.Load(ip)
	if !ok {
		return
	}
	d := v.(*device)
	if !d.fresh.CompareAndSwap(true, false) {
		return
	}
	var mac, hostname string
	if m := d.mac.Load(); m != nil {
		mac = *m
	}
	if h := d.hostname.Load(); h != nil {
		hostname = *h
	}
	r.onNewDevice(ip, mac, hostname)
}

func NewRegistry() *Registry {
	r := &Registry{enrichCh: make(chan string, 256)}
	empty := make(map[string]*Policy)
	r.policies.Store(&empty)
	return r
}

// Touch records one judged query for ip. Hot path — never blocks.
func (r *Registry) Touch(ip string, blocked bool, at time.Time) {
	var d *device
	if v, ok := r.seen.Load(ip); ok {
		d = v.(*device)
	} else {
		fresh := &device{}
		fresh.firstSeen.Store(at.UnixNano())
		fresh.fresh.Store(true) // live discovery, eligible for notification
		if v, loaded := r.seen.LoadOrStore(ip, fresh); loaded {
			d = v.(*device)
		} else {
			d = fresh
			select {
			case r.enrichCh <- ip:
			default: // enrichment is best-effort; drop rather than stall
			}
		}
	}
	d.lastSeen.Store(at.UnixNano())
	d.total.Add(1)
	if blocked {
		d.blocked.Add(1)
	}
}

// PolicyFor returns the device's resolved policy, or nil for the default.
// Hot path — one atomic load and one map read.
func (r *Registry) PolicyFor(ip string) *Policy {
	return (*r.policies.Load())[ip]
}

// ApplyConfig rebuilds the policy table from config. Called on every config
// change (off the hot path), then swapped in atomically.
func (r *Registry) ApplyConfig(cfg *config.Config) {
	r.cfg.Store(cfg)
	r.rebuildPolicies(time.Now())
}

// rebuildPolicies compiles the policy table for one moment in time: a group
// with a schedule counts only while its window is active; outside it,
// members follow the default rules (a per-device block still refuses).
// Run's minute ticker re-invokes this so windows open and close on their
// own — the hot path never checks the clock.
func (r *Registry) rebuildPolicies(now time.Time) {
	cfg := r.cfg.Load()
	if cfg == nil {
		return
	}
	groups := make(map[string]*Policy, len(cfg.Groups))
	for _, g := range cfg.Groups {
		if g.Schedule != nil && !scheduleActive(g.Schedule, now) {
			continue // inactive: members resolve to the default policy
		}
		p := &Policy{Group: g.Name, Mode: g.Mode}
		if g.Mode == ModeFilter {
			p.SafeSearch = g.SafeSearch
		}
		if g.Mode == ModeFilter && (len(g.Allowlist) > 0 || len(g.Denylist) > 0 || len(g.Services) > 0) {
			b := filter.NewBuilder()
			list := "group:" + g.Name
			for _, d := range g.Allowlist {
				b.AddAllow(list, d)
			}
			for _, d := range g.Denylist {
				b.AddDeny(list, d)
			}
			// Group-blocked services join the overlay; group pardons
			// still beat them (allow wins at every label depth).
			for _, name := range g.Services {
				for _, d := range services.Domains(name) {
					b.AddDeny("service:"+name, d)
				}
			}
			p.Overlay = b.Build()
		}
		groups[g.Name] = p
	}
	table := make(map[string]*Policy, len(cfg.Clients))
	for _, cl := range cfg.Clients {
		var pol Policy
		if g, ok := groups[cl.Group]; ok {
			pol = *g
		} else {
			pol = Policy{Group: "default", Mode: ModeFilter}
		}
		pol.Blocked = cl.Blocked
		if pol.Blocked || pol.Group != "default" {
			p := pol
			table[cl.IP] = &p
		}
	}
	r.policies.Store(&table)
}

// scheduleActive reports whether now falls inside the schedule's window.
// Windows anchor on each allowed day at Start; an End at or before Start
// wraps past midnight into the next day, so both today's and yesterday's
// anchors are checked.
func scheduleActive(s *config.Schedule, now time.Time) bool {
	startMin, endMin := config.ParseHHMM(s.Start), config.ParseHHMM(s.End)
	if startMin < 0 || endMin < 0 {
		return false // unreachable on validated config
	}
	lengthMin := endMin - startMin
	if lengthMin <= 0 {
		lengthMin += 24 * 60
	}
	for _, dayOffset := range []int{0, -1} {
		anchor := now.AddDate(0, 0, dayOffset)
		if !config.DayAllowed(s.Days, anchor.Weekday()) {
			continue
		}
		start := time.Date(anchor.Year(), anchor.Month(), anchor.Day(),
			startMin/60, startMin%60, 0, 0, now.Location())
		end := start.Add(time.Duration(lengthMin) * time.Minute)
		if !now.Before(start) && now.Before(end) {
			return true
		}
	}
	return false
}

// hasSchedules reports whether any group needs clock-driven rebuilds.
func (r *Registry) hasSchedules() bool {
	cfg := r.cfg.Load()
	if cfg == nil {
		return false
	}
	for _, g := range cfg.Groups {
		if g.Schedule != nil {
			return true
		}
	}
	return false
}

// Seed pre-populates a device from persisted history (query log DB), so the
// device list survives restarts. Never overwrites live state.
func (r *Registry) Seed(ip string, total, blocked uint64, first, last time.Time) {
	d := &device{}
	d.firstSeen.Store(first.UnixNano())
	d.lastSeen.Store(last.UnixNano())
	d.total.Store(total)
	d.blocked.Store(blocked)
	if _, loaded := r.seen.LoadOrStore(ip, d); !loaded {
		select {
		case r.enrichCh <- ip:
		default:
		}
	}
}

// Devices returns the merged view: every IP that has queried, plus every
// configured client (even if never seen). Sorted by numeric IP address so
// the list is stable — rows don't reorder as live traffic updates last-seen
// times (192.168.1.9 sorts before 192.168.1.10, and v4 before v6).
func (r *Registry) Devices(cfg *config.Config) []Device {
	byIP := make(map[string]*Device)
	r.seen.Range(func(k, v any) bool {
		ip := k.(string)
		d := v.(*device)
		first := time.Unix(0, d.firstSeen.Load())
		last := time.Unix(0, d.lastSeen.Load())
		dev := &Device{
			IP:        ip,
			Group:     "default",
			Seen:      true,
			Queries:   d.total.Load(),
			QBlocked:  d.blocked.Load(),
			FirstSeen: &first,
			LastSeen:  &last,
		}
		if mac := d.mac.Load(); mac != nil {
			dev.MAC = *mac
		}
		if h := d.hostname.Load(); h != nil {
			dev.Hostname = *h
		}
		byIP[ip] = dev
		return true
	})
	for _, cl := range cfg.Clients {
		dev, ok := byIP[cl.IP]
		if !ok {
			dev = &Device{IP: cl.IP, Group: "default"}
			byIP[cl.IP] = dev
		}
		dev.Name = cl.Name
		dev.Blocked = cl.Blocked
		if cl.Group != "" {
			dev.Group = cl.Group
		}
		if cl.MAC != "" { // a user-specified MAC beats ARP
			dev.MAC = cl.MAC
		}
	}
	out := make([]Device, 0, len(byIP))
	for _, d := range byIP {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessIP(out[i].IP, out[j].IP)
	})
	return out
}

// lessIP orders two device keys by numeric IP address. Unparseable keys sort
// after valid ones (and among themselves by byte order), so a malformed key
// never panics or hides real devices.
func lessIP(a, b string) bool {
	ipA, errA := netip.ParseAddr(a)
	ipB, errB := netip.ParseAddr(b)
	switch {
	case errA != nil && errB != nil:
		return a < b
	case errA != nil:
		return false
	case errB != nil:
		return true
	default:
		return ipA.Compare(ipB) < 0
	}
}

// SeenCount reports how many distinct client IPs have queried.
func (r *Registry) SeenCount() int {
	n := 0
	r.seen.Range(func(_, _ any) bool { n++; return true })
	return n
}

// setMAC/setHostname are called by the enrichment worker only.
func (r *Registry) setMAC(ip, mac string) {
	if v, ok := r.seen.Load(ip); ok {
		v.(*device).mac.Store(&mac)
	}
}

func (r *Registry) setHostname(ip, name string) {
	if v, ok := r.seen.Load(ip); ok {
		v.(*device).hostname.Store(&name)
	}
}
