// Package clients tracks every device that queries the resolver and resolves
// the per-device policy (group membership, bypass, block).
//
// Hot-path discipline: Touch and PolicyFor are called per query, so they are
// a sync.Map access plus atomics and one atomic pointer load — no mutexes,
// no allocation on the steady state. Enrichment (ARP, reverse DNS) and policy
// table rebuilds happen off the hot path.
package clients

import (
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/oui"
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

// Device is the merged API view of one physical device: live traffic state
// (summed across every IP it has used) plus any configured assignment.
type Device struct {
	IP string `json:"ip"` // the primary (most recently active) IP
	// IPs lists every IP this device has used, primary included, so a
	// drill-down can span them all. Sorted numerically.
	IPs    []string `json:"ips,omitempty"`
	MAC    string   `json:"mac,omitempty"`
	Vendor string   `json:"vendor,omitempty"` // derived from MAC via the OUI table
	// PrivateMAC marks a locally-administered (randomized) MAC — the
	// per-network "private addresses" modern devices generate, which no
	// vendor registry can name.
	PrivateMAC bool       `json:"private_mac,omitempty"`
	Hostname   string     `json:"hostname,omitempty"`
	Name       string     `json:"name,omitempty"`
	Group      string     `json:"group"`
	Blocked    bool       `json:"blocked"`
	Seen       bool       `json:"seen"`
	Queries    uint64     `json:"queries"`
	QBlocked   uint64     `json:"queries_blocked"`
	FirstSeen  *time.Time `json:"first_seen,omitempty"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
}

// Registry is safe for concurrent use.
type Registry struct {
	seen     sync.Map // ip string → *device
	policies atomic.Pointer[map[string]*Policy]
	cfg      atomic.Pointer[config.Config] // snapshot for scheduled rebuilds
	enrichCh chan string                   // newly seen IPs awaiting enrichment
	// revResolvers is the ordered list of resolvers used for PTR enrichment
	// (gateway first, then system); built once when Run starts.
	revResolvers []*net.Resolver
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
	if mac != "" && r.macKnownElsewhere(ip, mac) {
		return // a known device on a new lease, not a new device
	}
	r.onNewDevice(ip, mac, hostname)
}

// macKnownElsewhere reports whether mac already identifies a device we know:
// a configured client is keyed on it, or another live IP carries it.
// Best-effort — just after a restart, Seeded entries have no MACs until ARP
// re-tags them, so the seen-based check can miss; the configured-MAC check
// and main's startup grace period cover that window.
func (r *Registry) macKnownElsewhere(ip, mac string) bool {
	if r.macConfigured(mac) {
		return true
	}
	for _, other := range r.IPsForMAC(mac) {
		if other != ip {
			return true
		}
	}
	return false
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
		if g.Mode == ModeFilter && (len(g.Allowlist) > 0 || len(g.Denylist) > 0 ||
			len(g.Services) > 0 || len(g.AllowedServices) > 0) {
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
			// Group-pardoned services: like group allowlist entries, an
			// overlay allow verdict short-circuits the global rules.
			for _, name := range g.AllowedServices {
				for _, d := range services.AllowDomains(name) {
					b.AddAllow("service:"+name, d)
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
		if !pol.Blocked && pol.Group == "default" {
			continue
		}
		p := pol
		// The hot-path table is IP-keyed; a MAC-keyed client resolves to every
		// live IP carrying that MAC (built off the hot path here), so its group
		// follows the device across DHCP leases.
		for _, ip := range r.ipsForClient(cl) {
			table[ip] = &p
		}
	}
	r.policies.Store(&table)
}

// ipsForClient lists the IPs a configured client resolves to: a MAC-less client
// is just its IP; a MAC-keyed client is every seen IP currently carrying that
// MAC, plus its last-known IP as a fallback so a returning device stays covered
// until ARP re-tags it. The fallback yields when the live entry at that IP
// carries a different MAC — the address was recycled to another device, which
// must not inherit this client's rules.
func (r *Registry) ipsForClient(cl config.Client) []string {
	if cl.MAC == "" {
		return []string{cl.IP}
	}
	want := NormalizeMAC(cl.MAC)
	var ips []string
	recycled := false
	r.seen.Range(func(k, v any) bool {
		m := v.(*device).mac.Load()
		if m == nil {
			return true
		}
		switch ip := k.(string); {
		case NormalizeMAC(*m) == want:
			if ip != cl.IP {
				ips = append(ips, ip)
			}
		case ip == cl.IP:
			recycled = true
		}
		return true
	})
	if !recycled {
		ips = append(ips, cl.IP)
	}
	return ips
}

// macConfigured reports whether any configured client is keyed on mac.
func (r *Registry) macConfigured(mac string) bool {
	cfg := r.cfg.Load()
	if cfg == nil {
		return false
	}
	want := NormalizeMAC(mac)
	for _, cl := range cfg.Clients {
		if cl.MAC != "" && NormalizeMAC(cl.MAC) == want {
			return true
		}
	}
	return false
}

// IPsForMAC returns every live IP currently carrying mac. Off the hot path.
func (r *Registry) IPsForMAC(mac string) []string {
	want := NormalizeMAC(mac)
	var ips []string
	r.seen.Range(func(k, v any) bool {
		if m := v.(*device).mac.Load(); m != nil && NormalizeMAC(*m) == want {
			ips = append(ips, k.(string))
		}
		return true
	})
	return ips
}

// CurrentIP returns the most recently active IP currently carrying mac, or ""
// if none is known. The API uses it to stamp a last-known IP onto a MAC-keyed
// assignment. Off the hot path.
func (r *Registry) CurrentIP(mac string) string {
	want := NormalizeMAC(mac)
	var bestIP string
	bestLast := int64(-1)
	r.seen.Range(func(k, v any) bool {
		d := v.(*device)
		if m := d.mac.Load(); m != nil && NormalizeMAC(*m) == want {
			if l := d.lastSeen.Load(); l > bestLast {
				bestLast, bestIP = l, k.(string)
			}
		}
		return true
	})
	return bestIP
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

// Devices returns the merged per-physical-device view. A device is identified
// by its MAC when one is known (from ARP or a user-set assignment), else by its
// IP; every IP a device has used folds into one row, with query counts summed
// and first/last-seen spanning them — so a device that power-cycled onto a new
// DHCP lease shows once, not once per address. Configured assignments (label,
// group, block) attach by MAC when the client carries one, else by IP. Rows are
// sorted by the numeric primary (most recently active) IP so the list is stable
// (192.168.1.9 before 192.168.1.10, v4 before v6).
func (r *Registry) Devices(cfg *config.Config) []Device {
	accs := make(map[string]*deviceAcc)

	r.seen.Range(func(k, v any) bool {
		ip := k.(string)
		d := v.(*device)
		mac := ""
		if m := d.mac.Load(); m != nil {
			mac = *m
		}
		host := ""
		if h := d.hostname.Load(); h != nil {
			host = *h
		}
		a := accFor(accs, deviceKey(ip, mac))
		a.addLive(ip, mac, host, d.total.Load(), d.blocked.Load(),
			d.firstSeen.Load(), d.lastSeen.Load())
		return true
	})

	// Overlay configured assignments, matching an existing device by MAC (or
	// by last-known IP when the MAC isn't in the ARP table yet) and creating a
	// standalone row for one never seen.
	for _, cl := range cfg.Clients {
		a := matchAcc(accs, cl)
		if a == nil {
			a = accFor(accs, deviceKey(cl.IP, cl.MAC))
		}
		a.applyConfig(cl)
	}

	out := make([]Device, 0, len(accs))
	for _, a := range accs {
		out = append(out, a.device())
	}
	sort.Slice(out, func(i, j int) bool {
		return lessIP(out[i].IP, out[j].IP)
	})
	return out
}

// deviceKey is the merge key: a device with a MAC groups by it (regardless of
// IP), one without groups by its IP.
func deviceKey(ip, mac string) string {
	if mac != "" {
		return "mac:" + NormalizeMAC(mac)
	}
	return "ip:" + ip
}

func accFor(accs map[string]*deviceAcc, key string) *deviceAcc {
	a := accs[key]
	if a == nil {
		a = &deviceAcc{group: "default", ipSet: map[string]bool{}}
		accs[key] = a
	}
	return a
}

// matchAcc finds the device a configured client applies to: by MAC first, then
// by any IP the device is known to have used. Returns nil if none exists yet.
func matchAcc(accs map[string]*deviceAcc, cl config.Client) *deviceAcc {
	if cl.MAC != "" {
		if a := accs["mac:"+NormalizeMAC(cl.MAC)]; a != nil {
			return a
		}
	}
	for _, a := range accs {
		if a.ipSet[cl.IP] {
			return a
		}
	}
	return nil
}

// deviceAcc folds one physical device's per-IP live state and its configured
// assignment into a single row.
type deviceAcc struct {
	ips         []string
	ipSet       map[string]bool
	mac         string
	primaryIP   string
	primaryLast int64 // unix nanos of the primary IP's last-seen
	primaryHost string
	total       uint64
	blocked     uint64
	first       int64 // unix nanos, min across IPs (0 = unset)
	last        int64 // unix nanos, max across IPs
	seen        bool
	name        string
	group       string
	blockedDev  bool
}

func (a *deviceAcc) addIP(ip string) {
	if !a.ipSet[ip] {
		a.ipSet[ip] = true
		a.ips = append(a.ips, ip)
	}
}

func (a *deviceAcc) addLive(ip, mac, host string, total, blocked uint64, first, last int64) {
	a.addIP(ip)
	a.seen = true
	if mac != "" {
		a.mac = mac
	}
	a.total += total
	a.blocked += blocked
	if first != 0 && (a.first == 0 || first < a.first) {
		a.first = first
	}
	if last > a.last {
		a.last = last
	}
	if a.primaryIP == "" || last > a.primaryLast {
		a.primaryIP, a.primaryLast = ip, last
		if host != "" {
			a.primaryHost = host
		}
	}
	if a.primaryHost == "" && host != "" {
		a.primaryHost = host
	}
}

func (a *deviceAcc) applyConfig(cl config.Client) {
	a.name = cl.Name
	a.blockedDev = cl.Blocked
	if cl.Group != "" {
		a.group = cl.Group
	}
	if cl.MAC != "" { // a user-set MAC beats ARP; display the canonical form
		a.mac = NormalizeMAC(cl.MAC)
	}
	a.addIP(cl.IP) // record the last-known IP even if never seen live
	if a.primaryIP == "" {
		a.primaryIP = cl.IP
	}
}

func (a *deviceAcc) device() Device {
	sort.Slice(a.ips, func(i, j int) bool { return lessIP(a.ips[i], a.ips[j]) })
	group := a.group
	if group == "" {
		group = "default"
	}
	d := Device{
		IP:       a.primaryIP,
		IPs:      a.ips,
		MAC:      a.mac,
		Hostname: a.primaryHost,
		Name:     a.name,
		Group:    group,
		Blocked:  a.blockedDev,
		Seen:     a.seen,
		Queries:  a.total,
		QBlocked: a.blocked,
	}
	if a.mac != "" {
		d.Vendor = oui.Vendor(a.mac) // "" when no registry prefix covers it
		// A randomized "private address" can never match a registry — the
		// blank vendor cell is itself information, so say so.
		d.PrivateMAC = oui.IsLocallyAdministered(a.mac)
	}
	if a.first != 0 {
		t := time.Unix(0, a.first)
		d.FirstSeen = &t
	}
	if a.last != 0 {
		t := time.Unix(0, a.last)
		d.LastSeen = &t
	}
	return d
}

// NormalizeMAC canonicalises a MAC to lowercase colon form so ARP-derived and
// user-entered addresses compare equal regardless of separator or case.
func NormalizeMAC(s string) string {
	if ha, err := net.ParseMAC(s); err == nil {
		return ha.String()
	}
	return strings.ToLower(strings.TrimSpace(s))
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

// setMAC/setHostname are called by the enrichment worker only (a single
// goroutine — the rebuild below never races the schedule ticker's).
func (r *Registry) setMAC(ip, mac string) {
	v, ok := r.seen.Load(ip)
	if !ok {
		return
	}
	d := v.(*device)
	prev := d.mac.Load()
	if prev != nil && *prev == mac {
		return // unchanged: no policy consequence
	}
	d.mac.Store(&mac)
	// A group/block keyed on this MAC must take effect on this IP promptly:
	// the hot-path table is IP-keyed and built from MAC assignments, so a
	// freshly learned association needs a rebuild. Beyond a newly configured
	// MAC, a rebuild is also due when the association contradicts one — the
	// IP moved away from a configured MAC, or a configured client's last-known
	// IP now carries another device's MAC (a recycled lease must not inherit
	// that client's rules — see ipsForClient).
	if r.macConfigured(mac) ||
		(prev != nil && r.macConfigured(*prev)) ||
		r.lastKnownIPConfigured(ip) {
		r.rebuildPolicies(time.Now())
	}
}

// lastKnownIPConfigured reports whether ip is the persisted last-known IP of a
// MAC-keyed client. Learning a MAC on such an IP can start or stop the
// client's fallback coverage of it, so the policy table needs a rebuild.
func (r *Registry) lastKnownIPConfigured(ip string) bool {
	cfg := r.cfg.Load()
	if cfg == nil {
		return false
	}
	for _, cl := range cfg.Clients {
		if cl.MAC != "" && cl.IP == ip {
			return true
		}
	}
	return false
}

func (r *Registry) setHostname(ip, name string) {
	if v, ok := r.seen.Load(ip); ok {
		v.(*device).hostname.Store(&name)
	}
}
