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
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"minos/internal/mqtt"
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
	// TLS serves encrypted DNS to clients (DoT/DoH). Like the plain
	// listen addresses, these settings are file-only: changing them
	// requires a restart.
	TLS TLSListeners `yaml:"tls,omitempty"`
	// ForwardPrivateReverse disables the RFC 6303 default of answering
	// private reverse zones (192.168.x.x PTR and friends) locally with
	// NXDOMAIN. Only useful when the default upstreams are internal
	// resolvers that know those zones; routes are the finer-grained tool.
	ForwardPrivateReverse bool `yaml:"forward_private_reverse,omitempty"`
	// AllowFirefoxDoH disables the default interception of Firefox's DoH
	// canary (use-application-dns.net): normally Minos answers it NXDOMAIN —
	// Mozilla's documented signal that this network filters DNS — so Firefox
	// keeps using the system resolver instead of its built-in DoH.
	AllowFirefoxDoH bool `yaml:"allow_firefox_doh,omitempty"`
	// RebindProtection refuses answers that point a public name at an address
	// inside the network (RFC 1918, ULA, loopback, link-local) — the DNS
	// rebinding attack. Opt-in: allowed domains, local records, routes and
	// private suffixes (.lan, .home.arpa, …) are exempt. Applies live.
	RebindProtection bool `yaml:"rebind_protection,omitempty"`
	// AllowedNetworks are the client networks Minos answers, as CIDRs or
	// single addresses. Empty (the default) means private and local
	// networks only — RFC 1918, CGNAT, loopback, link-local, ULA — so an
	// accidentally exposed port never makes Minos an open resolver. Add
	// 0.0.0.0/0 and ::/0 to answer everyone. Applies live.
	AllowedNetworks []string `yaml:"allowed_networks,omitempty"`
	// RateLimit is the most queries one device may send per minute (default
	// 1000, as Pi-hole); further queries that minute are refused. 0 turns it
	// off. No omitempty, so 0 is written and survives a restart.
	RateLimit int `yaml:"rate_limit"`
	// DNSSEC controls local validation of forwarded answers against the
	// DNSSEC chain of trust: "" or "off" (default), "permissive"
	// (validate, count, and log — never blocks), or "enforce" (bogus
	// answers are refused with SERVFAIL; answers that cannot be judged
	// still pass, so a non-DNSSEC upstream degrades visibility, never
	// resolution). Applies live.
	DNSSEC string `yaml:"dnssec,omitempty"`
}

