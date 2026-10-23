package dnsproxy

import (
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

func TestDefaultAccessAnswersPrivateAndLocalOnly(t *testing.T) {
	a := buildAccessList(nil, time.Now())
	for _, ip := range []string{"192.168.1.5", "10.1.2.3", "172.20.0.1", "100.100.1.1", "127.0.0.1", "fd12::1", "fe80::1", "::1"} {
		if !a.allows(netip.MustParseAddr(ip)) {
			t.Errorf("%s refused by default", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2001:db8::1"} {
		if a.allows(netip.MustParseAddr(ip)) {
			t.Errorf("%s answered by default — an open resolver", ip)
		}
	}
	custom := buildAccessList([]string{"203.0.113.0/24", "198.51.100.7"}, time.Now())
	if !custom.allows(netip.MustParseAddr("203.0.113.9")) || !custom.allows(netip.MustParseAddr("198.51.100.7")) ||
		custom.allows(netip.MustParseAddr("192.168.1.5")) {
		t.Error("a configured list must replace the default")
	}
}

func TestQueriesFromOutsideAreRefused(t *testing.T) {
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.AllowedNetworks = []string{"10.0.0.0/8"} // the test client is 127.0.0.1
	})
	resp := exchangeEDNS(t, srv.UDPAddr().String(), "example.org", dns.TypeA, true)
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("rcode = %s, want REFUSED", dns.RcodeToString[resp.Rcode])
	}
	if e := ede(resp); e == nil || e.InfoCode != dns.ExtendedErrorCodeProhibited {
		t.Errorf("EDE = %+v", e)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(qlog.Recent(10)); n != 0 {
		t.Errorf("an outside query reached the query log (%d entries)", n)
	}
}

func TestRateLimit(t *testing.T) {
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) { c.DNS.RateLimit = 5 })
	addr := srv.UDPAddr().String()
	for i := 0; i < 5; i++ {
		if rc := query(t, addr, "example.org", dns.TypeA).Rcode; rc != dns.RcodeSuccess {
			t.Fatalf("query %d within the limit: %s", i+1, dns.RcodeToString[rc])
		}
	}
	for i := 0; i < 3; i++ {
		if rc := query(t, addr, "example.org", dns.TypeA).Rcode; rc != dns.RcodeRefused {
			t.Fatalf("query over the limit: %s, want REFUSED", dns.RcodeToString[rc])
		}
	}
	time.Sleep(50 * time.Millisecond)
	limited := 0
	for _, e := range qlog.Recent(20) {
		if e.List == ListRateLimit {
			limited++
		}
	}
	if limited != 1 {
		t.Errorf("rate-limit refusals logged %d times, want once a minute", limited)
	}
}

func TestRateLimiterWindows(t *testing.T) {
	l := newRateLimiter(2)
	now := time.Unix(1_800_000_000, 0)
	l.now = func() time.Time { return now }
	ip := netip.MustParseAddr("192.168.1.9")
	ok1, _ := l.allow(ip)
	ok2, _ := l.allow(ip)
	ok3, first := l.allow(ip)
	_, again := l.allow(ip)
	if !ok1 || !ok2 || ok3 || !first || again {
		t.Fatalf("window: %v %v %v first=%v again=%v", ok1, ok2, ok3, first, again)
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow(ip); !ok {
		t.Error("a new minute should reset the count")
	}
	if newRateLimiter(0) != nil {
		t.Error("0 must turn the limiter off")
	}
}
