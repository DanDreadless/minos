package dnsproxy

import (
	"net"
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/filter"
)

// startLANUpstream answers every A query with a LAN address — what a
// rebinding attacker's authoritative server does.
func startLANUpstream(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(req)
		reply.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.IPv4(192, 168, 1, 1),
		}}
		_ = w.WriteMsg(reply)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestRebindingProtection(t *testing.T) {
	lan := startLANUpstream(t)
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: lan, Protocol: "udp"}}
		c.DNS.RebindProtection = true
	})
	// The allowlist reaches the engine through the lists manager in
	// production; here it is compiled directly.
	fb := filter.NewBuilder()
	fb.AddAllow("allowlist", "plex.direct")
	srv.engine.Swap(fb.Build())
	addr := srv.UDPAddr().String()

	resp := query(t, addr, "evil.example.com", dns.TypeA)
	if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4zero) {
		t.Fatalf("public name answered with a LAN address was not refused: %v", resp.Answer)
	}
	e := waitForEntry(t, qlog)
	if e.List != ListRebinding {
		t.Errorf("logged as %+v, want the rebinding list", e)
	}
	// A second ask comes from the cache — it must be refused there too.
	if a := query(t, addr, "evil.example.com", dns.TypeA).Answer[0].(*dns.A); !a.A.Equal(net.IPv4zero) {
		t.Errorf("the cached answer slipped through: %v", a.A)
	}

	for _, name := range []string{"nas.lan", "printer.home.arpa", "router", "abc.plex.direct"} {
		resp := query(t, addr, name, dns.TypeA)
		if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(192, 168, 1, 1)) {
			t.Errorf("%s should be exempt, got %v", name, resp.Answer)
		}
	}
}

func TestRebindingOffByDefault(t *testing.T) {
	lan := startLANUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: lan, Protocol: "udp"}}
	})
	resp := query(t, srv.UDPAddr().String(), "evil.example.com", dns.TypeA)
	if a := resp.Answer[0].(*dns.A); !a.A.Equal(net.IPv4(192, 168, 1, 1)) {
		t.Errorf("protection is opt-in, but the answer was changed: %v", a.A)
	}
}

func TestRebindIP(t *testing.T) {
	rr := func(s string) dns.RR {
		r, err := dns.NewRR(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, c := range []struct {
		rr      string
		private bool
	}{
		{"x. 60 IN A 10.0.0.1", true}, {"x. 60 IN A 172.16.5.5", true}, {"x. 60 IN A 127.0.0.1", true},
		{"x. 60 IN A 169.254.1.1", true}, {"x. 60 IN AAAA fd00::1", true}, {"x. 60 IN AAAA fe80::1", true},
		{"x. 60 IN A 0.0.0.0", false}, {"x. 60 IN A 93.184.216.34", false}, {"x. 60 IN AAAA 2606:4700::1", false},
	} {
		m := &dns.Msg{Answer: []dns.RR{rr(c.rr)}}
		if got := rebindIP(m) != nil; got != c.private {
			t.Errorf("%s: private = %v, want %v", c.rr, got, c.private)
		}
	}
}
