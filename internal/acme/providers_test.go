package acme

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// --- deSEC ---

func TestDesecPresentCleanup(t *testing.T) {
	var rrsets sync.Map // path → body
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-token" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/domains/"):
			// Zone discovery: only example.com is registered.
			if r.URL.Path == "/domains/example.com/" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
		case r.Method == http.MethodPut:
			rrsets.Store(r.URL.Path, true)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			if _, ok := rrsets.LoadAndDelete(r.URL.Path); !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	defer ts.Close()

	p := &desecProvider{token: "test-token", base: ts.URL}
	ctx := context.Background()
	fqdn := "_acme-challenge.dns.example.com"
	wantPath := "/domains/example.com/rrsets/_acme-challenge.dns/TXT/"

	if err := p.Present(ctx, fqdn, "txt-value"); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if _, ok := rrsets.Load(wantPath); !ok {
		t.Errorf("rrset not created at %s", wantPath)
	}
	if err := p.Cleanup(ctx, fqdn, "txt-value"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, ok := rrsets.Load(wantPath); ok {
		t.Error("rrset survived Cleanup")
	}
	// Cleaning an already-gone rrset is not an error (404 tolerated).
	if err := p.Cleanup(ctx, fqdn, "txt-value"); err != nil {
		t.Errorf("second Cleanup: %v", err)
	}
}

// --- DuckDNS ---

func TestDuckDNSPresentCleanup(t *testing.T) {
	var last string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("token") != "test-token" || q.Get("domains") != "myhome" {
			_, _ = w.Write([]byte("KO"))
			return
		}
		last = q.Get("txt") + "|clear=" + q.Get("clear")
		_, _ = w.Write([]byte("OK"))
	}))
	defer ts.Close()

	p := &duckdnsProvider{token: "test-token", domain: "myhome.duckdns.org", base: ts.URL}
	ctx := context.Background()

	if err := p.Present(ctx, "_acme-challenge.myhome.duckdns.org", "txt-value"); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if last != "txt-value|clear=" {
		t.Errorf("Present sent %q", last)
	}
	if err := p.Cleanup(ctx, "_acme-challenge.myhome.duckdns.org", "txt-value"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !strings.HasSuffix(last, "clear=true") {
		t.Errorf("Cleanup sent %q, want clear=true", last)
	}

	// A KO answer surfaces as an error.
	bad := &duckdnsProvider{token: "wrong", domain: "myhome.duckdns.org", base: ts.URL}
	if err := bad.Present(ctx, "x", "v"); err == nil {
		t.Error("bad token Present succeeded, want error")
	}
}

// --- RFC 2136 ---

// startUpdateServer runs an in-process authoritative server for
// example.com that answers SOA discovery (UDP+TCP) and accepts
// TSIG-signed dynamic updates, recording the zone's TXT records.
func startUpdateServer(t *testing.T, tsigName, secret string) (addr string, records *sync.Map) {
	t.Helper()
	records = &sync.Map{}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(req)
		switch req.Opcode {
		case dns.OpcodeQuery:
			if len(req.Question) == 1 && req.Question[0].Qtype == dns.TypeSOA &&
				req.Question[0].Name == "example.com." {
				reply.Answer = []dns.RR{&dns.SOA{
					Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeSOA,
						Class: dns.ClassINET, Ttl: 300},
					Ns: "ns1.example.com.", Mbox: "admin.example.com.",
				}}
			}
		case dns.OpcodeUpdate:
			if req.IsTsig() == nil || w.TsigStatus() != nil {
				reply.SetRcode(req, dns.RcodeRefused)
				break
			}
			for _, rr := range req.Ns {
				txt, ok := rr.(*dns.TXT)
				if !ok {
					continue
				}
				switch rr.Header().Class {
				case dns.ClassINET:
					records.Store(txt.Hdr.Name+"|"+txt.Txt[0], true)
				case dns.ClassNONE: // remove this exact RR
					records.Delete(txt.Hdr.Name + "|" + txt.Txt[0])
				}
			}
			// Sign the response so the client's TSIG check passes.
			reply.SetTsig(tsigName, dns.HmacSHA256, 300, time.Now().Unix())
		}
		_ = w.WriteMsg(reply)
	})

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{tsigName: secret}
	// The default accept func rejects UPDATE with NOTIMP; let it through.
	accept := func(dh dns.Header) dns.MsgAcceptAction {
		if int(dh.Bits>>11)&0xf == dns.OpcodeUpdate {
			return dns.MsgAccept
		}
		return dns.DefaultMsgAcceptFunc(dh)
	}
	udpSrv := &dns.Server{PacketConn: pc, Handler: handler, TsigSecret: secrets, MsgAcceptFunc: accept}
	tcpSrv := &dns.Server{Listener: ln, Handler: handler, TsigSecret: secrets, MsgAcceptFunc: accept}
	go func() { _ = udpSrv.ActivateAndServe() }()
	go func() { _ = tcpSrv.ActivateAndServe() }()
	t.Cleanup(func() { _ = udpSrv.Shutdown(); _ = tcpSrv.Shutdown() })
	return pc.LocalAddr().String(), records
}

func TestRFC2136PresentCleanup(t *testing.T) {
	const secret = "c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0" // base64 "secret"×4
	addr, records := startUpdateServer(t, "minos-key.", secret)

	p, err := newRFC2136Provider(config.ACMEConfig{
		Provider:   "rfc2136",
		Server:     addr,
		TSIGName:   "minos-key",
		TSIGSecret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fqdn := "_acme-challenge.dns.example.com"
	key := dns.Fqdn(fqdn) + "|txt-value"

	if err := p.Present(ctx, fqdn, "txt-value"); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if _, ok := records.Load(key); !ok {
		t.Fatalf("TXT record not inserted (records: %v)", dumpKeys(records))
	}
	if err := p.Cleanup(ctx, fqdn, "txt-value"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, ok := records.Load(key); ok {
		t.Error("TXT record survived Cleanup")
	}
}

func TestRFC2136RejectsBadTSIG(t *testing.T) {
	const secret = "c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0"
	addr, _ := startUpdateServer(t, "minos-key.", secret)

	p, err := newRFC2136Provider(config.ACMEConfig{
		Provider:   "rfc2136",
		Server:     addr,
		TSIGName:   "minos-key",
		TSIGSecret: "d3JvbmdzZWNyZXR3cm9uZ3NlY3JldA==", // wrong key
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Present(context.Background(), "_acme-challenge.dns.example.com", "v"); err == nil {
		t.Error("update with wrong TSIG secret succeeded, want refusal")
	}
}

func TestRFC2136UnsupportedAlgorithm(t *testing.T) {
	_, err := newRFC2136Provider(config.ACMEConfig{
		Provider: "rfc2136", Server: "127.0.0.1:53",
		TSIGName: "k", TSIGSecret: "s", TSIGAlgorithm: "hmac-md5",
	})
	if err == nil {
		t.Error("hmac-md5 accepted, want unsupported-algorithm error")
	}
}

func dumpKeys(m *sync.Map) []string {
	var out []string
	m.Range(func(k, _ any) bool { out = append(out, k.(string)); return true })
	return out
}
