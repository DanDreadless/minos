package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"minos/internal/filter"
	"minos/internal/querylog"
)

type statsResponse struct {
	WindowHours int                       `json:"window_hours"`
	Timeline    []querylog.TimelineBucket `json:"timeline"`
	TopBlocked  []querylog.TopDomain      `json:"top_blocked"`
	TopClients  []querylog.ClientStat     `json:"top_clients"`
}

// handleStats aggregates the query log for the dashboard: a bucketed
// timeline plus top blocked domains and busiest clients.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 168 {
			writeError(w, http.StatusBadRequest, "hours must be 1-168")
			return
		}
		hours = n
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	bucket := 10 * time.Minute
	if hours > 24 {
		bucket = time.Hour
	}
	ctx := r.Context()
	timeline, err := s.qlog.Timeline(ctx, since, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	topBlocked, err := s.qlog.TopBlockedDomains(ctx, since, 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	topClients, err := s.qlog.TopClients(ctx, since, 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if topBlocked == nil {
		topBlocked = []querylog.TopDomain{}
	}
	if topClients == nil {
		topClients = []querylog.ClientStat{}
	}
	writeJSON(w, http.StatusOK, statsResponse{
		WindowHours: hours,
		Timeline:    timeline,
		TopBlocked:  topBlocked,
		TopClients:  topClients,
	})
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
		if err != nil || n < 1 || n > 168 {
			writeError(w, http.StatusBadRequest, "hours must be 1-168")
			return
		}
		hours = n
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	overview, err := s.qlog.ClientOverview(r.Context(), clients, since, 10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		WindowHours int `json:"window_hours"`
		querylog.ClientOverview
	}{hours, overview})
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
