package dnsproxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miekg/dns"
)

// TestDoHUpstreamSpeaksHTTP2 runs the DoH client against a server that, like
// Quad9, refuses HTTP/1.1 with 505. Before ForceAttemptHTTP2 every DoH
// upstream negotiated HTTP/1.1, so this server could never be reached.
func TestDoHUpstreamSpeaksHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			http.Error(w, "HTTP/2 required", http.StatusHTTPVersionNotSupported)
			return
		}
		body, _ := io.ReadAll(r.Body)
		q := new(dns.Msg)
		if err := q.Unpack(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := new(dns.Msg)
		resp.SetReply(q)
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.IPv4(192, 0, 2, 7),
		}}
		packed, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = io.Copy(w, bytes.NewReader(packed))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	up := newDoHUpstream(srv.URL+"/dns-query", srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs)
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	q.Id = 4242
	resp, err := up.Exchange(context.Background(), q)
	if err != nil {
		t.Fatalf("exchange with an HTTP/2-only DoH server: %v", err)
	}
	if resp.Id != 4242 {
		t.Errorf("response id = %d, want the query's 4242 restored", resp.Id)
	}
	if len(resp.Answer) != 1 || resp.Answer[0].(*dns.A).A.String() != "192.0.2.7" {
		t.Errorf("answer = %v, want 192.0.2.7", resp.Answer)
	}
}
