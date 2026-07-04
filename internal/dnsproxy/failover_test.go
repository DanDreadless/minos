package dnsproxy

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// deadUpstreamAddr reserves a loopback TCP port with nothing listening, so
// exchanges fail fast with connection refused.
func deadUpstreamAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func upstreamRequests(srv *Server, name string) uint64 {
	for _, u := range srv.UpstreamStats() {
		if u.Name == name {
			return u.Requests
		}
	}
	return 0
}

// TestFailoverSidestepsSickUpstream: after failThreshold consecutive
// failures the dead primary stops being tried; the healthy secondary
// answers everything.
func TestFailoverSidestepsSickUpstream(t *testing.T) {
	oldCooldown := failoverCooldown
	failoverCooldown = time.Hour // no half-open probes during this test
	defer func() { failoverCooldown = oldCooldown }()

	dead := deadUpstreamAddr(t)
	good, goodCount := startCountingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{
			{Address: dead, Protocol: "tcp"},
			{Address: good, Protocol: "udp"},
		}
		c.DNS.Cache.Enabled = false // every query must hit the forward path
	})
	addr := srv.UDPAddr().String()

	const queries = 6
	for i := 0; i < queries; i++ {
		resp := query(t, addr, "healthy.example.org", dns.TypeA)
		if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
			t.Fatalf("query %d: rcode=%s answers=%d, want success via secondary",
				i, dns.RcodeToString[resp.Rcode], len(resp.Answer))
		}
	}
	if got := goodCount.Load(); got != queries {
		t.Errorf("secondary served %d, want %d", got, queries)
	}
	// The primary is tried until the breaker trips, then sidestepped.
	if got := upstreamRequests(srv, dead); got != failThreshold {
		t.Errorf("dead primary tried %d times, want exactly %d (breaker)", got, failThreshold)
	}
	for _, u := range srv.UpstreamStats() {
		if u.Name == dead && !u.Sick {
			t.Error("dead primary not reported sick in UpstreamStats")
		}
	}
}

// TestFailoverRecovers: once the cooldown lapses, a probe query reaches the
// recovered primary and the breaker resets.
func TestFailoverRecovers(t *testing.T) {
	oldCooldown := failoverCooldown
	failoverCooldown = 50 * time.Millisecond
	defer func() { failoverCooldown = oldCooldown }()

	dead := deadUpstreamAddr(t)
	good, _ := startCountingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{
			{Address: dead, Protocol: "tcp"}, // refused now; comes alive later
			{Address: good, Protocol: "udp"},
		}
		c.DNS.Cache.Enabled = false
	})
	addr := srv.UDPAddr().String()

	// Trip the breaker: TCP to a closed loopback port is refused instantly,
	// so each of these is answered fast by the healthy secondary.
	for i := 0; i < failThreshold; i++ {
		query(t, addr, "trip.example.org", dns.TypeA)
	}

	// Bring the primary to life on the very port that was dead.
	ln, err := net.Listen("tcp", dead)
	if err != nil {
		t.Skipf("could not rebind reserved port: %v", err)
	}
	revived := &dns.Server{
		Listener: ln,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			reply := new(dns.Msg)
			reply.SetReply(req)
			reply.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{
					Name: req.Question[0].Name, Rrtype: dns.TypeA,
					Class: dns.ClassINET, Ttl: 300,
				},
				A: net.IPv4(198, 51, 100, 7),
			}}
			_ = w.WriteMsg(reply)
		}),
	}
	go func() { _ = revived.ActivateAndServe() }()
	t.Cleanup(func() { _ = revived.Shutdown() })

	// After the cooldown, queries flow to the recovered primary again.
	deadline := time.Now().Add(5 * time.Second)
	for {
		time.Sleep(60 * time.Millisecond)
		resp := query(t, addr, "recovered.example.org", dns.TypeA)
		if len(resp.Answer) == 1 {
			if a, ok := resp.Answer[0].(*dns.A); ok && a.A.Equal(net.IPv4(198, 51, 100, 7)) {
				return // primary answered: breaker reset
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("primary never resumed answering after recovery")
		}
	}
}

// TestAllSickStillTried: with every upstream sick, queries still attempt
// them (last resort) rather than failing without trying.
func TestAllSickStillTried(t *testing.T) {
	oldCooldown := failoverCooldown
	failoverCooldown = time.Hour
	defer func() { failoverCooldown = oldCooldown }()

	dead := deadUpstreamAddr(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: dead, Protocol: "tcp"}}
		c.DNS.Cache.Enabled = false
	})
	addr := srv.UDPAddr().String()

	const queries = 5
	for i := 0; i < queries; i++ {
		resp := query(t, addr, "doomed.example.org", dns.TypeA)
		if resp.Rcode != dns.RcodeServerFailure {
			t.Fatalf("query %d: rcode = %s, want SERVFAIL", i, dns.RcodeToString[resp.Rcode])
		}
	}
	if got := upstreamRequests(srv, dead); got != queries {
		t.Errorf("sole dead upstream tried %d times, want %d (last resort must always try)", got, queries)
	}
}
