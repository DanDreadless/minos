// Package config loads, validates, and persists the Minos configuration.
//
// A Store holds an immutable snapshot behind an atomic pointer; updates
// clone-validate-persist-swap so readers never see a partially applied
// config and the process never needs a restart for a settings change.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"minos/internal/services"
)

// Duration wraps time.Duration so YAML can use human strings ("5m", "24h").
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Upstream is a single upstream resolver.
type Upstream struct {
	// Address is host:port for udp/tcp/dot, or a full URL for doh.
	Address string `yaml:"address" json:"address"`
	// Protocol is one of: udp, tcp, dot, doh.
	Protocol string `yaml:"protocol" json:"protocol"`
}

type DNSConfig struct {
	Listen    string     `yaml:"listen"`
	Upstreams []Upstream `yaml:"upstreams"`
	// BlockTTL is the TTL (seconds) on synthesized blocked responses.
	BlockTTL uint32      `yaml:"block_ttl"`
	Cache    CacheConfig `yaml:"cache"`
	// LocalRecords are names Minos answers itself, never forwarding them
	// upstream. LocalTTL is the TTL (seconds) on those answers.
	LocalRecords []LocalRecord `yaml:"local_records,omitempty"`
	LocalTTL     uint32        `yaml:"local_ttl"`
	// Routes send matching domains (and their subdomains) to a specific
	// upstream instead of the default ones — conditional forwarding.
	Routes []Route `yaml:"routes,omitempty"`
}

// Route is one conditional-forwarding rule. A route is authoritative for
// its domains: if its upstream fails, the query fails (no fallback to the
// default upstreams).
type Route struct {
	Domains  []string `yaml:"domains" json:"domains"`
	Upstream Upstream `yaml:"upstream" json:"upstream"`
}

// LocalRecord is one locally-answered DNS name: address records, or a CNAME
// alias (mutually exclusive). A leading "*." makes it a wildcard that matches
// subdomains at any depth (but not the bare parent name).
type LocalRecord struct {
	Name  string   `yaml:"name" json:"name"`
	A     []string `yaml:"a,omitempty" json:"a,omitempty"`
	AAAA  []string `yaml:"aaaa,omitempty" json:"aaaa,omitempty"`
	CNAME string   `yaml:"cname,omitempty" json:"cname,omitempty"`
}

// CacheConfig bounds the in-memory DNS response cache. Any config change
// flushes the cache (safe: it repopulates within seconds).
type CacheConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// MaxEntries caps memory; ~500 B per entry.
	MaxEntries int `yaml:"max_entries" json:"max_entries"`
	// MinTTL/MaxTTL clamp (seconds) how long an answer may be served
	// from cache, regardless of the record's own TTL.
	MinTTL uint32 `yaml:"min_ttl" json:"min_ttl"`
	MaxTTL uint32 `yaml:"max_ttl" json:"max_ttl"`
}

type BlockingConfig struct {
	// Mode is "zero_ip" (respond 0.0.0.0 / ::) or "nxdomain".
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
	Denylist  []string `yaml:"denylist"`
	// Services are catalog names (see internal/services) blocked for
	// everyone; groups can block more for their members only.
	Services []string `yaml:"services,omitempty"`
}

// Group is a named device policy. Devices not assigned to a group get the
// default behavior: the full blocklist rules.
type Group struct {
	Name string `yaml:"name" json:"name"`
	// Mode is "filter" (default rules plus this group's extra lists),
	// "bypass" (no filtering at all), or "block" (refuse all DNS).
	Mode string `yaml:"mode" json:"mode"`
	// Extra domains for filter-mode groups, layered over the global rules.
	// Group allowlist entries beat global denies; group denylist entries
	// add blocks for members only.
	Allowlist []string `yaml:"allowlist,omitempty" json:"allowlist"`
	Denylist  []string `yaml:"denylist,omitempty" json:"denylist"`
	// Services are catalog names blocked for this group's members
	// (filter mode only).
	Services []string `yaml:"services,omitempty" json:"services"`
	// Schedule, when set, activates this group only inside the window;
	// outside it members follow the default rules. Server-local time.
	Schedule *Schedule `yaml:"schedule,omitempty" json:"schedule,omitempty"`
}

// Schedule is a weekly time window. End may be earlier than Start, in which
// case the window wraps past midnight ("21:00"–"07:00" starting on each
// listed day).
type Schedule struct {
	// Days are three-letter weekday names (mon..sun); empty means every day.
	Days  []string `yaml:"days,omitempty" json:"days,omitempty"`
	Start string   `yaml:"start" json:"start"` // "HH:MM"
	End   string   `yaml:"end" json:"end"`     // "HH:MM"
}

// weekdayNames maps config day names to time.Weekday.
var weekdayNames = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
	"sun": time.Sunday,
}

