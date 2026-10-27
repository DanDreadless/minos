// Package api exposes the REST + WebSocket surface consumed by the web UI
// and the CLI. Field names stay boring and literal ("blocked", "allowlist").
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"minos/internal/applog"
	"minos/internal/breakage"
	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/dnsproxy"
	"minos/internal/filter"
	"minos/internal/hostinfo"
	"minos/internal/lists"
	"minos/internal/notify"
	"minos/internal/querylog"
	"minos/internal/threats"
)

// ProxyStatsSource reports DNS-proxy counters (response cache, upstreams,
// DNSSEC validation); the dnsproxy server implements it. A nil source
// reports nothing.
type ProxyStatsSource interface {
	CacheStats() (hits, misses uint64, entries int64, enabled bool)
	UpstreamStats() []dnsproxy.UpstreamStat
	DNSSECStats() dnsproxy.DNSSECStat
}

// UpdateSource reports the newest known release; the updates.Checker
// implements it. A nil source reports nothing (check disabled or unwired).
type UpdateSource interface {
	Latest() (version string, available bool)
}

// HostSource reports the machine Minos runs on; hostinfo.Collector
// implements it. Nil when unwired, and Supported is false on container
// installs — both mean the same thing here: report no host health.
type HostSource interface {
	Supported() bool
	Info() hostinfo.Info
	Latest() *hostinfo.Sample
}

// Server wires the HTTP surface to the running components.
type Server struct {
	engine  *filter.Engine
	qlog    *querylog.Log
	store   *config.Store
	lists   *lists.Manager
	clients *clients.Registry
	cache   ProxyStatsSource // may be nil
	updates UpdateSource     // may be nil
	host    HostSource       // may be nil; nil or unsupported reports nothing
	static  fs.FS            // embedded web/dist; nil disables UI serving
	version string
	// installMethod is the build-time -ldflags stamp ("binary", "docker",
	// or "" for an unstamped source build); upgrade guidance refines it at
	// runtime — see resolveInstallMethod.
	installMethod string
	started       time.Time
	notifier      *notify.Notifier // may be nil
	login         *loginGuard
	appLog        *applog.Ring // may be nil: no Logs endpoints
	logLevel      *slog.LevelVar
	// updateHelper is where install.sh puts the root updater; its presence
	// is what turns the one-click update on (tests point it elsewhere).
	updateHelper string
	breakage     *breakage.Detector // may be nil: no suggestions
	threats      *threats.Detector  // may be nil: no threat findings
	hass         homeAssistant      // may be nil: no Home Assistant bridge
}

