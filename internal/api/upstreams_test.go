package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/dnsproxy"
)

// testingProxy is fakeProxyStats that can also test an upstream.
type testingProxy struct {
	fakeProxyStats
	got config.Upstream
}

func (p *testingProxy) TestUpstream(_ context.Context, u config.Upstream) dnsproxy.UpstreamTest {
	p.got = u
	if u.Address == "9.9.9.9:853" {
		return dnsproxy.UpstreamTest{OK: true, RTT: 12500 * time.Microsecond, Rcode: "NOERROR", Configured: true}
	}
	return dnsproxy.UpstreamTest{Err: "dial tcp 192.0.2.1:853: i/o timeout"}
}

func TestUpstreamTestEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()
	if rec := do(t, r, "POST", "/api/upstreams/test", `{"address":"9.9.9.9:853","protocol":"dot"}`, nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("without a proxy: %d", rec.Code)
	}
	p := &testingProxy{}
	s.cache = p
	var res upstreamTestResult
	rec := do(t, r, "POST", "/api/upstreams/test", `{"address":"9.9.9.9:853","protocol":"dot"}`, nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		t.Fatalf("test: %d %s", rec.Code, rec.Body)
	}
	if !res.OK || res.Ms != 12.5 || res.Rcode != "NOERROR" || !res.Configured || p.got.Protocol != "dot" {
		t.Errorf("result %+v (asked %+v)", res, p.got)
	}
	rec = do(t, r, "POST", "/api/upstreams/test", `{"address":"192.0.2.1:853","protocol":"dot"}`, nil, nil)
	res = upstreamTestResult{}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.OK || res.Error == "" {
		t.Errorf("failing test: %d %+v", rec.Code, res)
	}
	// Validated like a saved upstream: a DoH URL under dot is refused.
	if rec := do(t, r, "POST", "/api/upstreams/test", `{"address":"https://9.9.9.9/dns-query","protocol":"dot"}`, nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid upstream: %d", rec.Code)
	}
}
