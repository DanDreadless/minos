package lists

import (
	"bytes"
	"context"
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
	Name    string `json:"name"`
	URL     string `json:"url"`
	Format  string `json:"format"`
	Action  string `json:"action"` // "block" or "allow" (empty config = block)
	Enabled bool   `json:"enabled"`
	Audit   bool   `json:"audit"` // rules logged as "would block", never enforced
	Rules   int    `json:"rules"`
	Skipped int    `json:"skipped"`
	// LastRefresh is the last time the publisher was successfully reached
	// (a 200 or a 304) — the age of the copy in force.
	LastRefresh time.Time `json:"last_refresh,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	// State is the one word for this list's health (see ListState).
	State ListState `json:"state"`
	// LastAttempt is the most recent fetch attempt, successful or not.
	LastAttempt         time.Time `json:"last_attempt,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
	// ServedBy is the mirror URL the copy in force came from (empty = the
	// primary); PrimaryError is why the primary was passed over.
	ServedBy     string   `json:"served_by,omitempty"`
	PrimaryError string   `json:"primary_error,omitempty"`
	Mirrors      []string `json:"mirrors,omitempty"`

	failKind failKind
	hasBody  bool
}

// ListState is the single health verdict the API reports per list, so the
// UI never has to infer one from timestamps and error strings.
type ListState string

const (
	ListPending      ListState = "pending"      // not fetched yet
	ListOK           ListState = "ok"           // last attempt succeeded
	ListFailing      ListState = "failing"      // failing; a recent copy is in force
	ListStale        ListState = "stale"        // failing for over two refresh intervals
	ListUnavailable  ListState = "unavailable"  // failing and no copy at all: not blocking
	ListRetired      ListState = "retired"      // 404/410: the publisher removed it
	ListUnauthorized ListState = "unauthorized" // 401/403: the feed now needs credentials
	ListSuspicious   ListState = "suspicious"   // download refused by vetting; previous copy kept
	ListDisabled     ListState = "disabled"
)

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

	mu      sync.Mutex
	cached  map[string][]byte    // source name → last good raw body
	meta    map[string]cacheMeta // source name → validators for conditional GET
	status  map[string]*SourceStatus
	rejects map[string]rejection // source name → body vetting refused
	disk    *diskCache           // nil = memory only (ephemeral mode, or not configured)

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
		rejects:     make(map[string]rejection),
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
		st.ServedBy = meta.Source
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
// Sources that failed transiently are retried sooner, on a backoff schedule,
// without refetching the healthy ones.
func (m *Manager) Run(ctx context.Context) {
	// Compile what's on disk first: blocking is live from the first second,
	// before (and regardless of) the network.
	if m.loadFromDisk() {
		m.rebuild(ctx, false)
	}
	m.Refresh(ctx)
	next := time.Now().Add(m.store.Get().Lists.RefreshInterval.Std())
	for {
		interval := m.store.Get().Lists.RefreshInterval.Std()
		wait := time.Until(next)
		retry := m.retryDelay(interval)
		retrying := retry > 0 && retry < wait
		if retrying {
			wait = retry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			if retrying {
				m.retryFailing(ctx)
			} else {
				m.Refresh(ctx)
				next = time.Now().Add(interval)
			}
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
		var todo []annotated
		for _, src := range allSources(cfg) {
			keep[src.URL] = true // a disabled list keeps its cache for re-enable
			if src.Enabled {
				todo = append(todo, src)
			}
		}
		m.refreshAll(ctx, todo)
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

// fetchConcurrency bounds parallel downloads: enough that one slow
// publisher doesn't hold the rest up, few enough to be gentle on a Pi's
// network and memory (bodies are held until the rebuild).
const fetchConcurrency = 2

func (m *Manager) refreshAll(ctx context.Context, srcs []annotated) {
	sem := make(chan struct{}, fetchConcurrency)
	var wg sync.WaitGroup
	for _, src := range srcs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			m.refreshSource(ctx, src)
		}()
	}
	wg.Wait()
}

// retryFailing refetches only the sources whose last attempt failed
// transiently, and rebuilds if any of them recovered.
func (m *Manager) retryFailing(ctx context.Context) {
	var todo []annotated
	m.mu.Lock()
	for _, src := range allSources(m.store.Get()) {
		if st := m.status[src.Name]; src.Enabled && st != nil && st.failKind == failTransient {
			todo = append(todo, src)
		}
	}
	m.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	m.refreshAll(ctx, todo)
	m.mu.Lock()
	recovered := false
	for _, src := range todo {
		if st := m.status[src.Name]; st != nil && st.failKind == failNone {
			recovered = true
		}
	}
	m.mu.Unlock()
	if recovered {
		m.rebuild(ctx, false)
	}
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
	interval := cfg.Lists.RefreshInterval.Std()
	for _, src := range sources {
		var s SourceStatus
		if st, ok := m.status[src.Name]; ok {
			s = *st
		} else {
			s = SourceStatus{Name: src.Name, URL: src.URL, Format: src.Format}
		}
		s.URL = src.URL
		s.Mirrors = append([]string(nil), src.Mirrors...)
		s.Enabled = src.Enabled
		s.Action = src.actionLabel()
		s.Audit = src.Audit
		_, s.hasBody = m.cached[src.Name]
		s.State = s.state(interval, time.Now())
		out = append(out, s)
	}
	return out
}

func (s *SourceStatus) state(interval time.Duration, now time.Time) ListState {
	switch {
	case !s.Enabled:
		return ListDisabled
	case s.failKind == failRetired:
		return ListRetired
	case s.failKind == failUnauthorized:
		return ListUnauthorized
	case s.failKind == failSuspicious:
		return ListSuspicious
	case s.failKind == failTransient && !s.hasBody:
		return ListUnavailable
	case s.failKind == failTransient && now.Sub(s.LastRefresh) > 2*interval:
		return ListStale
	case s.failKind == failTransient:
		return ListFailing
	case s.LastAttempt.IsZero() && s.LastRefresh.IsZero():
		return ListPending
	}
	return ListOK
}