// ParseHHMM returns minutes since midnight, or -1 if s is not "HH:MM".
func ParseHHMM(s string) int {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return -1
	}
	return t.Hour()*60 + t.Minute()
}

// DayAllowed reports whether d is in days (empty days = every day).
func DayAllowed(days []string, d time.Weekday) bool {
	if len(days) == 0 {
		return true
	}
	for _, name := range days {
		if weekdayNames[name] == d {
			return true
		}
	}
	return false
}

// Client is a device assignment, keyed by IP. MAC and Name are labels the
// user (or ARP enrichment) attaches; matching is by source IP.
type Client struct {
	IP   string `yaml:"ip" json:"ip"`
	MAC  string `yaml:"mac,omitempty" json:"mac,omitempty"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Group is the assigned group name; empty means the default rules.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
	// Blocked refuses all DNS from this device, overriding any group.
	Blocked bool `yaml:"blocked,omitempty" json:"blocked"`
}

// ListSource is one remote blocklist subscription.
type ListSource struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Format  string `yaml:"format"` // hosts, plain, adblock
	Enabled bool   `yaml:"enabled"`
}

type ListsConfig struct {
	Sources         []ListSource `yaml:"sources"`
	RefreshInterval Duration     `yaml:"refresh_interval"`
}

type QueryLogConfig struct {
	// Ephemeral disables all disk logging; the ring buffer still feeds the UI.
	Ephemeral     bool   `yaml:"ephemeral"`
	DBPath        string `yaml:"db_path"`
	RingSize      int    `yaml:"ring_size"`
	RetentionDays int    `yaml:"retention_days"`
}

type APIConfig struct {
	Listen string `yaml:"listen"`
	Token  string `yaml:"token"`
}

type Config struct {
	DNS      DNSConfig      `yaml:"dns"`
	Blocking BlockingConfig `yaml:"blocking"`
	Groups   []Group        `yaml:"groups,omitempty"`
	Clients  []Client       `yaml:"clients,omitempty"`
	Lists    ListsConfig    `yaml:"lists"`
	QueryLog QueryLogConfig `yaml:"querylog"`
	API      APIConfig      `yaml:"api"`
}

// Default returns the configuration used when no file exists yet.
func Default() *Config {
	return &Config{
		DNS: DNSConfig{
			Listen: ":53",
			Upstreams: []Upstream{
				{Address: "https://cloudflare-dns.com/dns-query", Protocol: "doh"},
				{Address: "1.1.1.1:853", Protocol: "dot"},
			},
			BlockTTL: 60,
			Cache: CacheConfig{
				Enabled:    true,
				MaxEntries: 10000,
				MinTTL:     10,
				MaxTTL:     3600,
			},
			LocalTTL: 300,
		},
		Blocking: BlockingConfig{Mode: "zero_ip"},
		Lists: ListsConfig{
			Sources: []ListSource{
				{
					Name:    "StevenBlack",
					URL:     "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
					Format:  "hosts",
					Enabled: true,
				},
			},
			RefreshInterval: Duration(24 * time.Hour),
		},
		QueryLog: QueryLogConfig{
			DBPath:        "minos.db",
			RingSize:      10000,
			RetentionDays: 90,
		},
		API: APIConfig{Listen: "0.0.0.0:8080"},
	}
}

func (c *Config) Clone() *Config {
	out := *c
	out.DNS.Upstreams = append([]Upstream(nil), c.DNS.Upstreams...)
	out.Blocking.Allowlist = append([]string(nil), c.Blocking.Allowlist...)
	out.Blocking.Denylist = append([]string(nil), c.Blocking.Denylist...)
	out.Blocking.Services = append([]string(nil), c.Blocking.Services...)
	out.Lists.Sources = append([]ListSource(nil), c.Lists.Sources...)
	out.Groups = make([]Group, len(c.Groups))
	for i, g := range c.Groups {
		out.Groups[i] = g
		out.Groups[i].Allowlist = append([]string(nil), g.Allowlist...)
		out.Groups[i].Denylist = append([]string(nil), g.Denylist...)
		out.Groups[i].Services = append([]string(nil), g.Services...)
		if g.Schedule != nil {
			sch := *g.Schedule
			sch.Days = append([]string(nil), g.Schedule.Days...)
			out.Groups[i].Schedule = &sch
		}
	}
	out.Clients = append([]Client(nil), c.Clients...)
	out.DNS.LocalRecords = make([]LocalRecord, len(c.DNS.LocalRecords))
	for i, r := range c.DNS.LocalRecords {
		out.DNS.LocalRecords[i] = r
		out.DNS.LocalRecords[i].A = append([]string(nil), r.A...)
		out.DNS.LocalRecords[i].AAAA = append([]string(nil), r.AAAA...)
	}
	out.DNS.Routes = make([]Route, len(c.DNS.Routes))
	for i, r := range c.DNS.Routes {
		out.DNS.Routes[i] = r
		out.DNS.Routes[i].Domains = append([]string(nil), r.Domains...)
	}
	return &out
}

// Validate checks the whole config and returns the first problem found.
func (c *Config) Validate() error {
	if err := validateHostPort(c.DNS.Listen); err != nil {
		return fmt.Errorf("dns.listen: %w", err)
	}
	if len(c.DNS.Upstreams) == 0 {
		return fmt.Errorf("dns.upstreams: at least one upstream is required")
	}
	for i, u := range c.DNS.Upstreams {
		if err := validateUpstream(u); err != nil {
			return fmt.Errorf("dns.upstreams[%d]: %w", i, err)
		}
	}
	routed := make(map[string]bool)
	for i, r := range c.DNS.Routes {
		if len(r.Domains) == 0 {
			return fmt.Errorf("dns.routes[%d].domains: must not be empty", i)
		}
		for _, d := range r.Domains {
			if !validDomain(d) {
				return fmt.Errorf("dns.routes[%d].domains: %q is not a valid domain", i, d)
			}
			if routed[d] {
				return fmt.Errorf("dns.routes[%d].domains: %q appears in more than one route", i, d)
			}
			routed[d] = true
		}
		if err := validateUpstream(r.Upstream); err != nil {
			return fmt.Errorf("dns.routes[%d].upstream: %w", i, err)
		}
	}
	if c.DNS.Cache.Enabled {
		if c.DNS.Cache.MaxEntries <= 0 {
			return fmt.Errorf("dns.cache.max_entries: must be positive, got %d", c.DNS.Cache.MaxEntries)
		}
		if c.DNS.Cache.MaxTTL == 0 {
			return fmt.Errorf("dns.cache.max_ttl: must be at least 1 second")
		}
		if c.DNS.Cache.MinTTL > c.DNS.Cache.MaxTTL {
			return fmt.Errorf("dns.cache.min_ttl: %d exceeds max_ttl %d",
				c.DNS.Cache.MinTTL, c.DNS.Cache.MaxTTL)
		}
	}
	if len(c.DNS.LocalRecords) > 0 && c.DNS.LocalTTL == 0 {
		return fmt.Errorf("dns.local_ttl: must be at least 1 second")
	}
	localNames := make(map[string]bool, len(c.DNS.LocalRecords))
	for i, r := range c.DNS.LocalRecords {
		name := strings.TrimPrefix(r.Name, "*.")
		if !validDomain(name) {
			return fmt.Errorf("dns.local_records[%d].name: %q is not a valid domain", i, r.Name)
		}
		if localNames[r.Name] {
			return fmt.Errorf("dns.local_records[%d].name: duplicate record %q", i, r.Name)
		}
		localNames[r.Name] = true
		if r.CNAME != "" && (len(r.A) > 0 || len(r.AAAA) > 0) {
			return fmt.Errorf("dns.local_records[%d]: cname and address records are mutually exclusive", i)
		}
		if r.CNAME == "" && len(r.A) == 0 && len(r.AAAA) == 0 {
			return fmt.Errorf("dns.local_records[%d]: needs at least one of a, aaaa, or cname", i)
		}
		if r.CNAME != "" && !validDomain(r.CNAME) {
			return fmt.Errorf("dns.local_records[%d].cname: %q is not a valid domain", i, r.CNAME)
		}
		for _, a := range r.A {
			ip := net.ParseIP(a)
			if ip == nil || ip.To4() == nil {
				return fmt.Errorf("dns.local_records[%d].a: %q is not a valid IPv4 address", i, a)
			}
		}
		for _, a := range r.AAAA {
			ip := net.ParseIP(a)
			if ip == nil || ip.To4() != nil {
				return fmt.Errorf("dns.local_records[%d].aaaa: %q is not a valid IPv6 address", i, a)
			}
		}
	}
	switch c.Blocking.Mode {
	case "zero_ip", "nxdomain":
	default:
		return fmt.Errorf("blocking.mode: must be zero_ip or nxdomain, got %q", c.Blocking.Mode)
	}
	for i, s := range c.Blocking.Services {
		if !services.Exists(s) {
			return fmt.Errorf("blocking.services[%d]: unknown service %q", i, s)
		}
	}
	groupNames := make(map[string]bool, len(c.Groups))
	for i, g := range c.Groups {
		if g.Name == "" {
			return fmt.Errorf("groups[%d].name: must not be empty", i)
		}
		if g.Name == "default" {
			return fmt.Errorf("groups[%d].name: %q is reserved for unassigned devices", i, g.Name)
		}
		if groupNames[g.Name] {
			return fmt.Errorf("groups[%d].name: duplicate group %q", i, g.Name)
		}
		groupNames[g.Name] = true
		switch g.Mode {
		case "filter", "bypass", "block":
		default:
			return fmt.Errorf("groups[%d].mode: must be filter, bypass, or block, got %q", i, g.Mode)
		}
		for j, s := range g.Services {
			if !services.Exists(s) {
				return fmt.Errorf("groups[%d].services[%d]: unknown service %q", i, j, s)
			}
		}
		if sch := g.Schedule; sch != nil {
			for j, d := range sch.Days {
				if _, ok := weekdayNames[d]; !ok {
					return fmt.Errorf("groups[%d].schedule.days[%d]: %q is not one of mon..sun", i, j, d)
				}
			}
			start, end := ParseHHMM(sch.Start), ParseHHMM(sch.End)
			if start < 0 {
				return fmt.Errorf("groups[%d].schedule.start: %q is not HH:MM", i, sch.Start)
			}
			if end < 0 {
				return fmt.Errorf("groups[%d].schedule.end: %q is not HH:MM", i, sch.End)
			}
			if start == end {
				return fmt.Errorf("groups[%d].schedule: start and end are both %q — an always-on group needs no schedule", i, sch.Start)
			}
		}
	}
	clientIPs := make(map[string]bool, len(c.Clients))
	for i, cl := range c.Clients {
		if net.ParseIP(cl.IP) == nil {
			return fmt.Errorf("clients[%d].ip: %q is not a valid IP address", i, cl.IP)
		}
		if clientIPs[cl.IP] {
			return fmt.Errorf("clients[%d].ip: duplicate client %q", i, cl.IP)
		}
		clientIPs[cl.IP] = true
		if cl.MAC != "" {
			if _, err := net.ParseMAC(cl.MAC); err != nil {
				return fmt.Errorf("clients[%d].mac: %q is not a valid MAC address", i, cl.MAC)
			}
		}
		if cl.Group != "" && cl.Group != "default" && !groupNames[cl.Group] {
			return fmt.Errorf("clients[%d].group: no group named %q", i, cl.Group)
		}
	}
	for i, s := range c.Lists.Sources {
		if s.Name == "" {
			return fmt.Errorf("lists.sources[%d].name: must not be empty", i)
		}
		parsed, err := url.Parse(s.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("lists.sources[%d].url: must be an http(s) URL, got %q", i, s.URL)
		}
		switch s.Format {
		case "hosts", "plain", "adblock":
		default:
			return fmt.Errorf("lists.sources[%d].format: must be hosts, plain, or adblock, got %q", i, s.Format)
		}
	}
	if c.Lists.RefreshInterval.Std() < 5*time.Minute {
		return fmt.Errorf("lists.refresh_interval: must be at least 5m, got %s", c.Lists.RefreshInterval.Std())
	}
	if c.QueryLog.RingSize <= 0 {
		return fmt.Errorf("querylog.ring_size: must be positive, got %d", c.QueryLog.RingSize)
	}
	if c.QueryLog.RetentionDays < 1 {
		return fmt.Errorf("querylog.retention_days: must be at least 1, got %d", c.QueryLog.RetentionDays)
	}
	if !c.QueryLog.Ephemeral && c.QueryLog.DBPath == "" {
		return fmt.Errorf("querylog.db_path: required unless querylog.ephemeral is true")
	}
	if err := validateHostPort(c.API.Listen); err != nil {
		return fmt.Errorf("api.listen: %w", err)
	}
	return nil
}

func validateUpstream(u Upstream) error {
	switch u.Protocol {
	case "udp", "tcp", "dot":
		if err := validateHostPort(u.Address); err != nil {
			return fmt.Errorf("address: %w", err)
		}
	case "doh":
		parsed, err := url.Parse(u.Address)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("address: doh requires an https URL, got %q", u.Address)
		}
	default:
		return fmt.Errorf("protocol: must be udp, tcp, dot, or doh, got %q", u.Protocol)
	}
	return nil
}

// validDomain reports whether s looks like a DNS name: labels of 1-63 bytes
// from [A-Za-z0-9_-], at most 253 bytes total. (config cannot import
// internal/filter — that would be an import cycle — so the check lives here.)
func validDomain(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	labelLen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_':
			labelLen++
		case c == '.':
			if labelLen == 0 {
				return false
			}
			labelLen = 0
		default:
			return false
		}
		if labelLen > 63 {
			return false
		}
	}
	return labelLen > 0
}

func validateHostPort(addr string) error {
	if addr == "" {
		return fmt.Errorf("must not be empty")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	return nil
}

// save writes the config atomically: temp file in the same directory, then rename.
func save(path string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".minos-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Start from defaults so new fields get sane values on old config files.
	c := Default()
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return c, nil
}
