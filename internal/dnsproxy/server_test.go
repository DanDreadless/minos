package dnsproxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/querylog"
)

// startStubUpstream runs a real DNS server that answers every A query with
// 93.184.216.34 and returns its address.
func startStubUpstream(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{
		PacketConn: pc,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			reply := new(dns.Msg)
			reply.SetReply(req)
			if len(req.Question) == 1 && req.Question[0].Qtype == dns.TypeA {
				reply.Answer = []dns.RR{&dns.A{
					Hdr: dns.RR_Header{
						Name: req.Question[0].Name, Rrtype: dns.TypeA,
						Class: dns.ClassINET, Ttl: 300,
					},
					A: net.IPv4(93, 184, 216, 34),
				}}
			}
			_ = w.WriteMsg(reply)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

// startProxy builds and starts a judged proxy in front of the stub upstream.
func startProxy(t *testing.T, mode string, denied ...string) (*Server, *querylog.Log) {
	t.Helper()
	srv, qlog := startProxyCfg(t, mode, nil, denied...)
	return srv, qlog
}

// startProxyCfg is startProxy with a config mutator, for group/client tests.
func startProxyCfg(t *testing.T, mode string, mutate func(*config.Config), denied ...string) (*Server, *querylog.Log) {
	t.Helper()
	upstream := startStubUpstream(t)

	engine := filter.NewEngine()
	b := filter.NewBuilder()
	for _, d := range denied {
		b.AddDeny("testlist", d)
	}
	engine.Swap(b.Build())

	qlog, err := querylog.Open(querylog.Options{RingSize: 100, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = qlog.Close() })

	cfg := config.Default()
	cfg.DNS.Listen = "127.0.0.1:0"
	cfg.DNS.BlockTTL = 60
	cfg.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
	cfg.Blocking.Mode = mode
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	reg := clients.NewRegistry()
	reg.ApplyConfig(cfg)
	srv, err := New(cfg, engine, qlog, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, qlog
}

func query(t *testing.T, addr, qname string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), qtype)
	c := &dns.Client{Timeout: 3 * time.Second}
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("query %s: %v", qname, err)
	}
	return resp
}

func TestBlockedZeroIP(t *testing.T) {
	srv, qlog := startProxy(t, "zero_ip", "ads.example.com")
	addr := srv.UDPAddr().String()

	resp := query(t, addr, "tracker.ads.example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok || !a.A.Equal(net.IPv4zero) {
		t.Errorf("answer = %v, want 0.0.0.0", resp.Answer[0])
	}
	if a.Hdr.Ttl != 60 {
		t.Errorf("ttl = %d, want 60", a.Hdr.Ttl)
	}

	respAAAA := query(t, addr, "ads.example.com", dns.TypeAAAA)
	if len(respAAAA.Answer) != 1 {
		t.Fatalf("AAAA answers = %d, want 1", len(respAAAA.Answer))
	}
	if aaaa, ok := respAAAA.Answer[0].(*dns.AAAA); !ok || !aaaa.AAAA.Equal(net.IPv6zero) {
		t.Errorf("AAAA answer = %v, want ::", respAAAA.Answer[0])
	}

	// Non-address types get an empty NOERROR, not a bogus record.
	respTXT := query(t, addr, "ads.example.com", dns.TypeTXT)
	if respTXT.Rcode != dns.RcodeSuccess || len(respTXT.Answer) != 0 {
		t.Errorf("TXT block: rcode=%s answers=%d, want NOERROR empty",
			dns.RcodeToString[respTXT.Rcode], len(respTXT.Answer))
	}

	// The verdict must be recorded with list and rule.
	deadline := time.Now().Add(5 * time.Second)
	for {
		recent := qlog.Recent(0)
		if len(recent) >= 3 {
			e := recent[len(recent)-1] // oldest = first query
			if e.Verdict != querylog.VerdictBlocked || e.List != "testlist" || e.Rule != "ads.example.com" {
				t.Errorf("logged entry = %+v, want blocked by testlist/ads.example.com", e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("query log entries never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBlockedNXDomain(t *testing.T) {
	srv, _ := startProxy(t, "nxdomain", "ads.example.com")
	resp := query(t, srv.UDPAddr().String(), "ads.example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("rcode = %s, want NXDOMAIN", dns.RcodeToString[resp.Rcode])
	}
	if len(resp.Answer) != 0 {
		t.Errorf("answers = %d, want 0", len(resp.Answer))
	}
}

func TestAllowedForwards(t *testing.T) {
	srv, qlog := startProxy(t, "zero_ip", "ads.example.com")
	resp := query(t, srv.UDPAddr().String(), "innocent.example.org", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("rcode=%s answers=%d, want NOERROR with 1 answer",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok || !a.A.Equal(net.IPv4(93, 184, 216, 34)) {
		t.Errorf("answer = %v, want stub upstream's 93.184.216.34", resp.Answer[0])
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		recent := qlog.Recent(1)
		if len(recent) == 1 {
			if recent[0].Verdict != querylog.VerdictAllowed || recent[0].Upstream == "" {
				t.Errorf("logged entry = %+v, want allowed with upstream", recent[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("query log entry never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTCPServes(t *testing.T) {
	srv, _ := startProxy(t, "zero_ip", "ads.example.com")
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("ads.example.com"), dns.TypeA)
	c := &dns.Client{Net: "tcp", Timeout: 3 * time.Second}
	resp, _, err := c.Exchange(m, srv.UDPAddr().String())
	if err != nil {
		t.Fatalf("tcp query: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Errorf("tcp answers = %d, want 1", len(resp.Answer))
	}
}

func TestUpstreamFailureIsServfail(t *testing.T) {
	engine := filter.NewEngine()
	qlog, err := querylog.Open(querylog.Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer qlog.Close()

	cfg := config.Default()
	cfg.DNS.Listen = "127.0.0.1:0"
	// A blackhole upstream: reserved port on localhost that nothing serves.
	cfg.DNS.Upstreams = []config.Upstream{{Address: "127.0.0.1:1", Protocol: "tcp"}}

	srv, err := New(cfg, engine, qlog, clients.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	m := new(dns.Msg)
	m.SetQuestion("unreachable.example.", dns.TypeA)
	c := &dns.Client{Timeout: 10 * time.Second}
	resp, _, err := c.Exchange(m, srv.UDPAddr().String())
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if resp.Rcode != dns.RcodeServerFailure {
		t.Errorf("rcode = %s, want SERVFAIL", dns.RcodeToString[resp.Rcode])
	}
}

func TestMalformedQueryRefused(t *testing.T) {
	srv, _ := startProxy(t, "zero_ip")
	m := new(dns.Msg)
	m.Id = dns.Id()
	// Zero questions.
	c := &dns.Client{Timeout: 3 * time.Second}
	resp, _, err := c.Exchange(m, srv.UDPAddr().String())
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if resp.Rcode != dns.RcodeFormatError {
		t.Errorf("rcode = %s, want FORMERR", dns.RcodeToString[resp.Rcode])
	}
}