// TLSListeners configures client-facing encrypted DNS. Both listeners are
// optional; enabling either requires a certificate whose hostname the
// clients will validate (Android Private DNS is hostname-based) — either
// files you provide, or ACME automation.
type TLSListeners struct {
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`
	// DoTListen serves DNS-over-TLS (usually ":853"); empty disables.
	DoTListen string `yaml:"dot_listen,omitempty"`
	// DoHListen serves DNS-over-HTTPS at /dns-query (usually ":443");
	// empty disables. HTTP/3 (DoH3) is served on the same port over UDP
	// and advertised with Alt-Svc, so clients that speak it upgrade.
	DoHListen string `yaml:"doh_listen,omitempty"`
	// DoQListen serves DNS-over-QUIC (RFC 9250, usually ":853" — UDP, so
	// it shares the number with DoT's TCP port); empty disables.
	DoQListen string `yaml:"doq_listen,omitempty"`
	// ACME obtains and renews the certificate automatically (mutually
	// exclusive with CertFile/KeyFile). File-only like the listeners.
	ACME *ACMEConfig `yaml:"acme,omitempty"`
}

// ACMEConfig drives automatic certificate issuance via the DNS-01
// challenge — the only ACME challenge a LAN-only host can complete.
type ACMEConfig struct {
	Email  string `yaml:"email"`
	Domain string `yaml:"domain"`
	// Provider fulfils the DNS-01 TXT record:
	// cloudflare | desec | duckdns | rfc2136.
	Provider string `yaml:"provider"`
	// APIToken authenticates cloudflare/desec/duckdns.
	APIToken string `yaml:"api_token,omitempty"`
	// RFC 2136 (nsupdate) settings.
	Server        string `yaml:"server,omitempty"`
	TSIGName      string `yaml:"tsig_name,omitempty"`
	TSIGSecret    string `yaml:"tsig_secret,omitempty"`
	TSIGAlgorithm string `yaml:"tsig_algorithm,omitempty"` // default hmac-sha256
	// DirectoryURL overrides the CA (default: Let's Encrypt production;
	// point at the staging directory while testing).
	DirectoryURL string `yaml:"directory_url,omitempty"`
	// CacheDir holds the account key and issued certificate
	// (default: acme/ next to the config file).
	CacheDir string `yaml:"cache_dir,omitempty"`
}

// Enabled reports whether any encrypted listener is configured.
func (t TLSListeners) Enabled() bool {
	return t.DoTListen != "" || t.DoHListen != "" || t.DoQListen != ""
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
	// ServeStale (RFC 8767) answers from an expired entry for up to six
	// hours while a background refresh runs — upstream blips go unnoticed.
	ServeStale bool `yaml:"serve_stale" json:"serve_stale"`
}

// TemporaryAllow is one allowed domain with an expiry.
type TemporaryAllow struct {
	Domain string    `yaml:"domain" json:"domain"`
	Until  time.Time `yaml:"until" json:"until"`
}

// MaxTemporaryAllow is the longest a temporary allow may last.
const MaxTemporaryAllow = 7 * 24 * time.Hour

// ActiveTemporaryAllows returns the temporary allows that have not expired.
func (b BlockingConfig) ActiveTemporaryAllows(now time.Time) []TemporaryAllow {
	var out []TemporaryAllow
	for _, t := range b.TemporaryAllows {
		if t.Until.After(now) {
			out = append(out, t)
		}
	}
	return out
}

type BlockingConfig struct {
	// Mode is "zero_ip" (respond 0.0.0.0 / ::) or "nxdomain".
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
	Denylist  []string `yaml:"denylist"`
	// TemporaryAllows are allowed domains that expire ("Allow for 1 hour"),
	// compiled as the temporary-allow list until Until passes and then
	// removed. A separate key, so an older Minos that does not know it simply
	// blocks them again — the safe direction.
	TemporaryAllows []TemporaryAllow `yaml:"temporary_allows,omitempty"`
	// Services are catalog names (see internal/services) blocked for
	// everyone; groups can block more for their members only.
	Services []string `yaml:"services,omitempty"`
	// AllowedServices are catalog names pardoned for everyone: every domain
	// the service needs (including its playback CDN hosts) is always
	// allowed. A service both blocked and allowed ends up allowed — allow
	// wins at every label depth in the matcher.
	AllowedServices []string `yaml:"allowed_services,omitempty"`
	// CustomServices are user-defined service bundles. Their global
	// block/allow toggles live inside each definition, and group selections
	// live in the groups' own custom keys — never in Services or
	// AllowedServices above, whose names an older binary validates against
	// its compiled-in catalog. Keeping customs out of those keys is what
	// lets a downgrade boot (the unknown keys are dropped whole).
	CustomServices []services.Custom `yaml:"custom_services,omitempty"`
	// SafeSearch rewrites search engines (and YouTube) to their
	// enforced-safe variants for every device.
	SafeSearch bool `yaml:"safe_search,omitempty"`
	// BlockICloudPrivateRelay denies the mask.icloud.com hostnames Apple
	// documents for exactly this purpose: devices fall back to normal DNS
	// (and show "Private Relay is unavailable on this network"), so Minos
	// can judge their queries. Opt-in — it is a real privacy trade.
	BlockICloudPrivateRelay bool `yaml:"block_icloud_private_relay,omitempty"`
	// CNAMEInspection judges every CNAME target in a forwarded answer like
	// the name itself, refusing the answer when one is blocked — trackers
	// hidden behind a first-party name ("CNAME cloaking"). On by default;
	// no omitempty, so turning it off is written and survives a restart.
	CNAMEInspection bool `yaml:"cname_inspection"`
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
	// AllowedServices are catalog names pardoned for this group's members
	// (filter mode only); like group allowlist entries, they beat global
	// denies.
	AllowedServices []string `yaml:"allowed_services,omitempty" json:"allowed_services"`
	// CustomServices / AllowedCustomServices name blocking.custom_services
	// definitions blocked or pardoned for this group's members (filter mode
	// only). Deliberately separate keys from Services/AllowedServices: an
	// older binary validates those against its compiled-in catalog, so a
	// custom name there would brick a downgrade's boot; an unknown key is
	// merely dropped.
	CustomServices        []string `yaml:"custom_services,omitempty" json:"custom_services"`
	AllowedCustomServices []string `yaml:"allowed_custom_services,omitempty" json:"allowed_custom_services"`
	// SafeSearch enforces safe search for this group's members
	// (filter mode only; global blocking.safe_search covers everyone).
	SafeSearch bool `yaml:"safe_search,omitempty" json:"safe_search"`
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

// Client is a device assignment. When MAC is set the assignment follows the
// device by MAC (across DHCP leases) and IP holds its last-known address; with
// MAC empty it matches by IP alone (the only option for devices Minos can't see
// at layer 2 — off-subnet, IPv6, DoT/DoH). IP is always populated and valid so
// the config still loads on an older binary that only matches by IP. Name is a
// user label.
type Client struct {
	IP   string `yaml:"ip" json:"ip"`
	MAC  string `yaml:"mac,omitempty" json:"mac,omitempty"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Group is the assigned group name; empty means the default rules.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
	// Blocked refuses all DNS from this device, overriding any group.
	Blocked bool `yaml:"blocked,omitempty" json:"blocked"`
	// Notes is free-form user text about the device (location, owner,
	// model — whatever is worth remembering). Persisted in config; shown
	// on the device page. An older binary's tolerant loader drops it.
	Notes string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// ListSource is one remote list subscription. Whether its entries block or
// allow is decided by which ListsConfig slice it lives in, not a field on
// the source: a downgrade to a binary that predates allow-lists then drops
// the whole allow_sources key (tolerant loading) and the list simply
// vanishes — fail-safe over-blocking — instead of an ignored action field
// silently turning an allowlist into a blocklist of the very domains the
// user meant to protect. Same shape as the allowed_services decision.
type ListSource struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Format  string `yaml:"format"` // hosts, plain, adblock
	Enabled bool   `yaml:"enabled"`
	// Audit compiles the list's rules into the audit matcher instead of the
	// enforcing one: matches are logged as "would block" in the query log
	// but never enforced — try a strict list safely, then enforce it with
	// one click. Meaningless on an allowlist (validation rejects it there).
	Audit bool `yaml:"audit,omitempty"`
	// Mirrors are alternative URLs serving the same list, tried in order
	// when the primary is retired/unauthorized, when no copy is held yet,
	// or when the copy in force has gone stale — never for a passing blip,
	// because a mirror can lag and must not replace a fresher copy. An
	// additive key: an older Minos ignores it and just uses the primary.
	Mirrors []string `yaml:"mirrors,omitempty"`
	// Headers are extra request headers for the primary URL only — e.g. an
	// abuse.ch `Auth-Key` for a keyed feed. They are credentials: set in the
	// config file, never returned by the API, never sent to a mirror, and
	// stripped from any redirect to another host. Additive key.
	Headers map[string]string `yaml:"headers,omitempty"`
}

type ListsConfig struct {
	Sources []ListSource `yaml:"sources"`
	// AllowSources are subscribed allowlists: every entry is always
	// allowed, beating any blocklist (allow wins at every label depth,
	// like config allowlist entries and service pardons). List names are
	// unique across Sources and AllowSources.
	AllowSources    []ListSource `yaml:"allow_sources,omitempty"`
	RefreshInterval Duration     `yaml:"refresh_interval"`
}

type QueryLogConfig struct {
	// Ephemeral disables all disk logging; the ring buffer still feeds the UI.
	Ephemeral     bool   `yaml:"ephemeral"`
	DBPath        string `yaml:"db_path"`
	RingSize      int    `yaml:"ring_size"`
	RetentionDays int    `yaml:"retention_days"`
	// MemoryMB caps the RAM SQLite keeps for the query log — page caches,
	// sorts and aggregates together; larger work spills to a temp file.
	// 0 = the built-in default (32 MB). Applies live. Additive key: an
	// older Minos ignores it.
	MemoryMB int `yaml:"memory_mb,omitempty"`
}

