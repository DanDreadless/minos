package dnsproxy

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// startAnsweringUpstream runs a stub that answers every A query with ip and
// every PTR query with ptrTarget, counting what it serves.
func startAnsweringUpstream(t *testing.T, ip net.IP, ptrTarget string) (string, *countingHandler) {
	t.Helper()
	h := &countingHandler{ip: ip, ptrTarget: ptrTarget}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: h}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String(), h
}

type countingHandler struct {
	ip        net.IP
	ptrTarget string
	served    atomic.Int32
}

func (h *countingHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	h.served.Add(1)
	reply := new(dns.Msg)
	reply.SetReply(req)
	if len(req.Question) == 1 {
		q := req.Question[0]
		switch q.Qtype {
		case dns.TypeA:
			reply.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
				A:   h.ip,
			}}
		case dns.TypePTR:
			reply.Answer = []dns.RR{&dns.PTR{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 300},
				Ptr: h.ptrTarget,
			}}
		}
	}
	_ = w.WriteMsg(reply)
}

func TestConditionalForwarding(t *testing.T) {
	defaultAddr, defaultH := startAnsweringUpstream(t, net.IPv4(203, 0, 113, 1), "public.example.")
	routerAddr, routerH := startAnsweringUpstream(t, net.IPv4(192, 168, 1, 42), "printer.lan.")

	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: defaultAddr, Protocol: "udp"}}
		c.DNS.Routes = []config.Route{{
			Domains:  []string{"lan", "1.168.192.in-addr.arpa"},
			Upstream: config.Upstream{Address: routerAddr, Protocol: "udp"},
		}}
	})
	addr := srv.UDPAddr().String()

	// A routed name (subdomain of "lan") goes to the router upstream.
	resp := query(t, addr, "printer.lan", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("routed answers = %d, want 1", len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(192, 168, 1, 42)) {
		t.Errorf("routed answer = %v, want router's 192.168.1.42", resp.Answer[0])
	}

	// A reverse lookup in the routed zone goes to the router too.
	ptr := query(t, addr, "42.1.168.192.in-addr.arpa", dns.TypePTR)
	if len(ptr.Answer) != 1 {
		t.Fatalf("routed PTR answers = %d, want 1", len(ptr.Answer))
	}
	if p, ok := ptr.Answer[0].(*dns.PTR); !ok || p.Ptr != "printer.lan." {
		t.Errorf("routed PTR = %v, want printer.lan.", ptr.Answer[0])
	}

	// Everything else uses the default upstream.
	pub := query(t, addr, "example.org", dns.TypeA)
	if a, ok := pub.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(203, 0, 113, 1)) {
		t.Errorf("default answer = %v, want default upstream's 203.0.113.1", pub.Answer[0])
	}

	if routerH.served.Load() != 2 {
		t.Errorf("router upstream served %d, want 2", routerH.served.Load())
	}
	if defaultH.served.Load() != 1 {
		t.Errorf("default upstream served %d, want 1", defaultH.served.Load())
	}
}

func TestRoutedAnswersAreNotCached(t *testing.T) {
	_, _ = startAnsweringUpstream(t, net.IPv4(203, 0, 113, 1), "")
	routerAddr, routerH := startAnsweringUpstream(t, net.IPv4(192, 168, 1, 42), "")

	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Routes = []config.Route{{
			Domains:  []string{"lan"},
			Upstream: config.Upstream{Address: routerAddr, Protocol: "udp"},
		}}
	})
	addr := srv.UDPAddr().String()

	query(t, addr, "printer.lan", dns.TypeA)
	query(t, addr, "printer.lan", dns.TypeA)
	if routerH.served.Load() != 2 {
		t.Errorf("router served %d, want 2 (routed answers must not be cached)", routerH.served.Load())
	}
}

func TestRoutedUpstreamFailureIsServfail(t *testing.T) {
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		// Blackhole route: nothing listens there. No fallback to defaults.
		c.DNS.Routes = []config.Route{{
			Domains:  []string{"lan"},
			Upstream: config.Upstream{Address: "127.0.0.1:1", Protocol: "tcp"},
		}}
	})
	resp := query(t, srv.UDPAddr().String(), "printer.lan", dns.TypeA)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Errorf("rcode = %s, want SERVFAIL (routes are authoritative)",
			dns.RcodeToString[resp.Rcode])
	}
}
