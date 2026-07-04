package dnsproxy

import (
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
)

func TestMatchPrivateArpa(t *testing.T) {
	cases := []struct {
		qname string
		want  string
	}{
		{"42.1.168.192.in-addr.arpa", "168.192.in-addr.arpa"},
		{"9.0.0.10.in-addr.arpa", "10.in-addr.arpa"},
		{"1.20.172.in-addr.arpa", "20.172.in-addr.arpa"},
		{"7.3.99.100.in-addr.arpa", "99.100.in-addr.arpa"}, // CGNAT
		{"1.0.0.127.in-addr.arpa", "127.in-addr.arpa"},
		{"168.192.in-addr.arpa", "168.192.in-addr.arpa"}, // apex
		// ULA fd00::/8 lives under d.f.ip6.arpa
		{"0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.d.f.ip6.arpa", "d.f.ip6.arpa"},
		// Public space is untouched
		{"8.8.8.8.in-addr.arpa", ""},
		{"1.15.172.in-addr.arpa", ""},  // 172.15 is outside 172.16/12
		{"1.128.100.in-addr.arpa", ""}, // 100.128 is outside 100.64/10
		{"example.com", ""},
		{"totally.arpa", ""},
	}
	for _, tc := range cases {
		if got := matchPrivateArpa(tc.qname); got != tc.want {
			t.Errorf("matchPrivateArpa(%q) = %q, want %q", tc.qname, got, tc.want)
		}
	}
}

func TestPrivateArpaAnsweredLocally(t *testing.T) {
	upstream, count := startCountingUpstream(t)
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
	})
	addr := srv.UDPAddr().String()

	resp := query(t, addr, "42.1.168.192.in-addr.arpa", dns.TypePTR)
	if resp.Rcode != dns.RcodeNameError || !resp.Authoritative {
		t.Fatalf("rcode=%s aa=%v, want authoritative NXDOMAIN",
			dns.RcodeToString[resp.Rcode], resp.Authoritative)
	}
	if len(resp.Ns) != 1 {
		t.Fatalf("authority records = %d, want SOA for negative caching", len(resp.Ns))
	}
	if soa, ok := resp.Ns[0].(*dns.SOA); !ok || soa.Hdr.Name != "168.192.in-addr.arpa." {
		t.Errorf("authority = %v, want the zone's SOA", resp.Ns[0])
	}
	if got := count.Load(); got != 0 {
		t.Errorf("upstream saw %d queries, want 0 (private PTR must not leak)", got)
	}

	// Zone apex SOA gets a positive answer.
	apex := query(t, addr, "10.in-addr.arpa", dns.TypeSOA)
	if apex.Rcode != dns.RcodeSuccess || len(apex.Answer) != 1 {
		t.Errorf("apex SOA: rcode=%s answers=%d, want NOERROR with SOA",
			dns.RcodeToString[apex.Rcode], len(apex.Answer))
	}

	// The docket names the backstop.
	waitFor(t, func() bool {
		for _, e := range qlog.Recent(0) {
			if e.Rule == "private reverse zone" && e.Upstream == "local" {
				return true
			}
		}
		return false
	}, "private-reverse docket entry")
}

func TestPrivateArpaPrecedence(t *testing.T) {
	routerAddr, routerH := startAnsweringUpstream(t, nil, "router-answer.lan.")
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.LocalTTL = 120
		c.DNS.LocalRecords = []config.LocalRecord{
			{Name: "nas.home.lab", A: []string{"192.168.1.10"}},
		}
		c.DNS.Routes = []config.Route{{
			Domains:  []string{"2.168.192.in-addr.arpa"},
			Upstream: config.Upstream{Address: routerAddr, Protocol: "udp"},
		}}
	})
	addr := srv.UDPAddr().String()

	// A local record's auto-PTR beats the empty-zone backstop.
	local := query(t, addr, "10.1.168.192.in-addr.arpa", dns.TypePTR)
	if len(local.Answer) != 1 {
		t.Fatalf("local PTR answers = %d, want 1", len(local.Answer))
	}
	if ptr, ok := local.Answer[0].(*dns.PTR); !ok || ptr.Ptr != "nas.home.lab." {
		t.Errorf("local PTR = %v, want nas.home.lab.", local.Answer[0])
	}

	// A conditional route beats the backstop: the router answers.
	routed := query(t, addr, "9.2.168.192.in-addr.arpa", dns.TypePTR)
	if len(routed.Answer) != 1 {
		t.Fatalf("routed PTR answers = %d, want 1 from the router", len(routed.Answer))
	}
	if ptr, ok := routed.Answer[0].(*dns.PTR); !ok || ptr.Ptr != "router-answer.lan." {
		t.Errorf("routed PTR = %v, want router-answer.lan.", routed.Answer[0])
	}
	if routerH.served.Load() != 1 {
		t.Errorf("router served %d, want 1", routerH.served.Load())
	}

	// An uncovered private zone still gets the backstop.
	other := query(t, addr, "9.3.168.192.in-addr.arpa", dns.TypePTR)
	if other.Rcode != dns.RcodeNameError {
		t.Errorf("uncovered zone rcode = %s, want NXDOMAIN", dns.RcodeToString[other.Rcode])
	}
}

func TestPrivateArpaOptOut(t *testing.T) {
	upstream, count := startCountingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
		c.DNS.ForwardPrivateReverse = true
	})
	resp := query(t, srv.UDPAddr().String(), "42.1.168.192.in-addr.arpa", dns.TypePTR)
	if resp.Rcode == dns.RcodeNameError && resp.Authoritative {
		t.Error("opt-out set, but the private zone was still answered locally")
	}
	if got := count.Load(); got != 1 {
		t.Errorf("upstream saw %d queries, want 1 with forward_private_reverse", got)
	}
}
