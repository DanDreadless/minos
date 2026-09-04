package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"minos/internal/dnsproxy"
	"minos/internal/filter"
	"minos/internal/querylog"
)

// statsDeadline caps the dashboard aggregates. Timeline, top-domains and
// top-clients each walk every row in the window — no index answers "group
// the last 90 days by domain" — so a wide window is genuinely expensive:
// 90s over the 7.4M-row log this was measured against. Reads have their own
// connection pool now, so a slow one no longer stalls the writer or the
// rest of the API, but it can still hold a browser open long past the point
// the user gave up. The deadline turns waiting into an answer, and the
// driver's context cancellation is a real sqlite3_interrupt, so the work
// actually stops rather than running on unwatched.
const statsDeadline = 20 * time.Second

// statsTimeout is what the caller sees when an aggregate outruns the
// deadline. Actionable, because the fix genuinely is a shorter window.
func statsTimeout(hours int) string {
	return fmt.Sprintf(
		"that window (%dh) is too much history to summarise in one request — try a shorter one",
		hours)
}

// writeStatsErr reports an aggregate failure, distinguishing "took too
// long" from "went wrong": the first is the user's window choice and is
// fixable by them, the second is ours.
func writeStatsErr(ctx context.Context, w http.ResponseWriter, err error, hours int) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusServiceUnavailable, statsTimeout(hours))
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

type statsResponse struct {
	WindowHours int                       `json:"window_hours"`
	Timeline    []querylog.TimelineBucket `json:"timeline"`
	TopBlocked  []querylog.TopDomain      `json:"top_blocked"`
	TopClients  []querylog.ClientStat     `json:"top_clients"`
	// DNSSEC is present only while permissive mode is recording audit
	// marks — the windowed answer to "what would enforce refuse?", which
	// the process-lifetime counters on /status cannot give.
	DNSSEC *dnssecAudit `json:"dnssec,omitempty"`
}

// dnssecAudit is the windowed would-block picture for the dashboard card:
// how many answers permissive mode let through that enforce would have
// refused, and which domains they were.
type dnssecAudit struct {
	WouldBlock int                  `json:"would_block"`
	TopDomains []querylog.TopDomain `json:"top_domains"`
}

// handleStats aggregates the query log for the dashboard: a bucketed
// timeline plus top blocked domains and busiest clients.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 2160 {
			writeError(w, http.StatusBadRequest, "hours must be 1-2160")
			return
		}
		hours = n
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	bucket := 10 * time.Minute
	if hours > 24 {
		bucket = time.Hour
	}
	if hours > 168 {
		bucket = 24 * time.Hour // month-scale windows chart per day
	}
	ctx, cancel := context.WithTimeout(r.Context(), statsDeadline)
	defer cancel()
	timeline, err := s.qlog.Timeline(ctx, since, bucket)
	if err != nil {
		writeStatsErr(ctx, w, err, hours)
		return
	}
	topBlocked, err := s.qlog.TopBlockedDomains(ctx, since, 10)
	if err != nil {
		writeStatsErr(ctx, w, err, hours)
		return
	}
	topClients, err := s.qlog.TopClients(ctx, since, 10)
	if err != nil {
		writeStatsErr(ctx, w, err, hours)
		return
	}
	if topBlocked == nil {
		topBlocked = []querylog.TopDomain{}
	}
	if topClients == nil {
		topClients = []querylog.ClientStat{}
	}
	out := statsResponse{
		WindowHours: hours,
		Timeline:    timeline,
		TopBlocked:  topBlocked,
		TopClients:  topClients,
	}
	// Reported whenever validation is on, not only in permissive mode:
	// the mode is live-swappable, so a window that begins in permissive
	// and ends in enforce still holds marks worth showing. In steady-state
	// enforce the figures are simply zero — enforced refusals are blocks,
	// and the Codex list stats already count them under "dnssec".
	if s.cache != nil && s.cache.DNSSECStats().Mode != "off" {
		total, err := s.qlog.AuditedTotal(ctx, since, dnsproxy.ListDNSSEC)
		if err != nil {
			writeStatsErr(ctx, w, err, hours)
			return
		}
		top, err := s.qlog.TopAuditedDomains(ctx, since, dnsproxy.ListDNSSEC, 10)
		if err != nil {
			writeStatsErr(ctx, w, err, hours)
			return
		}
		if top == nil {
			top = []querylog.TopDomain{}
		}
		out.DNSSEC = &dnssecAudit{WouldBlock: total, TopDomains: top}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleClientStats aggregates one device's traffic for the client
// drill-down panel: totals plus top allowed and blocked domains. `client`
// is required and comma-separated (a MAC-merged device spans several IPs),
// matching the querylog/history filter's convention.
func (s *Server) handleClientStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var clients []string
	for _, part := range strings.Split(q.Get("client"), ",") {
		if part = strings.TrimSpace(part); part != "" {
			clients = append(clients, part)
		}
		if len(clients) >= 32 {
			break
		}
	}
	if len(clients) == 0 {
		writeError(w, http.StatusBadRequest, "client is required (comma-separated addresses)")
		return
	}
	hours := 24
	if v := q.Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 2160 {
			writeError(w, http.StatusBadRequest, "hours must be 1-2160")
			return
		}
		hours = n
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	ctx, cancel := context.WithTimeout(r.Context(), statsDeadline)
	defer cancel()
	overview, err := s.qlog.ClientOverview(ctx, clients, since, 10)
	if err != nil {
		writeStatsErr(ctx, w, err, hours)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		WindowHours int `json:"window_hours"`
		querylog.ClientOverview
	}{hours, overview})
}

// handleListStats reports how many blocks each list is responsible for —
// "is this list earning its keep" on the lists page. Defaults to a 7-day
// window, the widest the stats endpoints allow.
func (s *Server) handleListStats(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), statsDeadline)
	defer cancel()
	stats, err := s.qlog.BlocksByList(ctx, since)
	if err != nil {
		writeStatsErr(ctx, w, err, hours)
		return
	}
	if stats == nil {
		stats = []querylog.ListStat{}
	}
	writeJSON(w, http.StatusOK, struct {
		WindowHours int                 `json:"window_hours"`
		Lists       []querylog.ListStat `json:"lists"`
	}{hours, stats})
}

// handleCheck judges a domain against the compiled rules and reports which
// list and rule decide its fate. It consults the matcher directly, so the
// answer reflects the rules even while blocking is paused.
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	norm := filter.NormalizeDomain(domain)
	if norm == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a valid domain", domain))
		return
	}
	res := s.engine.Current().Match(norm)
	verdict := querylog.VerdictAllowed
	if res.Blocked {
		verdict = querylog.VerdictBlocked
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"domain":  norm,
		"verdict": verdict,
		"list":    res.List,
		"rule":    res.Rule,
	})
}
