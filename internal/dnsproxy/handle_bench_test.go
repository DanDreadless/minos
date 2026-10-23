package dnsproxy

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

// discardWriter is a dns.ResponseWriter that keeps nothing: the benchmark
// measures handle() itself, not a socket.
type discardWriter struct{ remote net.Addr }

func (w *discardWriter) LocalAddr() net.Addr         { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53} }
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
	srv, _ := startProxyCfg(b, "zero_ip", nil, "ads.example.com")
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
