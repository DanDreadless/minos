// Package api exposes the REST + WebSocket surface consumed by the web UI
// and the CLI. Field names stay boring and literal ("blocked", "allowlist");
// the lore lives in the frontend.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/lists"
	"minos/internal/querylog"
)

// Server wires the HTTP surface to the running components.
type Server struct {
	engine  *filter.Engine
	qlog    *querylog.Log
	store   *config.Store
	lists   *lists.Manager
	clients *clients.Registry
	static  fs.FS // embedded web/dist; nil disables UI serving
	version string
	started time.Time
}

func New(engine *filter.Engine, qlog *querylog.Log, store *config.Store,
	mgr *lists.Manager, reg *clients.Registry, static fs.FS, version string,
) *Server {
	return &Server{
		engine:  engine,
		qlog:    qlog,
		store:   store,
		lists:   mgr,
		clients: reg,
		static:  static,
		version: version,
		started: time.Now(),
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Route("/api", func(r chi.Router) {
		r.Use(s.auth)
		r.Get("/status", s.handleStatus)
		r.Get("/querylog", s.handleQueryLog)
		r.Get("/querylog/stream", s.handleQueryLogStream)
		r.Get("/stats", s.handleStats)
		r.Get("/check", s.handleCheck)
		r.Get("/config", s.handleGetConfig)
		r.Put("/config", s.handleUpdateConfig)
		r.Get("/config/export", s.handleExportConfig)
		r.Get("/lists", s.handleLists)
		r.Post("/lists", s.handleAddList)
		r.Post("/lists/refresh", s.handleListsRefresh)
		r.Put("/lists/{name}", s.handleUpdateList)
		r.Delete("/lists/{name}", s.handleDeleteList)
		r.Get("/clients", s.handleGetClients)
		r.Put("/clients/{ip}", s.handleUpdateClient)
		r.Delete("/clients/{ip}", s.handleDeleteClient)
		r.Get("/groups", s.handleGetGroups)
		r.Post("/groups", s.handleAddGroup)
		r.Put("/groups/{name}", s.handleUpdateGroup)
		r.Delete("/groups/{name}", s.handleDeleteGroup)
		r.Get("/allowlist", s.handleGetDomains("allowlist"))
		r.Post("/allowlist", s.handleAddDomain("allowlist"))
		r.Delete("/allowlist/{domain}", s.handleDeleteDomain("allowlist"))
		r.Get("/denylist", s.handleGetDomains("denylist"))
		r.Post("/denylist", s.handleAddDomain("denylist"))
		r.Delete("/denylist/{domain}", s.handleDeleteDomain("denylist"))
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
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.store.Get().API.Token
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Api-Token")
		if got == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				got = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if got == "" {
			// WebSocket clients can't set headers from a browser; allow the
			// token as a query parameter for the stream endpoint only.
			if strings.HasSuffix(r.URL.Path, "/querylog/stream") {
				got = r.URL.Query().Get("token")
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid API token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	if paused && !until.IsZero() {
		resp.PausedUntil = &until
	}
	writeJSON(w, http.StatusOK, resp)
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

func (s *Server) handleQueryLogStream(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error response
	}
	defer conn.Close(websocket.StatusInternalError, "stream closed")

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
			conn.Close(websocket.StatusNormalClosure, "")
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
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "body must be JSON: {\"domain\": \"...\"}")
			return
		}
		norm := filter.NormalizeDomain(body.Domain)
		if norm == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a valid domain", body.Domain))
			return
		}
		err := s.store.Update(func(c *config.Config) error {
			list := s.domains(kind, c)
			for _, d := range *list {
				if d == norm {
					return errAlreadyExists
				}
			}
			*list = append(*list, norm)
			return nil
		})
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

func (s *Server) handleDeleteDomain(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		norm := filter.NormalizeDomain(chi.URLParam(r, "domain"))
		if norm == "" {
			writeError(w, http.StatusBadRequest, "invalid domain")
			return
		}
		found := false
		err := s.store.Update(func(c *config.Config) error {
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
	paused, until := s.engine.Paused()
	resp := map[string]any{"paused": paused}
	if !until.IsZero() {
		resp["paused_until"] = until
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.engine.Resume()
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