func New(engine *filter.Engine, qlog *querylog.Log, store *config.Store,
	mgr *lists.Manager, reg *clients.Registry, cache ProxyStatsSource,
	upd UpdateSource, host HostSource, static fs.FS, version, installMethod string,
) *Server {
	return &Server{
		engine:        engine,
		qlog:          qlog,
		store:         store,
		lists:         mgr,
		clients:       reg,
		cache:         cache,
		updates:       upd,
		host:          host,
		static:        static,
		version:       version,
		installMethod: installMethod,
		started:       time.Now(),
		login:         newLoginGuard(),
		updateHelper:  defaultUpdateHelper,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	// Prometheus convention puts /metrics at the root; it shares the API
	// auth (Prometheus scrape configs send Authorization: Bearer).
	r.With(s.auth).Get("/metrics", s.handleMetrics)
	// Login itself is reachable without logging in.
	r.Get("/api/auth", s.handleAuthStatus)
	r.Post("/api/auth/login", s.handleLogin)
	r.Post("/api/auth/logout", s.handleLogout)
	r.Route("/api", func(r chi.Router) {
		r.Use(s.auth)
		r.Put("/auth/password", s.handleSetPassword)
		r.Delete("/auth/password", s.handleRemovePassword)
		r.Post("/auth/signout-all", s.handleSignOutEverywhere)
		r.Post("/auth/totp/setup", s.handleTOTPSetup)
		r.Post("/auth/totp/enable", s.handleTOTPEnable)
		r.Delete("/auth/totp", s.handleTOTPDisable)
		r.Get("/status", s.handleStatus)
		r.Get("/host", s.handleHost)
		r.Get("/upstreams", s.handleUpstreams)
		r.Post("/upstreams/test", s.handleUpstreamTest)
		r.Get("/update", s.handleUpdate)
		r.Get("/update/progress", s.handleUpdateProgress)
		r.Post("/update/install", s.handleUpdateInstall)
		r.Post("/update/check", s.handleUpdateCheck)
		r.Get("/querylog", s.handleQueryLog)
		r.Get("/querylog/history", s.handleQueryLogHistory)
		r.Get("/querylog/export", s.handleQueryLogExport)
		r.Get("/querylog/lists", s.handleQueryLogLists)
		r.Get("/querylog/stream", s.handleQueryLogStream)
		r.Get("/stats", s.handleStats)
		r.Get("/stats/client", s.handleClientStats)
		r.Get("/stats/lists", s.handleListStats)
		r.Get("/check", s.handleCheck)
		r.Get("/config", s.handleGetConfig)
		r.Put("/config", s.handleUpdateConfig)
		r.Post("/notifications/test", s.handleNotificationTest)
		r.Get("/logs", s.handleLogs)
		r.Get("/logs/stream", s.handleLogsStream)
		r.Get("/logs/download", s.handleLogsDownload)
		r.Get("/config/export", s.handleExportConfig)
		r.Post("/config/import", s.handleImportConfig)
		r.Post("/import/pihole", s.handleImportPihole)
		r.Post("/import/adguard", s.handleImportAdGuard)
		r.Get("/services", s.handleGetServices)
		r.Put("/services", s.handleUpdateServices)
		r.Post("/services/custom", s.handleAddCustomService)
		r.Put("/services/custom/{name}", s.handleUpdateCustomService)
		r.Delete("/services/custom/{name}", s.handleDeleteCustomService)
		r.Get("/lists/value", s.handleListValue)
		r.Get("/privacy", s.handlePrivacy)
		r.Get("/summary", s.handleSummary)
		r.Post("/privacy/companies", s.handleBlockCompany)
		r.Get("/lists", s.handleLists)
		r.Post("/lists", s.handleAddList)
		r.Post("/lists/refresh", s.handleListsRefresh)
		r.Put("/lists/{name}", s.handleUpdateList)
		r.Delete("/lists/{name}", s.handleDeleteList)
		r.Get("/clients", s.handleGetClients)
		r.Put("/clients/{key}", s.handleUpdateClient)
		r.Delete("/clients/{key}", s.handleDeleteClient)
		r.Get("/groups", s.handleGetGroups)
		r.Post("/groups", s.handleAddGroup)
		r.Put("/groups/{name}", s.handleUpdateGroup)
		r.Delete("/groups/{name}", s.handleDeleteGroup)
		r.Get("/allowlist", s.handleGetDomains("allowlist"))
		r.Post("/allowlist", s.handleAddDomain("allowlist"))
		r.Delete("/allowlist/{domain}", s.handleDeleteDomain("allowlist"))
		r.Delete("/allowlist", s.handleDeleteDomain("allowlist")) // ?domain=, for /regex/ rules
		r.Get("/allowlist/temporary", s.handleGetTemporaryAllows)
		r.Get("/suggestions", s.handleSuggestions)
		r.Get("/threats", s.handleThreats)
		r.Get("/home-assistant/status", s.handleHomeAssistantStatus)
		r.Get("/settings/history", s.handleSettingsHistory)
		r.Get("/settings/history/{id}/diff", s.handleSettingsHistoryDiff)
		r.Post("/settings/history/{id}/restore", s.handleSettingsRestore)
		r.Post("/threats/dismiss", s.handleDismissThreat)
		r.Post("/threats/ignore", s.handleIgnoreThreatDomain)
		r.Post("/suggestions/dismiss", s.handleDismissSuggestion)
		r.Delete("/allowlist/temporary", s.handleDeleteTemporaryAllow) // ?domain=
		r.Get("/denylist", s.handleGetDomains("denylist"))
		r.Post("/denylist", s.handleAddDomain("denylist"))
		r.Delete("/denylist/{domain}", s.handleDeleteDomain("denylist"))
		r.Delete("/denylist", s.handleDeleteDomain("denylist"))
		r.Post("/pause", s.handlePause)
		r.Delete("/pause", s.handleResume)
	})
	if s.static != nil {
		r.NotFound(s.serveStatic)
	}
	return r
}

// auth enforces the API token when one is configured. Comparison is
// constant-time; weakening or widening this needs maintainer sign-off.
type statusResponse struct {
	Version        string     `json:"version"`
	UptimeSeconds  int64      `json:"uptime_seconds"`
	Paused         bool       `json:"paused"`
	PausedUntil    *time.Time `json:"paused_until,omitempty"`
	QueriesTotal   uint64     `json:"queries_total"`
	QueriesBlocked uint64     `json:"queries_blocked"`
	EntriesDropped uint64     `json:"entries_dropped"`
	Rules          int        `json:"rules"`
	AllowRules     int        `json:"allow_rules"`
	RulesSkipped   int        `json:"rules_skipped"`
	CacheEnabled   bool       `json:"cache_enabled"`
	CacheHits      uint64     `json:"cache_hits"`
	CacheMisses    uint64     `json:"cache_misses"`
	CacheEntries   int64      `json:"cache_entries"`
	// LatestVersion is set once an opt-in update check has succeeded.
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	// DNSSEC is omitted entirely when validation is off, so the UI hides
	// the card on absence rather than needing a separate enabled flag —
	// the same gate /metrics uses.
	DNSSEC *dnssecStatus `json:"dnssec,omitempty"`
	// LogAlertSeq is the sequence number of the newest warning or error in
	// the application log (0: none since start). The UI badges the Logs
	// link while it is above the last one the user has looked at.
	LogAlertSeq uint64 `json:"log_alert_seq"`
}

// dnssecStatus is the validation scoreboard for the dashboard. Counters
// are process-lifetime totals (they reset on restart), which is why the
// windowed "would have been blocked" figure lives on /stats instead —
// these two must not be presented as if they covered the same period.
type dnssecStatus struct {
	Mode          string `json:"mode"`
	Secure        uint64 `json:"secure"`
	Insecure      uint64 `json:"insecure"`
	Bogus         uint64 `json:"bogus"`
	Indeterminate uint64 `json:"indeterminate"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total, blocked, dropped := s.qlog.Stats()
	paused, until := s.engine.Paused()
	m := s.engine.Current()
	resp := statusResponse{
		Version:        s.version,
		UptimeSeconds:  int64(time.Since(s.started).Seconds()),
		Paused:         paused,
		QueriesTotal:   total,
		QueriesBlocked: blocked,
		EntriesDropped: dropped,
		Rules:          m.Rules(),
		AllowRules:     m.AllowRules(),
		RulesSkipped:   m.Skipped(),
	}
	if s.cache != nil {
		resp.CacheHits, resp.CacheMisses, resp.CacheEntries, resp.CacheEnabled = s.cache.CacheStats()
		if d := s.cache.DNSSECStats(); d.Mode != "off" {
			resp.DNSSEC = &dnssecStatus{
				Mode:          d.Mode,
				Secure:        d.Secure,
				Insecure:      d.Insecure,
				Bogus:         d.Bogus,
				Indeterminate: d.Indeterminate,
			}
		}
	}
	if s.updates != nil {
		resp.LatestVersion, resp.UpdateAvailable = s.updates.Latest()
	}
	if paused && !until.IsZero() {
		resp.PausedUntil = &until
	}
	if s.appLog != nil {
		resp.LogAlertSeq = s.appLog.AlertSeq()
	}
	writeJSON(w, http.StatusOK, resp)
}

// upstreamHealth is one upstream's live health for the UI. Address matches
// the configured upstream address (the breaker's counters are keyed on it).
// Sick means the failover breaker is currently sidestepping the upstream;
// zero Requests means it simply hasn't been needed since start — healthy
// primaries starve the backups, which is normal, not an outage.
type upstreamHealth struct {
	Address  string  `json:"address"`
	Requests uint64  `json:"requests"`
	Failures uint64  `json:"failures"`
	AvgMs    float64 `json:"avg_ms"`
	// Sick: breaker tripped and not yet recovered (the whole outage).
	Sick bool `json:"sick"`
	// State: unused | healthy | failing — the one word the UI shows.
	State       string     `json:"state"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastFailure *time.Time `json:"last_failure,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
}

func (s *Server) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	out := []upstreamHealth{}
	if s.cache != nil {
		for _, st := range s.cache.UpstreamStats() {
			h := upstreamHealth{
				Address:   st.Name,
				Requests:  st.Requests,
				Failures:  st.Failures,
				Sick:      st.Sick,
				State:     string(st.State),
				LastError: st.LastError,
			}
			if !st.LastSuccess.IsZero() {
				h.LastSuccess = &st.LastSuccess
			}
			if !st.LastFailure.IsZero() {
				h.LastFailure = &st.LastFailure
			}
			if st.Requests > 0 {
				h.AvgMs = st.DurationSeconds / float64(st.Requests) * 1000
			}
			out = append(out, h)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// upstreamTester is the proxy's TestUpstream; optional so stats-only fakes
// need not implement it.
type upstreamTester interface {
	TestUpstream(ctx context.Context, u config.Upstream) dnsproxy.UpstreamTest
}

type upstreamTestResult struct {
	OK    bool    `json:"ok"`
	Ms    float64 `json:"ms"`
	Rcode string  `json:"rcode,omitempty"`
	Error string  `json:"error,omitempty"`
	// Configured: one Minos uses, so its health light was updated too.
	Configured bool `json:"configured"`
}

// handleUpstreamTest asks one upstream a test question now. The body is an
// upstream (address + protocol), saved or not.
func (s *Server) handleUpstreamTest(w http.ResponseWriter, r *http.Request) {
	var u config.Upstream
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with address and protocol")
		return
	}
	if err := config.ValidateUpstream(u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, ok := s.cache.(upstreamTester)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "upstream testing is not available")
		return
	}
	res := t.TestUpstream(r.Context(), u)
	writeJSON(w, http.StatusOK, upstreamTestResult{
		OK:         res.OK,
		Ms:         float64(res.RTT.Microseconds()) / 1000,
		Rcode:      res.Rcode,
		Error:      res.Err,
		Configured: res.Configured,
	})
}

func (s *Server) handleQueryLog(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			writeError(w, http.StatusBadRequest, "limit must be 1-10000")
			return
		}
		limit = n
	}
	entries := s.qlog.Recent(limit)
	if entries == nil {
		entries = []querylog.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleQueryLogHistory serves the persisted query log filtered by client/
// domain substring and verdict, newest first, paginated by `before` (unix
// millis). It backs the Docket drill-downs and search, which must span the
// full retained history — not just the live ring buffer. Returns [] in
// ephemeral mode (no SQLite); the frontend then filters the live ring.
func (s *Server) handleQueryLogHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 200
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be 1-1000")
			return
		}
		limit = n
	}
	var before time.Time
	if v := q.Get("before"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ms < 0 {
			writeError(w, http.StatusBadRequest, "before must be unix milliseconds")
			return
		}
		before = time.UnixMilli(ms)
	}
	filter, err := historyFilter(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.SearchClients = s.searchClients(filter.Search)
	entries, err := s.qlog.QueryHistory(r.Context(), filter, limit, before)
	if errors.Is(err, querylog.ErrSearchTimeout) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []querylog.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleQueryLogLists serves the distinct list names attributed in the
// window (enforcing and audit), for the Docket's list-filter dropdown.
func (s *Server) handleQueryLogLists(w http.ResponseWriter, r *http.Request) {
	hours := 168
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 2160 {
			writeError(w, http.StatusBadRequest, "hours must be 1-2160")
			return
		}
		hours = n
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	names, err := s.qlog.ListNames(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleQueryLogStream(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error response
	}
	defer func() { _ = conn.Close(websocket.StatusInternalError, "stream closed") }()

	ch, cancel := s.qlog.Subscribe()
	defer cancel()

	ctx := r.Context()
	// Reads only serve to detect the client going away.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		case e := <-ch:
			if err := wsjson.Write(ctx, conn, e); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleLists(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.lists.Status())
}

func (s *Server) handleListsRefresh(w http.ResponseWriter, r *http.Request) {
	// Refresh synchronously so the caller sees updated counts on return;
	// list downloads are capped and time-limited so this stays bounded.
	s.lists.Refresh(r.Context())
	writeJSON(w, http.StatusOK, s.lists.Status())
}

func (s *Server) domains(kind string, c *config.Config) *[]string {
	if kind == "allowlist" {
		return &c.Blocking.Allowlist
	}
	return &c.Blocking.Denylist
}

func (s *Server) handleGetDomains(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Get()
		out := *s.domains(kind, cfg)
		if out == nil {
			out = []string{}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) handleAddDomain(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
			// Minutes, on the allowlist only, makes the allow temporary.
			Minutes int `json:"minutes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "body must be JSON: {\"domain\": \"...\"}")
			return
		}
		if body.Minutes != 0 {
			if kind != "allowlist" {
				writeError(w, http.StatusBadRequest, "minutes applies to allowed domains only")
				return
			}
			s.addTemporaryAllow(w, r, body.Domain, body.Minutes)
			return
		}
		norm, err := domainOrRegex(body.Domain)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		err = s.update(r, func(c *config.Config) error {
			list := s.domains(kind, c)
			for _, d := range *list {
				if d == norm {
					return errAlreadyExists
				}
			}
			*list = append(*list, norm)
			if kind == "allowlist" {
				// A permanent allow supersedes a temporary one.
				c.Blocking.TemporaryAllows = withoutTemporary(c.Blocking.TemporaryAllows, norm)
			}
			return nil
		})
		if err == nil && kind == "allowlist" && s.breakage != nil {
			s.breakage.Resolve(norm)
		}
		if errors.Is(err, errAlreadyExists) {
			writeJSON(w, http.StatusOK, map[string]string{"domain": norm, "status": "unchanged"})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"domain": norm, "status": "added"})
	}
}

