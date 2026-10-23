package dnsproxy

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// startQUICProxy is startEncryptedProxy with DoQ on too, returning the CA
// pool that trusts its self-signed certificate.
func startQUICProxy(t *testing.T) (*Server, *x509.CertPool) {
	t.Helper()
	certFile, keyFile := writeTestCert(t, t.TempDir())
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.TLS = config.TLSListeners{
			CertFile:  certFile,
			KeyFile:   keyFile,
			DoHListen: "127.0.0.1:0",
			DoQListen: "127.0.0.1:0",
		}
	}, "ads.example.com")
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return srv, pool
}

func TestDoQServesAndBlocks(t *testing.T) {
	srv, pool := startQUICProxy(t)
	up, err := newDoQUpstream(srv.doqAddr.String(), pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, c := range []struct {
		name    string
		blocked bool
	}{{"tracker.ads.example.com.", true}, {"example.org.", false}, {"tracker.ads.example.com.", true}} {
		m := new(dns.Msg)
		m.SetQuestion(c.name, dns.TypeA)
		m.Id = uint16(1000 + i)
		resp, err := up.Exchange(ctx, m)
		if err != nil {
			t.Fatalf("query %d over DoQ: %v", i, err)
		}
		if resp.Id != m.Id {
			t.Errorf("id %d, want the caller's %d restored", resp.Id, m.Id)
		}
		a, ok := resp.Answer[0].(*dns.A)
		if !ok || a.A.Equal(net.IPv4zero) != c.blocked {
			t.Errorf("%s over DoQ: %v", c.name, resp.Answer)
		}
	}
}

func TestDoH3Serves(t *testing.T) {
	srv, pool := startQUICProxy(t)
	if srv.doh3 == nil {
		t.Fatal("HTTP/3 did not start beside DoH")
	}
	up := newDoH3Upstream("https://"+srv.dohAddr.String()+"/dns-query", pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := new(dns.Msg)
	m.SetQuestion("tracker.ads.example.com.", dns.TypeA)
	resp, err := up.Exchange(ctx, m)
	if err != nil {
		t.Fatalf("DoH3: %v", err)
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4zero) {
		t.Errorf("DoH3 answer %v", resp.Answer)
	}
}

func TestDDRAdvertisesDoQAndH3(t *testing.T) {
	srv, _ := startQUICProxy(t)
	resp := query(t, srv.UDPAddr().String(), ddrQName, dns.TypeSVCB)
	var sawDoQ, sawH3 bool
	for _, rr := range resp.Answer {
		for _, kv := range rr.(*dns.SVCB).Value {
			if a, ok := kv.(*dns.SVCBAlpn); ok {
				for _, p := range a.Alpn {
					sawDoQ = sawDoQ || p == "doq"
					sawH3 = sawH3 || p == "h3"
				}
			}
		}
	}
	if !sawDoQ || !sawH3 {
		t.Errorf("DDR records lack doq (%v) or h3 (%v): %v", sawDoQ, sawH3, resp.Answer)
	}
}

// A connection that died (server restart, NAT timeout) is redialled on the
// next query rather than failing it.
func TestDoQUpstreamRedials(t *testing.T) {
	srv, pool := startQUICProxy(t)
	up, _ := newDoQUpstream(srv.doqAddr.String(), pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := func() error {
		m := new(dns.Msg)
		m.SetQuestion("example.org.", dns.TypeA)
		_, err := up.Exchange(ctx, m)
		return err
	}
	if err := q(); err != nil {
		t.Fatal(err)
	}
	first := up.conn.Load()
	_ = first.CloseWithError(doqNoError, "test")
	<-first.Context().Done()
	if err := q(); err != nil {
		t.Fatalf("query after the connection closed: %v", err)
	}
	if up.conn.Load() == first {
		t.Error("the dead connection was reused")
	}
}
