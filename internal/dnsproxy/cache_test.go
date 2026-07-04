package dnsproxy

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/querylog"
)

func testCache(cfg config.CacheConfig) (*dnsCache, *time.Time) {
	c := newCache(cfg)
	now := time.Now()
	c.now = func() time.Time { return now }
	return c, &now
}

func aResponse(qname string, ttl uint32) (*dns.Msg, *dns.Msg) {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(qname), dns.TypeA)
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{
			Name: dns.Fqdn(qname), Rrtype: dns.TypeA,
			Class: dns.ClassINET, Ttl: ttl,
		},
		A: net.IPv4(192, 0, 2, 1),
	}}
	return req, resp
}

func nxResponse(qname string, soaMinTTL uint32) (*dns.Msg, *dns.Msg) {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(qname), dns.TypeA)
	resp := new(dns.Msg)
	resp.SetRcode(req, dns.RcodeNameError)
	resp.Ns = []dns.RR{&dns.SOA{
		Hdr: dns.RR_Header{
			Name: "example.com.", Rrtype: dns.TypeSOA,
			Class: dns.ClassINET, Ttl: 3600,
		},
		Ns: "ns.example.com.", Mbox: "hostmaster.example.com.",
		Minttl: soaMinTTL,
	}}
	return req, resp
}

func TestCacheRoundTripDecrementsTTL(t *testing.T) {
	c, now := testCache(config.CacheConfig{MaxEntries: 100, MinTTL: 0, MaxTTL: 3600})
	req, resp := aResponse("example.com", 300)
	key := cacheKey("example.com", dns.TypeA, req)

	c.put(key, resp)
	*now = now.Add(5 * time.Second)

	req2, _ := aResponse("example.com", 0)
	req2.Id = 4242
	got := c.get(key, req2)
	if got == nil {
		t.Fatal("expected cache hit")
	}
	if got.Id != 4242 {
		t.Errorf("Id = %d, want request's 4242", got.Id)
	}
	if !got.Response || !got.RecursionAvailable {
		t.Error("served message must have QR and RA set")
	}
	if len(got.Answer) != 1 || got.Answer[0].Header().Ttl != 295 {
		t.Errorf("answer TTL = %d, want 295", got.Answer[0].Header().Ttl)
	}
	// The stored entry must be untouched by TTL adjustment on serve.
	*now = now.Add(5 * time.Second)
	if again := c.get(key, req2); again.Answer[0].Header().Ttl != 290 {
		t.Errorf("second hit TTL = %d, want 290", again.Answer[0].Header().Ttl)
	}
}

func TestCacheExpiry(t *testing.T) {
	c, now := testCache(config.CacheConfig{MaxEntries: 100, MaxTTL: 3600})
	req, resp := aResponse("example.com", 300)
	key := cacheKey("example.com", dns.TypeA, req)
	c.put(key, resp)

	*now = now.Add(301 * time.Second)
	if got := c.get(key, req); got != nil {
		t.Fatal("expected miss after TTL expiry")
	}
	if c.size.Load() != 0 {
		t.Errorf("size = %d after lazy expiry, want 0", c.size.Load())
	}
}

func TestCacheTTLClamps(t *testing.T) {
	c, now := testCache(config.CacheConfig{MaxEntries: 100, MinTTL: 10, MaxTTL: 3600})

	// A 2s record is kept for min_ttl (10s); the served TTL floors at 0.
	req, short := aResponse("short.example.com", 2)
	key := cacheKey("short.example.com", dns.TypeA, req)
	c.put(key, short)
	*now = now.Add(5 * time.Second)
	got := c.get(key, req)
	if got == nil {
		t.Fatal("expected hit inside min_ttl window")
	}
	if got.Answer[0].Header().Ttl != 0 {
		t.Errorf("served TTL = %d, want floor 0", got.Answer[0].Header().Ttl)
	}

	// A day-long record is dropped after max_ttl.
	req2, long := aResponse("long.example.com", 86400)
	key2 := cacheKey("long.example.com", dns.TypeA, req2)
	c.put(key2, long)
	*now = now.Add(3601 * time.Second)
	if c.get(key2, req2) != nil {
		t.Error("expected miss after max_ttl")
	}
}

func TestCacheNegativeUsesSOA(t *testing.T) {
	c, now := testCache(config.CacheConfig{MaxEntries: 100, MaxTTL: 3600})
	req, resp := nxResponse("nope.example.com", 60)
	key := cacheKey("nope.example.com", dns.TypeA, req)
	c.put(key, resp)

	*now = now.Add(59 * time.Second)
	got := c.get(key, req)
	if got == nil {
		t.Fatal("expected negative-cache hit inside SOA minimum")
	}
	if got.Rcode != dns.RcodeNameError {
		t.Errorf("rcode = %s, want NXDOMAIN", dns.RcodeToString[got.Rcode])
	}
	*now = now.Add(2 * time.Second)
	if c.get(key, req) != nil {
		t.Error("expected miss after SOA minimum elapsed")
	}
}

