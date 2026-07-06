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
		host := u.Address
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
		func(c *Config) { c.Lists.Sources[0].URL = "ftp://old.example/list" },
		func(c *Config) { c.Lists.RefreshInterval = Duration(time.Second) },
		func(c *Config) { c.QueryLog.RingSize = 0 },
		func(c *Config) { c.QueryLog.RetentionDays = 0 },
		func(c *Config) { c.QueryLog.DBPath = "" },
		func(c *Config) { c.API.Listen = "" },
	}
	for i, mutate := range bad {
		c := Default()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: invalid config passed validation", i)
		}
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minos.yaml")
	yaml := "dns:\n  listen: \":53\"\n  fate: condemned\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "fate") {
		t.Errorf("unknown field should be rejected with its name, got: %v", err)
	}
}