var errAlreadyExists = errors.New("already present")

// addTemporaryAllow allows domain for minutes, replacing any earlier
// temporary allow for it. A domain already allowed for good is left alone.
func (s *Server) addTemporaryAllow(w http.ResponseWriter, r *http.Request, domain string, minutes int) {
	norm := filter.NormalizeDomain(strings.TrimSpace(domain))
	if norm == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a valid domain (temporary allows take a domain, not a /regex/ rule)", domain))
		return
	}
	d := time.Duration(minutes) * time.Minute
	if d <= 0 || d > config.MaxTemporaryAllow {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("minutes must be 1-%d", int(config.MaxTemporaryAllow/time.Minute)))
		return
	}
	until := time.Now().Add(d).UTC().Truncate(time.Second)
	err := s.update(r, func(c *config.Config) error {
		for _, a := range c.Blocking.Allowlist {
			if a == norm {
				return errAlreadyExists
			}
		}
		c.Blocking.TemporaryAllows = append(withoutTemporary(c.Blocking.TemporaryAllows, norm),
			config.TemporaryAllow{Domain: norm, Until: until})
		return nil
	})
	if s.breakage != nil && (err == nil || errors.Is(err, errAlreadyExists)) {
		s.breakage.Resolve(norm)
	}
	if errors.Is(err, errAlreadyExists) {
		writeJSON(w, http.StatusOK, map[string]string{"domain": norm, "status": "unchanged"})
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"domain": norm, "status": "added", "until": until})
}