func TestCacheRejectsUncacheable(t *testing.T) {
	c, _ := testCache(config.CacheConfig{MaxEntries: 100, MaxTTL: 3600})

	req, servfail := aResponse("fail.example.com", 300)
	servfail.Rcode = dns.RcodeServerFailure
	c.put(cacheKey("fail.example.com", dns.TypeA, req), servfail)

	req2, trunc := aResponse("trunc.example.com", 300)
	trunc.Truncated = true
	c.put(cacheKey("trunc.example.com", dns.TypeA, req2), trunc)

	if n := c.size.Load(); n != 0 {
		t.Errorf("size = %d, want 0 (SERVFAIL and truncated must not cache)", n)
	}
}

func TestCacheEvictionBoundsSize(t *testing.T) {
	c, _ := testCache(config.CacheConfig{MaxEntries: 8, MaxTTL: 3600})
	for i := 0; i < 40; i++ {
		name := "host" + strconv.Itoa(i) + ".example.com"
		req, resp := aResponse(name, 300)
		c.put(cacheKey(name, dns.TypeA, req), resp)
	}
	if n := c.size.Load(); n > 8 {
		t.Errorf("size = %d, want <= max 8", n)
	}
}

func TestCacheKeySeparatesDOBit(t *testing.T) {
	plain := new(dns.Msg)
	plain.SetQuestion("example.com.", dns.TypeA)
	dnssec := new(dns.Msg)
	dnssec.SetQuestion("example.com.", dns.TypeA)
	dnssec.SetEdns0(1232, true)
	if cacheKey("example.com", dns.TypeA, plain) == cacheKey("example.com", dns.TypeA, dnssec) {
		t.Error("DO-bit queries must not share a cache key with plain ones")
	}
}

// startCountingUpstream is startStubUpstream plus a served-query counter.
func startCountingUpstream(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{
		PacketConn: pc,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			count.Add(1)
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
	return pc.LocalAddr().String(), &count
}

func TestCacheServesSecondQuery(t *testing.T) {
	upstream, count := startCountingUpstream(t)

	engine := filter.NewEngine()
	qlog, err := querylog.Open(querylog.Options{RingSize: 100, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = qlog.Close() })

	cfg := config.Default()
	cfg.DNS.Listen = "127.0.0.1:0"
	cfg.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
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
	addr := srv.UDPAddr().String()

	first := query(t, addr, "cached.example.org", dns.TypeA)
	second := query(t, addr, "cached.example.org", dns.TypeA)
	if first.Rcode != dns.RcodeSuccess || second.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcodes = %s/%s, want NOERROR",
			dns.RcodeToString[first.Rcode], dns.RcodeToString[second.Rcode])
	}
	if got := count.Load(); got != 1 {
		t.Errorf("upstream served %d queries, want 1 (second must come from cache)", got)
	}
	if len(second.Answer) != 1 {
		t.Fatalf("cached answers = %d, want 1", len(second.Answer))
	}

	hits, misses, entries, enabled := srv.CacheStats()
	if !enabled || hits != 1 || misses != 1 || entries != 1 {
		t.Errorf("CacheStats = hits %d misses %d entries %d enabled %v, want 1/1/1/true",
			hits, misses, entries, enabled)
	}

	// The docket must attribute the hit to the cache.
	deadline := time.Now().Add(5 * time.Second)
	for {
		recent := qlog.Recent(1)
		if len(recent) == 1 && recent[0].Upstream == "cache" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("newest entry = %+v, want upstream \"cache\"", recent)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCacheDisabled(t *testing.T) {
	upstream, count := startCountingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
		c.DNS.Cache.Enabled = false
	})
	addr := srv.UDPAddr().String()
	query(t, addr, "twice.example.org", dns.TypeA)
	query(t, addr, "twice.example.org", dns.TypeA)
	if got := count.Load(); got != 2 {
		t.Errorf("upstream served %d queries, want 2 with cache disabled", got)
	}
	if _, _, _, enabled := srv.CacheStats(); enabled {
		t.Error("CacheStats reports enabled, want disabled")
	}
}

func BenchmarkCacheHit(b *testing.B) {
	c := newCache(config.CacheConfig{MaxEntries: 10000, MinTTL: 10, MaxTTL: 3600})
	req, resp := aResponse("bench.example.com", 300)
	key := cacheKey("bench.example.com", dns.TypeA, req)
	c.put(key, resp)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if c.get(key, req) == nil {
			b.Fatal("miss")
		}
	}
}