type APIConfig struct {
	Listen string `yaml:"listen"`
	// Token is the API token for automation (Home Assistant, scripts, the
	// CLI): sent as X-Api-Token or Authorization: Bearer. With a password
	// set too, either one is accepted.
	Token string `yaml:"token"`
	// PasswordHash is the web-interface password as an argon2id PHC string
	// ($argon2id$v=19$m=…,t=…,p=…$salt$hash); empty = no password. Set it
	// from Settings → Access & security, never by hand.
	PasswordHash string `yaml:"password_hash,omitempty"`
	// SessionKey signs login sessions (base64, 32 bytes), generated with
	// the first password and replaced to sign everyone out.
	SessionKey string `yaml:"session_key,omitempty"`
	// TOTPSecret turns on a second login step (an authenticator-app code):
	// base32, empty = off.
	TOTPSecret string `yaml:"totp_secret,omitempty"`
}

// NotificationsConfig points events (new device, upstream sick/recovered,
// update available) at user-chosen destinations. Nothing is sent unless a
// URL is configured.
type NotificationsConfig struct {
	// WebhookURL receives each event as a JSON POST.
	WebhookURL string `yaml:"webhook_url,omitempty" json:"webhook_url"`
	// NtfyURL is a full topic URL (https://ntfy.sh/my-topic or self-hosted).
	NtfyURL string `yaml:"ntfy_url,omitempty" json:"ntfy_url"`
	// NtfyToken is sent as a bearer token for protected topics.
	NtfyToken string `yaml:"ntfy_token,omitempty" json:"-"`
	// Digest sends a periodic traffic summary through the sinks above:
	// "off" (default, also the empty value), "daily", or "weekly".
	Digest string `yaml:"digest,omitempty" json:"digest"`
	// DigestTime is the delivery time as 24h "HH:MM" in server-local time
	// (default "09:00").
	DigestTime string `yaml:"digest_time,omitempty" json:"digest_time"`
	// DigestDay is the delivery weekday for the weekly cadence, as a
	// lowercase English day name (default "monday"). Ignored for daily.
	DigestDay string `yaml:"digest_day,omitempty" json:"digest_day"`
	// UIURL is the address notification links open, e.g.
	// "http://192.168.1.2:8080". Empty derives it from api.listen (its host,
	// or this machine's LAN address when it listens on every interface).
	UIURL string `yaml:"ui_url,omitempty" json:"ui_url"`
	// QuietHours ("22:00-07:00", local time, may wrap midnight) delivers
	// notifications silently — except "DNS is down", which always sounds.
	QuietHours string `yaml:"quiet_hours,omitempty" json:"quiet_hours"`
	// Mute lists event types that are never sent (NotificationTypes).
	Mute []string `yaml:"mute,omitempty" json:"mute"`
}

// NotificationTypes are the event types a user can mute. internal/notify
// owns the events; its tests hold this list to them.
var NotificationTypes = []string{
	"device_new", "upstream_sick", "upstream_recovered", "dns_down",
	"list_failing", "list_recovered", "update_available", "acme_renewal_failed", "digest",
	"breakage_suspected", "threat_suspected",
}

// DigestSchedule resolves the configured digest timing, applying defaults
// and tolerating garbage (validation rejects it up front, but a hand-edited
// file must never panic the scheduler).
func (n NotificationsConfig) DigestSchedule() (hour, minute int, day time.Weekday) {
	hour, minute, day = 9, 0, time.Monday
	if h, m, err := ParseClock(n.DigestTime); err == nil {
		hour, minute = h, m
	}
	if d, err := parseWeekday(n.DigestDay); err == nil {
		day = d
	}
	return hour, minute, day
}

// ParseClock parses 24h "HH:MM". Empty is an error (callers apply defaults).
func ParseClock(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("must be 24h HH:MM, got %q", s)
	}
	return t.Hour(), t.Minute(), nil
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

// parseWeekday parses a lowercase English day name. Empty is an error
// (callers apply defaults).
func parseWeekday(s string) (time.Weekday, error) {
	if d, ok := weekdays[s]; ok {
		return d, nil
	}
	return 0, fmt.Errorf("must be a lowercase day name (monday…sunday), got %q", s)
}

// ThreatsConfig is the behaviour-based threat detection (internal/threats).
type ThreatsConfig struct {
	// Enabled watches the query log for devices behaving like malware:
	// generated domain names, data tunnelled through DNS, lookups of known
	// malicious domains. Default on; a plain bool (no omitempty) so turning
	// it off is written out.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Ignore lists domains never flagged (with subdomains) — a service that
	// legitimately looks like tunnelling, say.
	Ignore []string `yaml:"ignore,omitempty" json:"ignore"`
}

// DiscoveryConfig switches the *active* device-discovery probes (passive
// techniques need no switch — they only listen).
type DiscoveryConfig struct {
	// SSDP names UPnP devices (TVs, consoles, IoT): one small multicast
	// search every few minutes, plus one description fetch from each
	// responding device. Default on.
	SSDP bool `yaml:"ssdp" json:"ssdp"`
	// DHCPListen passively reads DHCP client broadcasts on UDP :67 (Linux;
	// only when the port is free — never when a DHCP server runs here) for
	// lease-time hostnames and OS hints. A listener, never a server:
	// nothing is transmitted, allocated, or answered. Default on.
	DHCPListen bool `yaml:"dhcp_listen" json:"dhcp_listen"`
}

