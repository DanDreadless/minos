package api

import (
	"net/http"
	"strings"
	"testing"

	"minos/internal/dnsproxy"
	"minos/internal/querylog"
)

// fakeProxyStats implements ProxyStatsSource for metrics tests.
type fakeProxyStats struct{}

func (fakeProxyStats) CacheStats() (uint64, uint64, int64, bool) {
	return 7, 3, 5, true
}

func (fakeProxyStats) UpstreamStats() []dnsproxy.UpstreamStat {
	return []dnsproxy.UpstreamStat{
		{Name: `dns.example"quote`, Requests: 10, Failures: 2, DurationSeconds: 1.5},
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.cache = fakeProxyStats{}
	s.qlog.Record(querylog.Entry{Client: "10.0.0.9", QName: "example.com", Verdict: querylog.VerdictAllowed})

	rec := doJSON(t, s.Router(), http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q, want exposition format 0.0.4", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE minos_queries_total counter",
		"minos_queries_total 1",
		"minos_blocking_paused 0",
		`minos_rules{type="deny"} 0`,
		"minos_cache_hits_total 7",
		"minos_cache_entries 5",
		`minos_upstream_requests_total{upstream="dns.example\"quote"} 10`,
		`minos_upstream_failures_total{upstream="dns.example\"quote"} 2`,
		`minos_upstream_duration_seconds_total{upstream="dns.example\"quote"} 1.500000`,
		`minos_build_info{version="test"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

func TestMetricsRequiresToken(t *testing.T) {
	s, _ := newTestServer(t, "sekrit")
	rec := doJSON(t, s.Router(), http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /metrics = %d, want 401", rec.Code)
	}
	rec = doJSON(t, s.Router(), http.MethodGet, "/metrics", "", map[string]string{
		"Authorization": "Bearer sekrit",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer-authed /metrics = %d, want 200", rec.Code)
	}
}

func TestMetricsWithoutProxySource(t *testing.T) {
	s, _ := newTestServer(t, "") // cache source stays nil
	rec := doJSON(t, s.Router(), http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with nil proxy source", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "minos_cache_enabled") {
		t.Error("cache series present despite nil proxy source")
	}
}