func withoutTemporary(list []config.TemporaryAllow, domain string) []config.TemporaryAllow {
	out := make([]config.TemporaryAllow, 0, len(list))
	for _, t := range list {
		if t.Domain != domain {
			out = append(out, t)
		}
	}
	return out
}

func (s *Server) handleGetTemporaryAllows(w http.ResponseWriter, _ *http.Request) {
	out := s.store.Get().Blocking.ActiveTemporaryAllows(time.Now())
	if out == nil {
		out = []config.TemporaryAllow{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteTemporaryAllow(w http.ResponseWriter, r *http.Request) {
	norm := filter.NormalizeDomain(r.URL.Query().Get("domain"))
	found := false
	err := s.update(r, func(c *config.Config) error {
		kept := withoutTemporary(c.Blocking.TemporaryAllows, norm)
		found = len(kept) != len(c.Blocking.TemporaryAllows)
		c.Blocking.TemporaryAllows = kept
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("%q is not allowed temporarily", norm))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"domain": norm, "status": "removed"})
}

// domainOrRegex accepts a domain (normalized) or a /pattern/ regex rule
// (kept as written, after checking it compiles as RE2).
func domainOrRegex(s string) (string, error) {
	s = strings.TrimSpace(s)
	if filter.IsRegexRule(s) {
		if _, err := filter.CompileRegexRule(s); err != nil {
			return "", fmt.Errorf("%s: %v", s, err)
		}
		return s, nil
	}
	norm := filter.NormalizeDomain(s)
	if norm == "" {
		return "", fmt.Errorf("%q is not a valid domain (or a /regex/ rule)", s)
	}
	return norm, nil
}

func (s *Server) handleDeleteDomain(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("domain")
		if raw == "" {
			raw = chi.URLParam(r, "domain")
		}
		norm, err := domainOrRegex(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		found := false
		err = s.update(r, func(c *config.Config) error {
			list := s.domains(kind, c)
			kept := (*list)[:0]
			for _, d := range *list {
				if d == norm {
					found = true
					continue
				}
				kept = append(kept, d)
			}
			*list = kept
			return nil
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("%q is not in the %s", norm, kind))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"domain": norm, "status": "removed"})
	}
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Duration string `json:"duration"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"duration\": \"5m\"} (empty = indefinite)")
		return
	}
	var d time.Duration
	if body.Duration != "" {
		var err error
		d, err = time.ParseDuration(body.Duration)
		if err != nil || d < 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid duration %q", body.Duration))
			return
		}
	}
	s.engine.Pause(d)
	s.homeAssistantChanged()
	paused, until := s.engine.Paused()
	resp := map[string]any{"paused": paused}
	if !until.IsZero() {
		resp["paused_until"] = until
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.engine.Resume()
	s.homeAssistantChanged()
	writeJSON(w, http.StatusOK, map[string]any{"paused": false})
}

// serveStatic serves the embedded frontend, falling back to index.html for
// client-side routes.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.static.Open(path); err == nil {
		f.Close()
		http.FileServerFS(s.static).ServeHTTP(w, r)
		return
	}
	// SPA fallback.
	index, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response failed", "err", err)
	}
}

// writeError is always plain and literal — no lore in error messages.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
