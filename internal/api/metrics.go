package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleMetrics serves Prometheus text exposition format (0.0.4),
// hand-rolled so the metrics endpoint costs zero dependencies. It is
// scrape-only and served by the same authenticated LAN listener as the
// rest of the API — nothing here phones home, keeping the no-telemetry
// promise intact.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder

	gauge := func(name, help string, v any, labels ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		fmt.Fprintf(&b, "%s%s %v\n", name, labelSet(labels), v)
	}
	counterHead := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	}
	sample := func(name string, v any, labels ...string) {
		fmt.Fprintf(&b, "%s%s %v\n", name, labelSet(labels), v)
	}

	gauge("minos_build_info", "Build information; the value is always 1.",
		1, "version", s.version)
	gauge("minos_uptime_seconds", "Seconds since this instance started.",
		int64(time.Since(s.started).Seconds()))

	total, blocked, dropped := s.qlog.Stats()
	counterHead("minos_queries_total", "DNS queries handled since start.")
	sample("minos_queries_total", total)
	counterHead("minos_queries_blocked_total", "DNS queries blocked since start.")
	sample("minos_queries_blocked_total", blocked)
	counterHead("minos_querylog_dropped_total", "Query-log entries dropped because the writer fell behind.")
	sample("minos_querylog_dropped_total", dropped)

	paused, _ := s.engine.Paused()
	gauge("minos_blocking_paused", "1 while blocking is paused (recess), else 0.", boolVal(paused))

	m := s.engine.Current()
	fmt.Fprintf(&b, "# HELP minos_rules Compiled rules in the active matcher.\n# TYPE minos_rules gauge\n")
	sample("minos_rules", m.Rules(), "type", "deny")
	sample("minos_rules", m.AllowRules(), "type", "allow")
	gauge("minos_rules_skipped", "List entries skipped as invalid or unsupported.", m.Skipped())

	if s.cache != nil {
		hits, misses, entries, enabled := s.cache.CacheStats()
		gauge("minos_cache_enabled", "1 when the response cache is enabled.", boolVal(enabled))
		gauge("minos_cache_entries", "Entries currently in the response cache.", entries)
		counterHead("minos_cache_hits_total", "Queries answered from the response cache.")
		sample("minos_cache_hits_total", hits)
		counterHead("minos_cache_misses_total", "Cache lookups that had to go upstream.")
		sample("minos_cache_misses_total", misses)

		ups := s.cache.UpstreamStats()
		if len(ups) > 0 {
			counterHead("minos_upstream_requests_total", "Exchange attempts per upstream resolver.")
			for _, u := range ups {
				sample("minos_upstream_requests_total", u.Requests, "upstream", u.Name)
			}
			counterHead("minos_upstream_failures_total", "Failed exchange attempts per upstream resolver.")
			for _, u := range ups {
				sample("minos_upstream_failures_total", u.Failures, "upstream", u.Name)
			}
			counterHead("minos_upstream_duration_seconds_total", "Cumulative exchange time per upstream; divide by requests for mean latency.")
			for _, u := range ups {
				sample("minos_upstream_duration_seconds_total",
					fmt.Sprintf("%.6f", u.DurationSeconds), "upstream", u.Name)
			}
		}
	}

	lists := s.lists.Status()
	if len(lists) > 0 {
		fmt.Fprintf(&b, "# HELP minos_list_rules Compiled rules contributed per blocklist.\n# TYPE minos_list_rules gauge\n")
		for _, l := range lists {
			sample("minos_list_rules", l.Rules, "list", l.Name)
		}
		fmt.Fprintf(&b, "# HELP minos_list_enabled 1 when the blocklist subscription is enabled.\n# TYPE minos_list_enabled gauge\n")
		for _, l := range lists {
			sample("minos_list_enabled", boolVal(l.Enabled), "list", l.Name)
		}
	}

	gauge("minos_devices_seen", "Distinct client IPs that have queried since start.", s.clients.SeenCount())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// labelSet renders {k="v",...} from alternating key/value pairs.
func labelSet(kv []string) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(kv[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// escapeLabel escapes a label value per the exposition format.
func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

func boolVal(b bool) int {
	if b {
		return 1
	}
	return 0
}
