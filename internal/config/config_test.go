package config

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCreatesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minos.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default config not written: %v", err)
	}
	c := s.Get()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if c.QueryLog.RingSize != 10000 || c.Blocking.Mode != "zero_ip" {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minos.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(c *Config) error {
		c.Blocking.Denylist = []string{"bad.example"}
		c.Lists.RefreshInterval = Duration(6 * time.Hour)
		c.API.Token = "sekrit"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := s2.Get()
	if len(c.Blocking.Denylist) != 1 || c.Blocking.Denylist[0] != "bad.example" {
		t.Errorf("denylist = %v", c.Blocking.Denylist)
	}
	if c.Lists.RefreshInterval.Std() != 6*time.Hour {
		t.Errorf("refresh interval = %s", c.Lists.RefreshInterval.Std())
	}
	if c.API.Token != "sekrit" {
		t.Errorf("token = %q", c.API.Token)
	}
}

func TestUpdateRejectsInvalidAndKeepsRunningConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minos.yaml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Get()
	err = s.Update(func(c *Config) error {
		c.Blocking.Mode = "banish-to-tartarus" // not a real mode
		return nil
	})
	if err == nil {
		t.Fatal("invalid mode accepted")
	}
	if s.Get() != before {
		t.Error("running config changed after failed update")
	}
	// File on disk must still parse and hold the old value.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Get().Blocking.Mode != "zero_ip" {
		t.Errorf("disk config mode = %q, want zero_ip", s2.Get().Blocking.Mode)
	}
}

func TestOnChangeFires(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fired := 0
	s.OnChange(func(c *Config) { fired++ })
	if err := s.Update(func(c *Config) error { c.API.Token = "x"; return nil }); err != nil {
		t.Fatal(err)
	}
	if fired != 1 {
		t.Errorf("onChange fired %d times, want 1", fired)
	}
}

// TestDefaultUpstreamsAreBootstrapFree guards the circular-dependency fix: a
// DNS server must not need to resolve its own upstream's hostname before it
// can forward anything, so every default upstream must address an IP literal,
// never a domain name.
func TestDefaultUpstreamsAreBootstrapFree(t *testing.T) {
	ups := Default().DNS.Upstreams
	if len(ups) == 0 {
		t.Fatal("default config has no upstreams")
	}
	for _, u := range ups {
		var host string
		if u.Protocol == "doh" {
			parsed, err := url.Parse(u.Address)
			if err != nil {
				t.Errorf("default doh upstream %q: unparseable: %v", u.Address, err)
				continue
			}
			host = parsed.Hostname()
		} else {
			h, _, err := net.SplitHostPort(u.Address)
			if err != nil {
				t.Errorf("default upstream %q: not host:port: %v", u.Address, err)
				continue
			}
			host = h
		}
		if net.ParseIP(host) == nil {
			t.Errorf("default upstream %q resolves to hostname %q — a fresh "+
				"install can't resolve it before DNS is up; use an IP literal",
				u.Address, host)
		}
	}
}

func TestValidateCatchesBadValues(t *testing.T) {
	bad := []func(*Config){
		func(c *Config) { c.DNS.Listen = "no-port" },
		func(c *Config) { c.DNS.Upstreams = nil },
		func(c *Config) { c.DNS.Upstreams[0].Protocol = "carrier-pigeon" },
		func(c *Config) { c.DNS.Upstreams = []Upstream{{Address: "http://insecure", Protocol: "doh"}} },
		func(c *Config) { c.Blocking.Mode = "maim" },
		func(c *Config) { c.Lists.Sources[0].Format = "csv" },
		func(c *Config) { // allow-source names share the block-source namespace
			c.Lists.AllowSources = []ListSource{{
				Name: c.Lists.Sources[0].Name, URL: "https://example.com/allow.txt",
				Format: "plain", Enabled: true,
			}}
		},
		func(c *Config) { // allow sources are validated like block sources
			c.Lists.AllowSources = []ListSource{{Name: "bad", URL: "ftp://x", Format: "plain", Enabled: true}}
		},
		func(c *Config) { // auditing an allowlist is meaningless
			c.Lists.AllowSources = []ListSource{{Name: "aud", URL: "https://example.com/a.txt", Format: "plain", Enabled: true, Audit: true}}
		},
		func(c *Config) { c.Lists.Sources[0].URL = "ftp://old.example/list" },
		func(c *Config) { c.Lists.RefreshInterval = Duration(time.Second) },
		func(c *Config) { c.QueryLog.RingSize = 0 },
		func(c *Config) { c.QueryLog.RetentionDays = 0 },
		func(c *Config) { c.QueryLog.DBPath = "" },
		func(c *Config) { c.API.Listen = "" },
		func(c *Config) { c.UpdateInstallMethod = "snap" },
		func(c *Config) { c.Notifications.Digest = "hourly" },
		func(c *Config) { c.Notifications.DigestTime = "25:00" },
		func(c *Config) { c.Notifications.DigestTime = "9am" },
		func(c *Config) { c.Notifications.DigestDay = "Monday" }, // lowercase only
		func(c *Config) { c.Notifications.DigestDay = "someday" },
	}
	for i, mutate := range bad {
		c := Default()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: invalid config passed validation", i)
		}
	}
}