// HomeAssistantConfig announces Minos to Home Assistant over MQTT
// (internal/hass): its switches, buttons and sensors appear in Home
// Assistant with no YAML.
type HomeAssistantConfig struct {
	// Enabled connects to the broker. Off by default.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Broker is mqtt://host:1883 or mqtts://host:8883 — usually Home
	// Assistant's Mosquitto add-on.
	Broker   string `yaml:"broker,omitempty" json:"broker"`
	Username string `yaml:"username,omitempty" json:"username"`
	// Password is a secret: never served by the API, hidden in the
	// settings history.
	Password string `yaml:"password,omitempty" json:"-"`
	// DiscoveryPrefix is Home Assistant's discovery prefix (default
	// "homeassistant").
	DiscoveryPrefix string `yaml:"discovery_prefix,omitempty" json:"discovery_prefix"`
	// TopicPrefix roots Minos's own state and command topics (default
	// "minos"); a second Minos on the same broker needs its own.
	TopicPrefix string `yaml:"topic_prefix,omitempty" json:"topic_prefix"`
	// DeviceSwitches adds a "Block <device>" switch for every device given a
	// name in Minos. Default on; a plain bool so turning it off is written.
	DeviceSwitches bool `yaml:"device_switches" json:"device_switches"`
}

// Prefixes returns the discovery and topic prefixes, defaults applied.
func (h HomeAssistantConfig) Prefixes() (discovery, topic string) {
	discovery, topic = h.DiscoveryPrefix, h.TopicPrefix
	if discovery == "" {
		discovery = "homeassistant"
	}
	if topic == "" {
		topic = "minos"
	}
	return discovery, topic
}

type Config struct {
	DNS           DNSConfig           `yaml:"dns"`
	Blocking      BlockingConfig      `yaml:"blocking"`
	Groups        []Group             `yaml:"groups,omitempty"`
	Clients       []Client            `yaml:"clients,omitempty"`
	Lists         ListsConfig         `yaml:"lists"`
	QueryLog      QueryLogConfig      `yaml:"querylog"`
	API           APIConfig           `yaml:"api"`
	Notifications NotificationsConfig `yaml:"notifications,omitempty"`
	Discovery     DiscoveryConfig     `yaml:"discovery"`
	Threats       ThreatsConfig       `yaml:"threats"`
	HomeAssistant HomeAssistantConfig `yaml:"home_assistant"`
	// UpdateCheck, when true, asks the GitHub releases API for the latest
	// version once a day. Strictly opt-in (default false): nothing is
	// sent beyond the request itself, and nothing is sent at all unless
	// the user turns it on.
	UpdateCheck bool `yaml:"update_check"`
	// LogLevel is the application log level set from the UI: debug, info,
	// warn or error. Empty keeps the -log-level flag's (default info). It
	// applies live, to the journal and the UI's Logs page alike.
	LogLevel string `yaml:"log_level,omitempty"`
	// UpdateInstallMethod forces the install method the upgrade guidance
	// assumes ("binary", "docker", or "source") for deployments the
	// detection can't see — a distro package, a k8s manifest. Empty (the
	// default) means detect: runtime container markers, then the
	// build-time stamp, then a dev-version heuristic.
	UpdateInstallMethod string `yaml:"update_install_method,omitempty"`
}

// Default returns the configuration used when no file exists yet.
func Default() *Config {
	return &Config{
		DNS: DNSConfig{
			Listen: ":53",
			Upstreams: []Upstream{
				// IP-literal DoH URL, not cloudflare-dns.com: a DNS server
				// must not have to resolve its own resolver's hostname before
				// it can forward anything. Cloudflare's certificate carries
				// 1.1.1.1 as an IP SAN, so TLS validation still succeeds and
				// no bootstrap resolver is needed.
				{Address: "https://1.1.1.1/dns-query", Protocol: "doh"},
				{Address: "1.0.0.1:853", Protocol: "dot"},
			},
			BlockTTL: 60,
			Cache: CacheConfig{
				Enabled:    true,
				MaxEntries: 10000,
				MinTTL:     10,
				MaxTTL:     3600,
				ServeStale: true,
			},
			LocalTTL:  300,
			RateLimit: 1000,
		},
		Blocking:      BlockingConfig{Mode: "zero_ip", CNAMEInspection: true},
		Threats:       ThreatsConfig{Enabled: true},
		HomeAssistant: HomeAssistantConfig{DeviceSwitches: true},
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
		API:       APIConfig{Listen: "0.0.0.0:8080"},
		Discovery: DiscoveryConfig{SSDP: true, DHCPListen: true},
	}
}

func (c *Config) Clone() *Config {
	out := *c
	out.DNS.Upstreams = append([]Upstream(nil), c.DNS.Upstreams...)
	out.Blocking.Allowlist = append([]string(nil), c.Blocking.Allowlist...)
	out.Blocking.TemporaryAllows = append([]TemporaryAllow(nil), c.Blocking.TemporaryAllows...)
	out.Threats.Ignore = append([]string(nil), c.Threats.Ignore...)
	out.Blocking.Denylist = append([]string(nil), c.Blocking.Denylist...)
	out.Blocking.Services = append([]string(nil), c.Blocking.Services...)
	out.Blocking.AllowedServices = append([]string(nil), c.Blocking.AllowedServices...)
	out.Blocking.CustomServices = make([]services.Custom, len(c.Blocking.CustomServices))
	for i, cs := range c.Blocking.CustomServices {
		out.Blocking.CustomServices[i] = cs
		out.Blocking.CustomServices[i].Domains = append([]string(nil), cs.Domains...)
		out.Blocking.CustomServices[i].AllowExtra = append([]string(nil), cs.AllowExtra...)
	}
	out.Lists.Sources = cloneSources(c.Lists.Sources)
	out.Lists.AllowSources = cloneSources(c.Lists.AllowSources)
	out.Groups = make([]Group, len(c.Groups))
	for i, g := range c.Groups {
		out.Groups[i] = g
		out.Groups[i].Allowlist = append([]string(nil), g.Allowlist...)
		out.Groups[i].Denylist = append([]string(nil), g.Denylist...)
		out.Groups[i].Services = append([]string(nil), g.Services...)
		out.Groups[i].AllowedServices = append([]string(nil), g.AllowedServices...)
		out.Groups[i].CustomServices = append([]string(nil), g.CustomServices...)
		out.Groups[i].AllowedCustomServices = append([]string(nil), g.AllowedCustomServices...)
		if g.Schedule != nil {
			sch := *g.Schedule
			sch.Days = append([]string(nil), g.Schedule.Days...)
			out.Groups[i].Schedule = &sch
		}
	}
	out.Clients = append([]Client(nil), c.Clients...)
	out.DNS.AllowedNetworks = append([]string(nil), c.DNS.AllowedNetworks...)
	out.Notifications.Mute = append([]string(nil), c.Notifications.Mute...)
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
	if c.DNS.TLS.ACME != nil {
		a := *c.DNS.TLS.ACME
		out.DNS.TLS.ACME = &a
	}
	return &out
}

