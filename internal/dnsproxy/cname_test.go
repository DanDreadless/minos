package dnsproxy

import (
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/filter"
)

// startCloakingUpstream answers metrics.shop.example with a CNAME to a
// tracker, as a cloaked first-party tracker does; every other name gets a
// plain A record.
func startCloakingUpstream(t testing.TB) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(req)
		name := req.Question[0].Name
		owner := name
		if name == "metrics.shop.example." {
			reply.Answer = append(reply.Answer, &dns.CNAME{
				Hdr:    dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
				Target: "shop.tracker.example.",
			})
			owner = "shop.tracker.example."
		}
		reply.Answer = append(reply.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: owner, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.IPv4(93, 184, 216, 34),
		})
		_ = w.WriteMsg(reply)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func cloakProxy(t testing.TB, inspect bool) (*Server, string) {
	t.Helper()
	up := startCloakingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: up, Protocol: "udp"}}
		c.Blocking.CNAMEInspection = inspect
	}, "tracker.example")
	return srv, srv.UDPAddr().String()
}

func TestCNAMECloakingIsBlocked(t *testing.T) {
	srv, addr := cloakProxy(t, true)
	_ = srv
	for i := 0; i < 2; i++ { // fresh, then from the cache
		resp := query(t, addr, "metrics.shop.example", dns.TypeA)
		if len(resp.Answer) != 1 {
			t.Fatalf("answer %d: %v", i, resp.Answer)
		}
		if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4zero) {
			t.Fatalf("answer %d: the cloaked tracker was not blocked: %v", i, resp.Answer)
		}
	}
	e := srv.qlog.Recent(1)[0]
	if e.List != "testlist" || !strings.Contains(e.Rule, "via CNAME shop.tracker.example") {
		t.Errorf("logged as list %q rule %q", e.List, e.Rule)
	}
	// A name without a blocked CNAME is untouched.
	if a := query(t, addr, "www.shop.example", dns.TypeA).Answer[0].(*dns.A); !a.A.Equal(net.IPv4(93, 184, 216, 34)) {
		t.Errorf("an ordinary answer changed: %v", a.A)
	}
}

func TestCNAMEInspectionCanBeOff(t *testing.T) {
	_, addr := cloakProxy(t, false)
	resp := query(t, addr, "metrics.shop.example", dns.TypeA)
	if len(resp.Answer) != 2 {
		t.Errorf("with inspection off the CNAME chain should pass through: %v", resp.Answer)
	}
}

func TestCNAMEOfAnAllowedNameIsNotInspected(t *testing.T) {
	srv, addr := cloakProxy(t, true)
	fb := filter.NewBuilder()
	fb.AddDeny("testlist", "tracker.example")
	fb.AddAllow("allowlist", "metrics.shop.example")
	srv.engine.Swap(fb.Build())
	if resp := query(t, addr, "metrics.shop.example", dns.TypeA); len(resp.Answer) != 2 {
		t.Errorf("an allowed name's CNAME was blocked: %v", resp.Answer)
	}
}