// Blocking the encrypted-dns service alongside a hostname doh/dot upstream
// it covers is a warning (the operator may resolve it elsewhere), never a
// validation error — and IP-literal upstreams are always quiet.
func TestEncryptedDNSUpstreamWarnings(t *testing.T) {
	base := func() *Config {
		c := Default()
		c.Blocking.Services = []string{"encrypted-dns"}
		return c
	}

	c := base()
	c.DNS.Upstreams = []Upstream{{Address: "https://dns.google/dns-query", Protocol: "doh"}}
	if w := c.encryptedDNSUpstreamWarnings(); len(w) != 1 {
		t.Errorf("hostname doh upstream: %d warnings, want 1: %v", len(w), w)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil — the overlap warns, never rejects", err)
	}

	// Subdomain of a covered name warns too (the block covers subdomains).
	c = base()
	c.DNS.Upstreams = []Upstream{{Address: "abcd12.dns.nextdns.io:853", Protocol: "dot"}}
	if w := c.encryptedDNSUpstreamWarnings(); len(w) != 1 {
		t.Errorf("dot subdomain upstream: %d warnings, want 1: %v", len(w), w)
	}

	// Group-level service blocks count: kids-group devices lose the name.
	c = base()
	c.Blocking.Services = nil
	c.Groups = []Group{{Name: "kids", Mode: "filter", Services: []string{"encrypted-dns"}}}
	c.DNS.Upstreams = []Upstream{{Address: "https://dns.quad9.net/dns-query", Protocol: "doh"}}
	if w := c.encryptedDNSUpstreamWarnings(); len(w) != 1 {
		t.Errorf("group service block: %d warnings, want 1: %v", len(w), w)
	}

	// Quiet cases: IP-literal upstreams (the shipped presets), an uncovered
	// hostname, and the service not being blocked at all.
	quiet := []*Config{base(), base(), Default()}
	quiet[0].DNS.Upstreams = []Upstream{{Address: "https://1.1.1.1/dns-query", Protocol: "doh"}}
	quiet[1].DNS.Upstreams = []Upstream{{Address: "https://doh.mydns.example/dns-query", Protocol: "doh"}}
	quiet[2].DNS.Upstreams = []Upstream{{Address: "https://dns.google/dns-query", Protocol: "doh"}}
	for i, qc := range quiet {
		if w := qc.encryptedDNSUpstreamWarnings(); len(w) != 0 {
			t.Errorf("quiet case %d: unexpected warnings %v", i, w)
		}
	}
}

// DigestSchedule applies defaults for empty fields and never chokes on a
// hand-edited file (validation catches bad values up front, but the
// scheduler must stay panic-free regardless).
func TestDigestScheduleDefaults(t *testing.T) {
	var n NotificationsConfig
	if h, m, d := n.DigestSchedule(); h != 9 || m != 0 || d != time.Monday {
		t.Errorf("defaults = %d:%02d %v, want 09:00 Monday", h, m, d)
	}
	n.DigestTime, n.DigestDay = "21:30", "friday"
	if h, m, d := n.DigestSchedule(); h != 21 || m != 30 || d != time.Friday {
		t.Errorf("custom = %d:%02d %v, want 21:30 Friday", h, m, d)
	}
	n.DigestTime, n.DigestDay = "junk", "junk"
	if h, m, d := n.DigestSchedule(); h != 9 || m != 0 || d != time.Monday {
		t.Errorf("garbage fallback = %d:%02d %v, want 09:00 Monday", h, m, d)
	}
}