// reservedListHeaders are set by Minos itself (or by the transport) and may
// not be overridden: conditional-request validators, identification, and
// framing headers whose override could corrupt the exchange.
var reservedListHeaders = map[string]bool{
	"host": true, "user-agent": true, "if-none-match": true, "if-modified-since": true,
	"content-length": true, "transfer-encoding": true, "connection": true,
}

func notHeaderNameRune(r rune) bool {
	isName := r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	return !isName
}

func validateListHeaders(h map[string]string) error {
	if len(h) > 10 {
		return fmt.Errorf("at most 10, got %d", len(h))
	}
	for k, v := range h {
		if k == "" || strings.IndexFunc(k, notHeaderNameRune) >= 0 {
			return fmt.Errorf("%q is not a valid header name (letters, digits and dashes)", k)
		}
		if reservedListHeaders[strings.ToLower(k)] {
			return fmt.Errorf("%q is set by Minos and cannot be overridden", k)
		}
		// CR/LF would let a value inject further headers.
		if len(v) > 1024 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("%q: value must be at most 1024 characters, on one line", k)
		}
	}
	return nil
}

// cloneSources deep-copies list sources: Mirrors is a slice, and a shallow
// copy would let one config snapshot's edit show through in another.
func cloneSources(in []ListSource) []ListSource {
	if in == nil {
		return nil
	}
	out := make([]ListSource, len(in))
	for i, s := range in {
		out[i] = s
		out[i].Mirrors = append([]string(nil), s.Mirrors...)
		if s.Headers != nil {
			out[i].Headers = make(map[string]string, len(s.Headers))
			for k, v := range s.Headers {
				out[i].Headers[k] = v
			}
		}
	}
	return out
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
	// A warning, not an error: blocking the encrypted-dns service while a
	// doh/dot upstream is named by a hostname it covers can self-sabotage —
	// on a production box the OS resolver Go dials through is usually Minos
	// itself. The shipped presets use IP-literal DoH, so this only bites
	// hand-typed hostname upstreams.
	for _, w := range c.encryptedDNSUpstreamWarnings() {
		slog.Warn(w)
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
	if t := c.DNS.TLS; t.Enabled() {
		hasFiles := t.CertFile != "" || t.KeyFile != ""
		switch {
		case t.ACME != nil && hasFiles:
			return fmt.Errorf("dns.tls: acme and cert_file/key_file are mutually exclusive")
		case t.ACME == nil && (t.CertFile == "" || t.KeyFile == ""):
			return fmt.Errorf("dns.tls: cert_file+key_file or an acme block is required when dot_listen, doh_listen or doq_listen is set")
		}
		if t.DoTListen != "" {
			if err := validateHostPort(t.DoTListen); err != nil {
				return fmt.Errorf("dns.tls.dot_listen: %w", err)
			}
		}
		if t.DoHListen != "" {
			if err := validateHostPort(t.DoHListen); err != nil {
				return fmt.Errorf("dns.tls.doh_listen: %w", err)
			}
		}
		if t.DoQListen != "" {
			if err := validateHostPort(t.DoQListen); err != nil {
				return fmt.Errorf("dns.tls.doq_listen: %w", err)
			}
		}
		if a := t.ACME; a != nil {
			if a.Email == "" || !strings.Contains(a.Email, "@") {
				return fmt.Errorf("dns.tls.acme.email: a contact email is required")
			}
			if !validDomain(a.Domain) {
				return fmt.Errorf("dns.tls.acme.domain: %q is not a valid domain", a.Domain)
			}
			switch a.Provider {
			case "cloudflare", "desec", "duckdns":
				if a.APIToken == "" {
					return fmt.Errorf("dns.tls.acme.api_token: required for provider %q", a.Provider)
				}
			case "rfc2136":
				if err := validateHostPort(a.Server); err != nil {
					return fmt.Errorf("dns.tls.acme.server: %w", err)
				}
				if a.TSIGName == "" || a.TSIGSecret == "" {
					return fmt.Errorf("dns.tls.acme: tsig_name and tsig_secret are required for rfc2136")
				}
			default:
				return fmt.Errorf("dns.tls.acme.provider: must be cloudflare, desec, duckdns, or rfc2136, got %q", a.Provider)
			}
			if a.DirectoryURL != "" {
				parsed, err := url.Parse(a.DirectoryURL)
				if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
					return fmt.Errorf("dns.tls.acme.directory_url: must be an https URL, got %q", a.DirectoryURL)
				}
			}
		}
	}
	switch c.DNS.DNSSEC {
	case "", "off", "permissive", "enforce":
	default:
		return fmt.Errorf("dns.dnssec: must be off, permissive, or enforce, got %q", c.DNS.DNSSEC)
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
	if err := validateHomeAssistant(c.HomeAssistant); err != nil {
		return err
	}
	if len(c.Threats.Ignore) > 500 {
		return fmt.Errorf("threats.ignore: at most 500 entries")
	}
	for i, d := range c.Threats.Ignore {
		if !validDomain(d) {
			return fmt.Errorf("threats.ignore[%d]: %q is not a valid domain", i, d)
		}
	}
	if len(c.Blocking.TemporaryAllows) > 1000 {
		return fmt.Errorf("blocking.temporary_allows: at most 1000 entries")
	}
	for i, t := range c.Blocking.TemporaryAllows {
		if !validDomain(t.Domain) {
			return fmt.Errorf("blocking.temporary_allows[%d].domain: %q is not a valid domain", i, t.Domain)
		}
		if t.Until.IsZero() {
			return fmt.Errorf("blocking.temporary_allows[%d].until: an expiry time is required", i)
		}
	}
	for i, s := range c.Blocking.AllowedServices {
		if !services.Exists(s) {
			return fmt.Errorf("blocking.allowed_services[%d]: unknown service %q", i, s)
		}
	}
	customNames := make(map[string]bool, len(c.Blocking.CustomServices))
	for i, cs := range c.Blocking.CustomServices {
		if !validSlug(cs.Name) {
			return fmt.Errorf("blocking.custom_services[%d].name: %q must be a lowercase slug (a-z, 0-9, hyphens; max 40 chars)", i, cs.Name)
		}
		if services.Exists(cs.Name) {
			return fmt.Errorf("blocking.custom_services[%d].name: %q collides with a built-in catalog service", i, cs.Name)
		}
		if customNames[cs.Name] {
			return fmt.Errorf("blocking.custom_services[%d].name: duplicate custom service %q", i, cs.Name)
		}
		customNames[cs.Name] = true
		if len(cs.Label) > 100 {
			return fmt.Errorf("blocking.custom_services[%d].label: must be at most 100 characters", i)
		}
		if len(cs.Domains) == 0 {
			return fmt.Errorf("blocking.custom_services[%d].domains: at least one domain is required", i)
		}
		if len(cs.Domains) > 200 {
			return fmt.Errorf("blocking.custom_services[%d].domains: at most 200 domains, got %d", i, len(cs.Domains))
		}
		for j, d := range cs.Domains {
			if !validDomain(d) {
				return fmt.Errorf("blocking.custom_services[%d].domains[%d]: %q is not a valid domain", i, j, d)
			}
		}
		if len(cs.AllowExtra) > 50 {
			return fmt.Errorf("blocking.custom_services[%d].allow_extra: at most 50 domains, got %d", i, len(cs.AllowExtra))
		}
		for j, d := range cs.AllowExtra {
			if !validDomain(d) {
				return fmt.Errorf("blocking.custom_services[%d].allow_extra[%d]: %q is not a valid domain", i, j, d)
			}
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
		for j, s := range g.AllowedServices {
			if !services.Exists(s) {
				return fmt.Errorf("groups[%d].allowed_services[%d]: unknown service %q", i, j, s)
			}
		}
		for j, s := range g.CustomServices {
			if !customNames[s] {
				return fmt.Errorf("groups[%d].custom_services[%d]: no custom service named %q", i, j, s)
			}
		}
		for j, s := range g.AllowedCustomServices {
			if !customNames[s] {
				return fmt.Errorf("groups[%d].allowed_custom_services[%d]: no custom service named %q", i, j, s)
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
	clientMACs := make(map[string]bool, len(c.Clients))
	for i, cl := range c.Clients {
		if net.ParseIP(cl.IP) == nil {
			return fmt.Errorf("clients[%d].ip: %q is not a valid IP address", i, cl.IP)
		}
		if clientIPs[cl.IP] {
			return fmt.Errorf("clients[%d].ip: duplicate client %q", i, cl.IP)
		}
		clientIPs[cl.IP] = true
		if cl.MAC != "" {
			hw, err := net.ParseMAC(cl.MAC)
			if err != nil {
				return fmt.Errorf("clients[%d].mac: %q is not a valid MAC address", i, cl.MAC)
			}
			// Canonical form so aa:bb… and AA-BB… count as the same device.
			key := hw.String()
			if clientMACs[key] {
				return fmt.Errorf("clients[%d].mac: duplicate client %q", i, cl.MAC)
			}
			clientMACs[key] = true
		}
		if cl.Group != "" && cl.Group != "default" && !groupNames[cl.Group] {
			return fmt.Errorf("clients[%d].group: no group named %q", i, cl.Group)
		}
		if len(cl.Notes) > 4096 {
			return fmt.Errorf("clients[%d].notes: must be at most 4096 characters", i)
		}
	}
	listNames := make(map[string]bool, len(c.Lists.Sources)+len(c.Lists.AllowSources))
	for key, sources := range map[string][]ListSource{
		"lists.sources":       c.Lists.Sources,
		"lists.allow_sources": c.Lists.AllowSources,
	} {
		for i, s := range sources {
			if s.Name == "" {
				return fmt.Errorf("%s[%d].name: must not be empty", key, i)
			}
			if listNames[s.Name] {
				return fmt.Errorf("%s[%d].name: %q is used by another list", key, i, s.Name)
			}
			listNames[s.Name] = true
			parsed, err := url.Parse(s.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return fmt.Errorf("%s[%d].url: must be an http(s) URL, got %q", key, i, s.URL)
			}
			switch s.Format {
			case "hosts", "plain", "adblock":
			default:
				return fmt.Errorf("%s[%d].format: must be hosts, plain, or adblock, got %q", key, i, s.Format)
			}
			for j, mu := range s.Mirrors {
				p, err := url.Parse(mu)
				if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
					return fmt.Errorf("%s[%d].mirrors[%d]: must be an http(s) URL, got %q", key, i, j, mu)
				}
				if mu == s.URL {
					return fmt.Errorf("%s[%d].mirrors[%d]: is the list's own url", key, i, j)
				}
			}
			if err := validateListHeaders(s.Headers); err != nil {
				return fmt.Errorf("%s[%d].headers: %w", key, i, err)
			}
			if len(s.Mirrors) > 5 {
				return fmt.Errorf("%s[%d].mirrors: at most 5, got %d", key, i, len(s.Mirrors))
			}
			if s.Audit && key == "lists.allow_sources" {
				return fmt.Errorf("%s[%d].audit: auditing an allowlist is meaningless — allows are never enforced against", key, i)
			}
		}
	}
	if c.Lists.RefreshInterval.Std() < 5*time.Minute {
		return fmt.Errorf("lists.refresh_interval: must be at least 5m, got %s", c.Lists.RefreshInterval.Std())
	}
	if c.QueryLog.RingSize <= 0 {
		return fmt.Errorf("querylog.ring_size: must be positive, got %d", c.QueryLog.RingSize)
	}
	if m := c.QueryLog.MemoryMB; m != 0 && (m < 8 || m > 4096) {
		return fmt.Errorf("querylog.memory_mb: must be 8–4096 (or 0 for the default), got %d", m)
	}
	if c.QueryLog.RetentionDays < 1 {
		return fmt.Errorf("querylog.retention_days: must be at least 1, got %d", c.QueryLog.RetentionDays)
	}
	if !c.QueryLog.Ephemeral && c.QueryLog.DBPath == "" {
		return fmt.Errorf("querylog.db_path: required unless querylog.ephemeral is true")
	}
	for _, n := range c.DNS.AllowedNetworks {
		if _, err := netip.ParsePrefix(n); err != nil {
			if _, err := netip.ParseAddr(n); err != nil {
				return fmt.Errorf("dns.allowed_networks: %q is not a network (CIDR such as 192.168.1.0/24) or an address", n)
			}
		}
	}
	if c.DNS.RateLimit < 0 || c.DNS.RateLimit > 1000000 {
		return fmt.Errorf("dns.rate_limit: must be 0 (off) to 1000000 queries a minute, got %d", c.DNS.RateLimit)
	}
	if err := validateHostPort(c.API.Listen); err != nil {
		return fmt.Errorf("api.listen: %w", err)
	}
	for name, u := range map[string]string{
		"notifications.webhook_url": c.Notifications.WebhookURL,
		"notifications.ntfy_url":    c.Notifications.NtfyURL,
		"notifications.ui_url":      c.Notifications.UIURL,
	} {
		if u == "" {
			continue
		}
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("%s: must be an http(s) URL, got %q", name, u)
		}
		if name == "notifications.ntfy_url" && strings.Trim(parsed.Path, "/") == "" {
			return fmt.Errorf("%s: must name a topic, e.g. https://ntfy.sh/my-topic, got %q", name, u)
		}
	}
	if s := c.Notifications.QuietHours; s != "" {
		from, to, ok := strings.Cut(s, "-")
		if !ok {
			return fmt.Errorf("notifications.quiet_hours: must look like 22:00-07:00, got %q", s)
		}
		for _, part := range []string{from, to} {
			if _, _, err := ParseClock(strings.TrimSpace(part)); err != nil {
				return fmt.Errorf("notifications.quiet_hours: %w", err)
			}
		}
	}
	for _, m := range c.Notifications.Mute {
		if !slices.Contains(NotificationTypes, m) {
			return fmt.Errorf("notifications.mute: unknown event type %q (known: %s)",
				m, strings.Join(NotificationTypes, ", "))
		}
	}
	switch c.Notifications.Digest {
	case "", "off", "daily", "weekly":
	default:
		return fmt.Errorf("notifications.digest: must be off, daily, or weekly, got %q", c.Notifications.Digest)
	}
	if s := c.Notifications.DigestTime; s != "" {
		if _, _, err := ParseClock(s); err != nil {
			return fmt.Errorf("notifications.digest_time: %w", err)
		}
	}
	if s := c.Notifications.DigestDay; s != "" {
		if _, err := parseWeekday(s); err != nil {
			return fmt.Errorf("notifications.digest_day: %w", err)
		}
	}
	switch c.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level: must be debug, info, warn, or error, got %q", c.LogLevel)
	}
	switch c.UpdateInstallMethod {
	case "", "binary", "docker", "source":
	default:
		return fmt.Errorf("update_install_method: must be binary, docker, or source, got %q", c.UpdateInstallMethod)
	}
	return nil
}

// ValidateUpstream checks one upstream the way saving the config does (the
// Settings "Test" button checks an unsaved row with it).
func ValidateUpstream(u Upstream) error { return validateUpstream(u) }

func validateUpstream(u Upstream) error {
	switch u.Protocol {
	case "udp", "tcp", "dot", "doq":
		// The mistake this catches was a real one: a DoH URL left under
		// protocol dot passed the old SplitHostPort check (host "https",
		// port "//8.8.8.8/dns-query") and then failed every exchange.
		if strings.Contains(u.Address, "://") {
			return fmt.Errorf("address: %q is a URL, not host:port — for a DoH URL set protocol to doh", u.Address)
		}
		if err := validateHostPort(u.Address); err != nil {
			return fmt.Errorf("address: %w", err)
		}
		host, port, _ := net.SplitHostPort(u.Address)
		if host == "" || port == "0" {
			return fmt.Errorf("address: %q needs both a host and a non-zero port", u.Address)
		}
	case "doh", "doh3":
		parsed, err := url.Parse(u.Address)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("address: %s requires an https URL, got %q", u.Protocol, u.Address)
		}
	default:
		return fmt.Errorf("protocol: must be udp, tcp, dot, doh, doq, or doh3, got %q", u.Protocol)
	}
	return nil
}

// encryptedDNSUpstreamWarnings reports doh/dot upstreams whose hostname the
// enabled "encrypted-dns" service block covers. Minos's own forwarding never
// passes the filter, but resolving a hostname upstream goes through the OS
// resolver — usually Minos itself in production — so the block would starve
// the upstream of its own address. Warn, never reject: the operator may
// resolve it elsewhere. Catalog domains only: a custom service covering an
// upstream hostname gets no warning (curating that is the user's business).
func (c *Config) encryptedDNSUpstreamWarnings() []string {
	enabled := slices.Contains(c.Blocking.Services, "encrypted-dns")
	for _, g := range c.Groups {
		enabled = enabled || slices.Contains(g.Services, "encrypted-dns")
	}
	if !enabled {
		return nil
	}
	covered := make(map[string]bool)
	for _, d := range services.Domains("encrypted-dns") {
		covered[d] = true
	}
	var out []string
	for _, u := range c.DNS.Upstreams {
		var host string
		switch u.Protocol {
		case "doh", "doh3":
			if parsed, err := url.Parse(u.Address); err == nil {
				host = parsed.Hostname()
			}
		case "dot", "doq":
			host, _, _ = net.SplitHostPort(u.Address)
		default:
			continue
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == "" || net.ParseIP(host) != nil {
			continue
		}
		// The service block covers subdomains, so walk the label suffixes.
		for probe := host; probe != ""; {
			if covered[probe] {
				out = append(out, fmt.Sprintf(
					"upstream %s resolves via a hostname the blocked encrypted-dns service covers (%s); "+
						"clients of this Minos cannot resolve it — use an IP-literal upstream to be safe", u.Address, probe))
				break
			}
			dot := strings.IndexByte(probe, '.')
			if dot < 0 {
				break
			}
			probe = probe[dot+1:]
		}
	}
	return out
}

// validSlug reports whether s is a lowercase slug: [a-z0-9] then up to 39
// more of [a-z0-9-]. Custom-service names become YAML values, API path
// segments, and "service:<name>" pseudo-list names, so they stay strict.
func validSlug(s string) bool {
	if len(s) == 0 || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
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
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	// SplitHostPort splits on the last colon and checks nothing else, so a
	// URL ("https://x/y") or a path slips through. An empty host is legal
	// (":53" listens on every address) and port 0 is legal for listeners
	// (ephemeral, used by tests); upstreams add their own stricter check.
	if strings.ContainsAny(host, "/?#@ ") {
		return fmt.Errorf("%q: host must be a hostname or IP address", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%q: port must be a number from 0 to 65535", addr)
	}
	return nil
}

// healUpstreams repairs or drops on-disk upstreams that today's validation
// refuses but an older Minos accepted, so an upgrade never bricks boot on a
// config the previous version started with. A DoH URL filed under dot is
// what the user evidently meant, so it becomes doh; anything else invalid is
// dropped with a warning. If nothing usable is left, the defaults stand in —
// the old binary would have started, but with no working upstream at all.
func healUpstreams(c *Config) {
	kept := c.DNS.Upstreams[:0]
	for _, u := range c.DNS.Upstreams {
		if validateUpstream(u) != nil && u.Protocol == "dot" &&
			validateUpstream(Upstream{Address: u.Address, Protocol: "doh"}) == nil {
			slog.Warn("config: upstream is a DoH URL filed under protocol dot; treating it as doh",
				"address", u.Address)
			u.Protocol = "doh"
		}
		if err := validateUpstream(u); err != nil {
			slog.Warn("config: dropping invalid upstream", "address", u.Address,
				"protocol", u.Protocol, "err", err)
			continue
		}
		kept = append(kept, u)
	}
	c.DNS.Upstreams = kept
	if len(kept) == 0 {
		slog.Warn("config: no valid upstreams left; using the defaults")
		c.DNS.Upstreams = Default().DNS.Upstreams
	}
}

// save writes the config atomically: temp file in the same directory, then
// rename. Before overwriting an existing file it keeps a one-step-back copy at
// <path>.bak, so a bad settings change — or a config first rewritten after a
// version upgrade — always has a recovery point.
func save(path string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := backupExisting(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".minos-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after the rename succeeds
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

// backupExisting copies path to <path>.bak when path already exists; a missing
// file (first run) is not an error.
func backupExisting(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config for backup: %w", err)
	}
	if err := os.WriteFile(path+".bak", data, 0o600); err != nil {
		return fmt.Errorf("write config backup: %w", err)
	}
	return nil
}

func load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := parseTolerant(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// parseTolerant decodes the on-disk config, ignoring (but logging) unknown
// fields, so a config written by a newer Minos still loads after a downgrade
// and settings survive the round trip even when the older binary can't model
// every key. Strict parsing (Parse) is reserved for user-uploaded restores,
// where an unrecognised key is more likely a typo worth rejecting outright.
func parseTolerant(data []byte) (*Config, error) {
	if detail := unknownFields(data); detail != "" {
		slog.Warn("config has unrecognised fields; ignoring them "+
			"(written by a newer Minos?)", "detail", detail)
	}
	c := Default()
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	healDuplicateClientMACs(c)
	healUpstreams(c)
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return c, nil
}

// healDuplicateClientMACs demotes later duplicate-MAC client entries to
// IP-keyed so a hand-edited config still boots: validation rejects duplicate
// MACs, but a startup load must not brick on a mistake the API self-heals on
// its next write anyway. Strict Parse (restores) and Update stay strict.
func healDuplicateClientMACs(c *Config) {
	seen := make(map[string]bool, len(c.Clients))
	for i := range c.Clients {
		cl := &c.Clients[i]
		if cl.MAC == "" {
			continue
		}
		hw, err := net.ParseMAC(cl.MAC)
		if err != nil {
			continue // validation reports the malformed MAC itself
		}
		key := hw.String()
		if seen[key] {
			slog.Warn("config: duplicate client MAC; keeping the first entry "+
				"and demoting this one to IP-keyed", "mac", cl.MAC, "ip", cl.IP)
			cl.MAC = ""
			continue
		}
		seen[key] = true
	}
}

// unknownFields reports the decoder's complaint about the first unrecognised
// field, or "" when the file contains only known keys.
func unknownFields(data []byte) string {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(Default()); err != nil && strings.Contains(err.Error(), "not found in type") {
		return err.Error()
	}
	return ""
}

// Parse decodes and validates a YAML config from raw bytes, starting from
// defaults so a partial or older file still fills in sane values. Used to
// restore an uploaded backup.
func Parse(data []byte) (*Config, error) {
	c := Default()
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return c, nil
}

// validateHomeAssistant checks the MQTT settings. A broker is needed only
// once the connection is switched on, so a half-filled form can be saved.
func validateHomeAssistant(h HomeAssistantConfig) error {
	if h.Broker != "" {
		if _, _, _, err := mqtt.ParseBroker(h.Broker); err != nil {
			return fmt.Errorf("home_assistant.broker: %w", err)
		}
	} else if h.Enabled {
		return errors.New("home_assistant.broker: give the broker's address (mqtt://host:1883) to connect")
	}
	if len(h.Username) > 256 || len(h.Password) > 1024 {
		return errors.New("home_assistant: the username or password is too long")
	}
	for _, p := range []struct{ key, v string }{
		{"home_assistant.discovery_prefix", h.DiscoveryPrefix},
		{"home_assistant.topic_prefix", h.TopicPrefix},
	} {
		if p.v == "" {
			continue
		}
		if len(p.v) > 64 || mqtt.ValidTopic(p.v) != nil || strings.HasPrefix(p.v, "/") ||
			strings.HasSuffix(p.v, "/") || strings.Contains(p.v, "//") || strings.HasPrefix(p.v, "$") {
			return fmt.Errorf("%s: %q must be a plain topic like \"minos\" (no +, #, $ or leading/trailing slashes)", p.key, p.v)
		}
	}
	return nil
}
