package dnsproxy

import (
	"net"
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// discardWriter is a dns.ResponseWriter that keeps nothing: the benchmark
// measures handle() itself, not a socket.
type discardWriter struct{ remote net.Addr }

func (w *discardWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}
}
func (w *discardWriter) RemoteAddr() net.Addr        { return w.remote }
func (w *discardWriter) WriteMsg(*dns.Msg) error     { return nil }
func (w *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *discardWriter) Close() error                { return nil }
func (w *discardWriter) TsigStatus() error           { return nil }
func (w *discardWriter) TsigTimersOnly(bool)         {}
func (w *discardWriter) Hijack()                     {}

// BenchmarkHandleBlocked is the blocked-query hot path end to end through
// handle(): policy lookup, match, the synthesized answer, the log record.
func BenchmarkHandleBlocked(b *testing.B) {
	// The limiter stays on (its cost is part of the path) but high enough
	// never to refuse: a benchmark asks far more than 1000 times a minute.
	srv, _ := startProxyCfg(b, "zero_ip", func(c *config.Config) { c.DNS.RateLimit = 1000000 }, "ads.example.com")
	w := &discardWriter{remote: &net.UDPAddr{IP: net.IPv4(192, 168, 1, 50), Port: 40000}}
	for _, tc := range []struct {
		name string
		edns bool
	}{{"plain", false}, {"edns0", true}} {
		req := new(dns.Msg)
		req.SetQuestion("tracker.ads.example.com.", dns.TypeA)
		if tc.edns {
			req.SetEdns0(1232, false)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				srv.handle(w, req)
			}
		})
	}
}

// BenchmarkHandleCacheHit is an allowed name answered from the cache, the
// most common path of all — with CNAME inspection on (a CNAME in the
// answer is judged) and off.
func BenchmarkHandleCacheHit(b *testing.B) {
	for _, inspect := range []bool{false, true} {
		srv, _ := cloakProxy(b, inspect)
		w := &discardWriter{remote: &net.UDPAddr{IP: net.IPv4(192, 168, 1, 50), Port: 40000}}
		for _, name := range []string{"www.shop.example.", "metrics.shop.example."} {
			req := new(dns.Msg)
			req.SetQuestion(name, dns.TypeA)
			srv.handle(w, req) // warm the cache
			label := "plain"
			if name == "metrics.shop.example." {
				label = "cname"
			}
			if inspect {
				label += "/inspect"
			} else {
				label += "/off"
			}
			b.Run(label, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					srv.handle(w, req)
				}
			})
		}
	}
}
