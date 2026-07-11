package dnsproxy

import (
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/querylog"
)

// TestFirefoxCanary: the DoH canary is answered authoritative-NXDOMAIN by
// default (any qtype), logged as a block naming the canary pseudo-list.
func TestFirefoxCanary(t *testing.T) {
	srv, qlog := startProxy(t, "zero_ip")
	addr := srv.UDPAddr().String()

	for _, qtype := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeTXT} {
		resp := query(t, addr, "use-application-dns.net", qtype)
		if resp.Rcode != dns.RcodeNameError || !resp.Authoritative {
			t.Errorf("qtype %s: rcode=%s aa=%v, want authoritative NXDOMAIN",
				dns.TypeToString[qtype], dns.RcodeToString[resp.Rcode], resp.Authoritative)
		}
	}
	// Case-insensitive: normalization happens before the compare.
	if resp := query(t, addr, "Use-Application-DNS.Net", dns.TypeA); resp.Rcode != dns.RcodeNameError {
		t.Errorf("mixed case: rcode = %s, want NXDOMAIN", dns.RcodeToString[resp.Rcode])
	}

	entries := qlog.Recent(1)
	if len(entries) != 1 || entries[0].Verdict != querylog.VerdictBlocked ||
		entries[0].List != "firefox-doh-canary" {
		t.Errorf("docket entry = %+v, want blocked by firefox-doh-canary", entries)
	}

	// A sibling name must not be swallowed by the canary check.
	if resp := query(t, addr, "example.com", dns.TypeA); resp.Rcode != dns.RcodeSuccess {
		t.Errorf("example.com rcode = %s, want NOERROR (forwarded)", dns.RcodeToString[resp.Rcode])
	}
}

// TestFirefoxCanaryOptOut: dns.allow_firefox_doh forwards the probe upstream
// like any other name.
func TestFirefoxCanaryOptOut(t *testing.T) {
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.AllowFirefoxDoH = true
	})
	resp := query(t, srv.UDPAddr().String(), "use-application-dns.net", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		t.Errorf("opt-out: rcode=%s answers=%d, want the stub upstream's answer",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
}
