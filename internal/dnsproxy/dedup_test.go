package dnsproxy

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// startSlowUpstream answers every A query after delay, counting requests —
// slow enough that concurrent queries overlap and dedup can collapse them.
func startSlowUpstream(t *testing.T, delay time.Duration) (string, *atomic.Int64) {
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
			time.Sleep(delay)
			reply := new(dns.Msg)
			reply.SetReply(req)
			reply.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{
					Name: req.Question[0].Name, Rrtype: dns.TypeA,
					Class: dns.ClassINET, Ttl: 300,
				},
				A: net.IPv4(203, 0, 113, 99),
			}}
			_ = w.WriteMsg(reply)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String(), &count
}

// TestDedupCollapsesConcurrentQueries: a burst of identical queries costs
// one upstream exchange; every client still gets a correct answer.
func TestDedupCollapsesConcurrentQueries(t *testing.T) {
	upstream, count := startSlowUpstream(t, 200*time.Millisecond)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
	})
	addr := srv.UDPAddr().String()

	const burst = 8
	var wg sync.WaitGroup
	answers := make([]*dns.Msg, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := new(dns.Msg)
			m.SetQuestion("burst.example.org.", dns.TypeA)
			c := &dns.Client{Timeout: 5 * time.Second}
			resp, _, err := c.Exchange(m, addr)
			if err == nil {
				answers[i] = resp
			}
		}(i)
	}
	wg.Wait()

	for i, resp := range answers {
		if resp == nil || len(resp.Answer) != 1 {
			t.Fatalf("client %d: missing answer", i)
		}
		if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(203, 0, 113, 99)) {
			t.Errorf("client %d: answer = %v", i, resp.Answer[0])
		}
	}
	if got := count.Load(); got != 1 {
		t.Errorf("upstream served %d exchanges for %d concurrent clients, want 1", got, burst)
	}
}

// TestDedupKeysAreDistinct: different names must not be collapsed together.
func TestDedupKeysAreDistinct(t *testing.T) {
	upstream, count := startSlowUpstream(t, 100*time.Millisecond)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
	})
	addr := srv.UDPAddr().String()

	var wg sync.WaitGroup
	for _, qname := range []string{"one.example.org", "two.example.org"} {
		wg.Add(1)
		go func(qname string) {
			defer wg.Done()
			query(t, addr, qname, dns.TypeA)
		}(qname)
	}
	wg.Wait()
	if got := count.Load(); got != 2 {
		t.Errorf("upstream served %d, want 2 distinct exchanges", got)
	}
}

func TestServeStaleUnit(t *testing.T) {
	c, now := testCache(config.CacheConfig{MaxEntries: 100, MaxTTL: 3600, ServeStale: true})
	req, resp := aResponse("stale.example.com", 300)
	key := cacheKey("stale.example.com", dns.TypeA, req)
	c.put(key, resp)

	// Fresh hit first.
	if got, stale := c.get(key, req); got == nil || stale {
		t.Fatalf("fresh get = (%v, %v), want hit and not stale", got != nil, stale)
	}
	// Expired but inside the stale window: served with the short stale TTL.
	*now = now.Add(301 * time.Second)
	got, stale := c.get(key, req)
	if got == nil || !stale {
		t.Fatalf("expired get = (%v, %v), want stale hit", got != nil, stale)
	}
	if ttl := got.Answer[0].Header().Ttl; ttl != staleTTL {
		t.Errorf("stale TTL = %d, want %d", ttl, staleTTL)
	}
	// Past the stale window: a real miss, entry evicted.
	*now = now.Add(staleWindow)
	if hit, _ := c.get(key, req); hit != nil {
		t.Error("expected miss past the stale window")
	}
	if c.size.Load() != 0 {
		t.Errorf("size = %d after stale-window expiry, want 0", c.size.Load())
	}
}

// TestServeStaleRefreshes: a stale hit answers instantly and triggers a
// background refresh that repopulates the cache.
func TestServeStaleRefreshes(t *testing.T) {
	upstream, count := startCountingUpstream(t)
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
		c.DNS.Cache.MinTTL = 0 // let the 1s TTL below expire naturally
	})
	addr := srv.UDPAddr().String()

	// Prime the cache, then force the entry to expire by rewinding its
	// stored time (the cache instance is live on the server).
	query(t, addr, "blinky.example.org", dns.TypeA)
	cache := srv.cache.Load()
	key := cacheKey("blinky.example.org", dns.TypeA, new(dns.Msg))
	v, ok := cache.entries.Load(key)
	if !ok {
		t.Fatal("primed entry not found")
	}
	e := v.(*cacheEntry)
	e.expires = time.Now().Add(-time.Minute) // expired, within stale window

	resp := query(t, addr, "blinky.example.org", dns.TypeA)
	if len(resp.Answer) != 1 || resp.Answer[0].Header().Ttl != staleTTL {
		t.Fatalf("stale serve: answers=%d ttl=%d, want 1 answer at TTL %d",
			len(resp.Answer), resp.Answer[0].Header().Ttl, staleTTL)
	}

	// The docket labels it, and the background refresh reaches upstream.
	waitFor(t, func() bool {
		refreshed := count.Load() == 2
		var labelled bool
		for _, e := range qlog.Recent(0) {
			if e.Upstream == "stale" {
				labelled = true
			}
		}
		return refreshed && labelled
	}, "stale docket entry + background refresh")

	// The refreshed entry is fresh again: full TTL, no stale flag.
	waitFor(t, func() bool {
		hit, stale := cache.get(key, new(dns.Msg))
		return hit != nil && !stale
	}, "cache repopulated by refresh")
}
