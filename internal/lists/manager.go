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
	userAgent    = "minos-dns/0.1 (+https://github.com/minos)"
)

// SourceStatus is what the API reports per configured list.
type SourceStatus struct {
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Format      string    `json:"format"`
	Enabled     bool      `json:"enabled"`
	Rules       int       `json:"rules"`
	Skipped     int       `json:"skipped"`
	LastRefresh time.Time `json:"last_refresh,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

// Manager owns list fetching and matcher rebuilds. A rebuild always
// constructs a complete new matcher off the hot path, then swaps it into
// the engine atomically — a live matcher is never mutated.
type Manager struct {
	engine *filter.Engine
	store  *config.Store
	client *http.Client

	mu     sync.Mutex
	cached map[string][]byte // source name → last good raw body
	status map[string]*SourceStatus

	refreshNow chan struct{}
}

func NewManager(engine *filter.Engine, store *config.Store) *Manager {
	m := &Manager{
		engine:     engine,
		store:      store,
		client:     &http.Client{Timeout: fetchTimeout},
		cached:     make(map[string][]byte),
		status:     make(map[string]*SourceStatus),
		refreshNow: make(chan struct{}, 1),
	}
	// Config changes (new pardons/sentences, list edits) rebuild from cache
	// immediately — no refetch needed, no restart ever.
	store.OnChange(func(*config.Config) { m.TriggerRebuild() })
	return m
}

// Run blocks: initial refresh, then periodic refreshes until ctx ends.
func (m *Manager) Run(ctx context.Context) {
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

// EnsureFetched downloads only enabled sources that have no cached body yet
// (freshly added or URL-changed lists), then rebuilds. Cheaper than a full
// Refresh after a config edit.
func (m *Manager) EnsureFetched(ctx context.Context) {
	cfg := m.store.Get()
	for _, src := range cfg.Lists.Sources {
		if !src.Enabled {
			continue
		}
		m.mu.Lock()
		_, have := m.cached[src.Name]
		m.mu.Unlock()
		if have {
			continue
		}
		body, err := m.fetch(ctx, src.URL)
		m.mu.Lock()
		if err != nil {
			slog.Warn("list fetch failed", "list", src.Name, "url", src.URL, "err", err)
			m.setStatusError(src, err)
		} else {
			m.cached[src.Name] = body
			m.setStatusFetched(src)
		}
		m.mu.Unlock()
	}
	m.rebuild(ctx, false)
}

// Forget drops the cached body and status for a source, forcing a refetch on
// the next EnsureFetched/Refresh. Used when a list is removed or its URL edited.
func (m *Manager) Forget(name string) {
	m.mu.Lock()
	delete(m.cached, name)
	delete(m.status, name)
	m.mu.Unlock()
}

func (m *Manager) rebuild(ctx context.Context, refetch bool) {
	cfg := m.store.Get()
	start := time.Now()

	if refetch {
		for _, src := range cfg.Lists.Sources {
			if !src.Enabled {
				continue
			}
			body, err := m.fetch(ctx, src.URL)
			m.mu.Lock()
			if err != nil {
				slog.Warn("list fetch failed", "list", src.Name, "url", src.URL, "err", err)
				m.setStatusError(src, err)
			} else {
				m.cached[src.Name] = body
				m.setStatusFetched(src)
			}
			m.mu.Unlock()
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

	m.mu.Lock()
	for _, src := range cfg.Lists.Sources {
		if !src.Enabled {
			continue
		}
		body, ok := m.cached[src.Name]
		if !ok {
			continue
		}
		stats, err := Parse(src.Format, src.Name, bytes.NewReader(body), b)
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
	m.engine.Swap(matcher)
	slog.Info("matcher rebuilt",
		"rules", matcher.Rules(), "allow_rules", matcher.AllowRules(),
		"skipped", matcher.Skipped(), "took", time.Since(start).Round(time.Millisecond),
		"refetched", refetch)
}

func (m *Manager) setStatusFetched(src config.ListSource) {
	st := m.ensureStatus(src)
	st.LastRefresh = time.Now()
	st.LastError = ""
}

func (m *Manager) setStatusError(src config.ListSource, err error) {
	st := m.ensureStatus(src)
	st.LastError = err.Error()
}

func (m *Manager) ensureStatus(src config.ListSource) *SourceStatus {
	st, ok := m.status[src.Name]
	if !ok {
		st = &SourceStatus{}
		m.status[src.Name] = st
	}
	st.Name, st.URL, st.Format, st.Enabled = src.Name, src.URL, src.Format, src.Enabled
	return st
}

// Status returns per-source state for the API, in config order.
func (m *Manager) Status() []SourceStatus {
	cfg := m.store.Get()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SourceStatus, 0, len(cfg.Lists.Sources))
	for _, src := range cfg.Lists.Sources {
		if st, ok := m.status[src.Name]; ok {
			s := *st
			s.Enabled = src.Enabled
			out = append(out, s)
		} else {
			out = append(out, SourceStatus{
				Name: src.Name, URL: src.URL, Format: src.Format, Enabled: src.Enabled,
			})
		}
	}
	return out
}

// fetch downloads one list with a hard size cap and timeout.
func (m *Manager) fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxListBytes {
		return nil, fmt.Errorf("list exceeds %d byte cap", maxListBytes)
	}
	return body, nil
}
