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
	BlockTTL uint32 `yaml:"block_ttl"`
}

type BlockingConfig struct {
	// Mode is "zero_ip" (respond 0.0.0.0 / ::) or "nxdomain".
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
	Denylist  []string `yaml:"denylist"`
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
	out.Lists.Sources = append([]ListSource(nil), c.Lists.Sources...)
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
		switch u.Protocol {
		case "udp", "tcp", "dot":
			if err := validateHostPort(u.Address); err != nil {
				return fmt.Errorf("dns.upstreams[%d].address: %w", i, err)
			}
		case "doh":
			parsed, err := url.Parse(u.Address)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return fmt.Errorf("dns.upstreams[%d].address: doh requires an https URL, got %q", i, u.Address)
			}
		default:
			return fmt.Errorf("dns.upstreams[%d].protocol: must be udp, tcp, dot, or doh, got %q", i, u.Protocol)
		}
	}
	switch c.Blocking.Mode {
	case "zero_ip", "nxdomain":
	default:
		return fmt.Errorf("blocking.mode: must be zero_ip or nxdomain, got %q", c.Blocking.Mode)
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