// On disk, an unknown field is tolerated (so a config written by a newer
// Minos still loads after a downgrade); an uploaded restore stays strict.
func TestUnknownFieldToleratedOnDiskStrictOnRestore(t *testing.T) {
	yaml := "dns:\n  listen: \":53\"\n  fate: condemned\n"

	// On-disk load: tolerated, config still opens.
	path := filepath.Join(t.TempDir(), "minos.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open should tolerate an unknown on-disk field, got: %v", err)
	}
	if s.Get().DNS.Listen != ":53" {
		t.Errorf("known fields should still load: listen = %q", s.Get().DNS.Listen)
	}

	// Uploaded restore: rejected with the field name.
	if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "fate") {
		t.Errorf("Parse (restore) should reject the unknown field, got: %v", err)
	}
}

// Clone must deep-copy the service lists so an aborted Update can't leak
// mutations into the live config.
func TestCloneDeepCopiesServiceLists(t *testing.T) {
	c := Default()
	c.Blocking.Services = []string{"tiktok"}
	c.Blocking.AllowedServices = []string{"netflix"}
	c.Groups = []Group{{Name: "kids", Mode: "filter", AllowedServices: []string{"disneyplus"}}}
	out := c.Clone()
	out.Blocking.Services[0] = "changed"
	out.Blocking.AllowedServices[0] = "changed"
	out.Groups[0].AllowedServices[0] = "changed"
	if c.Blocking.Services[0] != "tiktok" ||
		c.Blocking.AllowedServices[0] != "netflix" ||
		c.Groups[0].AllowedServices[0] != "disneyplus" {
		t.Errorf("Clone shares slices with the original: %+v", c.Blocking)
	}
}

func TestValidateRejectsDuplicateClientMAC(t *testing.T) {
	c := Default()
	c.Clients = []Client{
		{IP: "192.168.1.10", MAC: "aa:bb:cc:dd:ee:ff"},
		{IP: "192.168.1.11", MAC: "AA-BB-CC-DD-EE-FF"}, // same device, other notation
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate MAC should fail validation, got: %v", err)
	}
	c.Clients[1].MAC = "aa:bb:cc:dd:ee:00"
	if err := c.Validate(); err != nil {
		t.Errorf("distinct MACs should validate: %v", err)
	}
}

// A hand-edited config with two entries for one MAC must not brick startup:
// the on-disk load demotes the later entry to IP-keyed (warning logged), while
// an uploaded restore rejects the duplicate outright.
func TestDuplicateClientMACHealedOnDiskStrictOnRestore(t *testing.T) {
	yaml := "clients:\n" +
		"  - ip: 192.168.1.10\n    mac: aa:bb:cc:dd:ee:ff\n    group: default\n" +
		"  - ip: 192.168.1.11\n    mac: AA-BB-CC-DD-EE-FF\n    name: stray\n"

	path := filepath.Join(t.TempDir(), "minos.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open should heal a duplicate client MAC, got: %v", err)
	}
	cls := s.Get().Clients
	if len(cls) != 2 || cls[0].MAC == "" || cls[1].MAC != "" {
		t.Errorf("want the first entry to keep the MAC and the second demoted to IP-keyed, got %+v", cls)
	}

	if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("Parse (restore) should reject the duplicate MAC, got: %v", err)
	}
}

// Overwriting an existing config leaves a .bak recovery point with the prior
// contents; the first-ever write (no file yet) creates no spurious backup.
func TestSaveBacksUpPriorConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minos.yaml")
	bak := path + ".bak"

	s, err := Open(path) // creates the default; nothing to back up yet
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("no backup expected on first write, stat err = %v", err)
	}

	if err := s.Update(func(c *Config) error { c.DNS.BlockTTL = 120; return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("expected a backup after overwrite: %v", err)
	}
	// The backup holds the pre-change config (default BlockTTL 60), not the new one.
	prior, err := Parse(data)
	if err != nil {
		t.Fatalf("backup should be a valid config: %v", err)
	}
	if prior.DNS.BlockTTL != 60 {
		t.Errorf("backup should hold the prior config (BlockTTL 60), got %d", prior.DNS.BlockTTL)
	}
}
