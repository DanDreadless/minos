package lists

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/services"
)

const (
	// maxListBytes caps a single downloaded list. List content is
	// attacker-controllable; never trust Content-Length.
	maxListBytes = 64 << 20 // 64 MB
	fetchTimeout = 2 * time.Minute
	// userAgent is the fallback until SetVersion stamps the real one. List
	// publishers read it to see who fetches their lists (and how often), so it
	// names the project honestly — the old string carried a frozen "0.1" and
	// a URL that does not exist.
	userAgent = "Minos (+https://github.com/DanDreadless/minos)"
)

// SourceStatus is what the API reports per configured list.
type SourceStatus struct {
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Format      string    `json:"format"`
	Action      string    `json:"action"` // "block" or "allow" (empty config = block)
	Enabled     bool      `json:"enabled"`
	Audit       bool      `json:"audit"` // rules logged as "would block", never enforced
	Rules       int       `json:"rules"`
	Skipped     int       `json:"skipped"`
	LastRefresh time.Time `json:"last_refresh,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

// Manager owns list fetching and matcher rebuilds. A rebuild always
// constructs a complete new matcher off the hot path, then swaps it into
// the engine atomically — a live matcher is never mutated.
//
// It owns two engines: the enforcing one it was given, and a second audit
// engine it creates, compiled from only the audit:true sources. Audit rules
// are matched and attributed ("would block") but never enforced — the
// enforcing matcher stays byte-identical whether audit lists exist or not.
type Manager struct {
	engine      *filter.Engine
	auditEngine *filter.Engine
	store       *config.Store
	client      *http.Client
	ua          string

	mu     sync.Mutex
	cached map[string][]byte    // source name → last good raw body
	meta   map[string]cacheMeta // source name → validators for conditional GET
	status map[string]*SourceStatus
	disk   *diskCache // nil = memory only (ephemeral mode, or not configured)

	refreshNow chan struct{}
}

func NewManager(engine *filter.Engine, store *config.Store) *Manager {
	m := &Manager{
		engine:      engine,
		auditEngine: filter.NewEngine(),
		store:       store,
		client:      &http.Client{Timeout: fetchTimeout},
		ua:          userAgent,
		cached:      make(map[string][]byte),
		meta:        make(map[string]cacheMeta),
		status:      make(map[string]*SourceStatus),
		refreshNow:  make(chan struct{}, 1),
	}
	// Config changes (new pardons/sentences, list edits) rebuild from cache
	// immediately — no refetch needed, no restart ever.
	store.OnChange(func(*config.Config) { m.TriggerRebuild() })
	return m
}

// SetVersion stamps the running version into the fetch User-Agent. Call
// before Run.
func (m *Manager) SetVersion(v string) {
	m.ua = "Minos/" + v + " (+https://github.com/DanDreadless/minos)"
}

// SetCacheDir enables the on-disk list cache in dir. Call before Run. A
// failure to create the directory leaves the cache memory-only (logged):
// Minos still works, it just can't survive a restart during an outage.
func (m *Manager) SetCacheDir(dir string) {
	d, err := newDiskCache(dir)
	if err != nil {
		slog.Warn("list cache disabled", "dir", dir, "err", err)
		return
	}
	m.disk = d
}

// loadFromDisk fills the in-memory cache from disk for every enabled source
// that has no body yet, and reports whether anything was loaded.
func (m *Manager) loadFromDisk() bool {
	if m.disk == nil {
		return false
	}
	loaded := 0
	for _, src := range allSources(m.store.Get()) {
		if !src.Enabled {
			continue
		}
		m.mu.Lock()
		_, have := m.cached[src.Name]
		m.mu.Unlock()
		if have {
			continue
		}
		body, meta, ok := m.disk.load(src.URL)
		if !ok {
			continue
		}
		m.mu.Lock()
		m.cached[src.Name] = body
		m.meta[src.Name] = meta
		st := m.ensureStatus(src)
		st.LastRefresh = meta.FetchedAt
		m.mu.Unlock()
		loaded++
	}
	if loaded > 0 {
		slog.Info("lists loaded from disk cache", "lists", loaded)
	}
	return loaded > 0
}

// AuditEngine exposes the audit matcher for the DNS server's "would block"
// attribution. Never nil; empty until an audit:true source compiles.
func (m *Manager) AuditEngine() *filter.Engine { return m.auditEngine }

// Run blocks: initial refresh, then periodic refreshes until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	// Compile what's on disk first: blocking is live from the first second,
	// before (and regardless of) the network.
	if m.loadFromDisk() {
		m.rebuild(ctx, false)
	}
	m.Refresh(ctx)
	for {
		interval := m.store.Get().Lists.RefreshInterval.Std()
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			m.Refresh(ctx)
		case <-m.refreshNow:
			m.rebuild(ctx, false)
		}
	}
}

// TriggerRebuild schedules an immediate rebuild from cached list bodies
// (used after config edits). Non-blocking.
func (m *Manager) TriggerRebuild() {
	select {
	case m.refreshNow <- struct{}{}:
	default:
	}
}

// Refresh refetches every enabled source and rebuilds the matcher.
// Sources that fail keep their last good cached body.
func (m *Manager) Refresh(ctx context.Context) {
	m.rebuild(ctx, true)
}

// annotated pairs a configured source with what its entries do, which is
// decided by the config slice it came from (sources vs. allow_sources).
type annotated struct {
	config.ListSource
	allow bool
}

// allSources returns every configured source in config order, blocklists
// first, allowlists after. Compile order does not decide precedence — an
// allow wins over any deny by matcher semantics.
func allSources(cfg *config.Config) []annotated {
	out := make([]annotated, 0, len(cfg.Lists.Sources)+len(cfg.Lists.AllowSources))
	for _, s := range cfg.Lists.Sources {
		out = append(out, annotated{s, false})
	}
	for _, s := range cfg.Lists.AllowSources {
		out = append(out, annotated{s, true})
	}
	return out
}

// EnsureFetched downloads only enabled sources that have no cached body yet
// (freshly added or URL-changed lists), then rebuilds. Cheaper than a full
// Refresh after a config edit.
func (m *Manager) EnsureFetched(ctx context.Context) {
	cfg := m.store.Get()
	for _, src := range allSources(cfg) {
		if !src.Enabled {
			continue
		}
		m.mu.Lock()
		_, have := m.cached[src.Name]
		m.mu.Unlock()
		if have {
			continue
		}
		m.refreshSource(ctx, src)
	}
	m.rebuild(ctx, false)
}

// Forget drops the cached body and status for a source, forcing a refetch on
// the next EnsureFetched/Refresh. Used when a list is removed or its URL edited.
func (m *Manager) Forget(name string) {
	m.mu.Lock()
	delete(m.cached, name)
	delete(m.meta, name)
	delete(m.status, name)
	m.mu.Unlock()
}

func (m *Manager) rebuild(ctx context.Context, refetch bool) {
	cfg := m.store.Get()
	start := time.Now()

	if refetch {
		keep := make(map[string]bool)
		for _, src := range allSources(cfg) {
			keep[src.URL] = true // a disabled list keeps its cache for re-enable
			if !src.Enabled {
				continue
			}
			m.refreshSource(ctx, src)
		}
		if m.disk != nil {
			m.disk.prune(keep)
		}
	}

	b := filter.NewBuilder()
	// Config-level entries compile first so they win domain-priority ties;
	// pardons beat everything by matcher semantics anyway.
	for _, d := range cfg.Blocking.Allowlist {
		b.AddAllow("allowlist", d)
	}
	for _, d := range cfg.Blocking.Denylist {
		b.AddDeny("denylist", d)
	}
	// Globally blocked services: curated bundles, one pseudo-list per
	// service so the docket names the service that condemned a query.
	for _, name := range cfg.Blocking.Services {
		for _, d := range services.Domains(name) {
			b.AddDeny("service:"+name, d)
		}
	}
	// Globally pardoned services: the allow bundle is the deny bundle plus
	// the playback hosts a working service needs; allow wins over any deny,
	// including the same service being blocked above.
	for _, name := range cfg.Blocking.AllowedServices {
		for _, d := range services.AllowDomains(name) {
			b.AddAllow("service:"+name, d)
		}
	}
	// Custom services carry their global block/allow toggles on the
	// definition (never as names in the catalog-validated keys above — the
	// downgrade-safety contract). Same pseudo-list namespace: validation
	// guarantees a custom name can't collide with a catalog service.
	for i := range cfg.Blocking.CustomServices {
		cs := &cfg.Blocking.CustomServices[i]
		if cs.Blocked {
			for _, d := range cs.Domains {
				b.AddDeny("service:"+cs.Name, d)
			}
		}
		if cs.Allowed {
			for _, d := range services.CustomAllowDomains(cs) {
				b.AddAllow("service:"+cs.Name, d)
			}
		}
	}
	// iCloud Private Relay (opt-in): Apple documents that denying these
	// names makes devices fall back to normal DNS — the device shows
	// "Private Relay is unavailable on this network". A pseudo-list, so the
	// docket names it and the one-click pardon flow works on it like any rule.
	if cfg.Blocking.BlockICloudPrivateRelay {
		for _, d := range []string{"mask.icloud.com", "mask-h2.icloud.com", "mask-api.icloud.com"} {
			b.AddDeny("icloud-private-relay", d)
		}
	}

	// Audit sources compile into their own builder: attribution without
	// enforcement, and the enforcing matcher stays byte-identical.
	ab := filter.NewBuilder()

	m.mu.Lock()
	for _, src := range allSources(cfg) {
		if !src.Enabled {
			continue
		}
		body, ok := m.cached[src.Name]
		if !ok {
			continue
		}
		target := b
		if src.Audit && !src.allow {
			target = ab
		}
		stats, err := Parse(src.Format, src.Name, src.allow, bytes.NewReader(body), target)
		if err != nil {
			// Only a broken reader errors, and ours can't break; log anyway.
			slog.Error("list parse failed", "list", src.Name, "err", err)
		}
		if st := m.status[src.Name]; st != nil {
			st.Rules = stats.Rules
			st.Skipped = stats.Skipped
		}
		if stats.Skipped > 0 {
			slog.Warn("list contained unsupported or invalid rules",
				"list", src.Name, "skipped", stats.Skipped, "rules", stats.Rules)
		}
	}
	m.mu.Unlock()

	matcher := b.Build()
	audit := ab.Build()
	m.engine.Swap(matcher)
	m.auditEngine.Swap(audit)
	slog.Info("matcher rebuilt",
		"rules", matcher.Rules(), "allow_rules", matcher.AllowRules(),
		"audit_rules", audit.Rules(),
		"skipped", matcher.Skipped(), "took", time.Since(start).Round(time.Millisecond),
		"refetched", refetch)
}

func (m *Manager) setStatusFetched(src annotated) {
	st := m.ensureStatus(src)
	st.LastRefresh = time.Now()
	st.LastError = ""
}

func (m *Manager) setStatusError(src annotated, err error) {
	st := m.ensureStatus(src)
	st.LastError = err.Error()
}

func (m *Manager) ensureStatus(src annotated) *SourceStatus {
	st, ok := m.status[src.Name]
	if !ok {
		st = &SourceStatus{}
		m.status[src.Name] = st
	}
	st.Name, st.URL, st.Format, st.Enabled = src.Name, src.URL, src.Format, src.Enabled
	st.Action = src.actionLabel()
	st.Audit = src.Audit
	return st
}

// actionLabel names what the source's entries do, for the API.
func (a annotated) actionLabel() string {
	if a.allow {
		return "allow"
	}
	return "block"
}

// Status returns per-source state for the API, in config order (blocklists
// first, then allowlists).
func (m *Manager) Status() []SourceStatus {
	cfg := m.store.Get()
	m.mu.Lock()
	defer m.mu.Unlock()
	sources := allSources(cfg)
	out := make([]SourceStatus, 0, len(sources))
	for _, src := range sources {
		if st, ok := m.status[src.Name]; ok {
			s := *st
			s.Enabled = src.Enabled
			s.Action = src.actionLabel()
			s.Audit = src.Audit
			out = append(out, s)
		} else {
			out = append(out, SourceStatus{
				Name: src.Name, URL: src.URL, Format: src.Format,
				Action: src.actionLabel(), Enabled: src.Enabled, Audit: src.Audit,
			})
		}
	}
	return out
}

// refreshSource fetches one source (conditionally, when a body is already
// held) and records the outcome. A failure keeps the last good body.
func (m *Manager) refreshSource(ctx context.Context, src annotated) {
	m.mu.Lock()
	_, have := m.cached[src.Name]
	prev := m.meta[src.Name]
	m.mu.Unlock()
	if !have {
		prev = cacheMeta{} // no body to fall back on: must download it
	}
	res, err := m.fetch(ctx, src.URL, prev)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		slog.Warn("list fetch failed", "list", src.Name, "url", src.URL, "err", err)
		m.setStatusError(src, err)
		return
	}
	meta := cacheMeta{ETag: res.etag, LastModified: res.lastModified, FetchedAt: time.Now()}
	if res.notModified {
		if meta.ETag == "" {
			meta.ETag = prev.ETag
		}
		if meta.LastModified == "" {
			meta.LastModified = prev.LastModified
		}
		meta.SHA256 = prev.SHA256
		if m.disk != nil {
			if err := m.disk.touch(src.URL, meta); err != nil {
				slog.Debug("list cache touch", "list", src.Name, "err", err)
			}
		}
	} else {
		m.cached[src.Name] = res.body
		meta.SHA256 = bodyHash(res.body)
		if m.disk != nil {
			if err := m.disk.store(src.URL, res.body, meta); err != nil {
				slog.Warn("list cache write failed", "list", src.Name, "err", err)
			}
		}
	}
	m.meta[src.Name] = meta
	m.setStatusFetched(src)
}

// fetchResult is one download. notModified means a 304 answered a
// conditional request: the held body is current and body is nil.
type fetchResult struct {
	body         []byte
	notModified  bool
	etag         string
	lastModified string
}

// fetch downloads one list with a hard size cap and timeout. With validators
// in prev it asks conditionally, so an unchanged list costs a 304 instead of
// the whole download — most refreshes, for most lists.
func (m *Manager) fetch(ctx context.Context, url string, prev cacheMeta) (fetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetchResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", m.ua)
	if prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	if prev.LastModified != "" {
		req.Header.Set("If-Modified-Since", prev.LastModified)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fetchResult{}, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	res := fetchResult{etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified && (prev.ETag != "" || prev.LastModified != "") {
		res.notModified = true
		return res, nil
	}
	if resp.StatusCode != http.StatusOK {
		return fetchResult{}, fmt.Errorf("fetch: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return fetchResult{}, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxListBytes {
		return fetchResult{}, fmt.Errorf("list exceeds %d byte cap", maxListBytes)
	}
	res.body = body
	return res, nil
}
