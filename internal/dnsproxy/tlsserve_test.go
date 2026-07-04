package dnsproxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/querylog"
)

// writeTestCert generates a self-signed cert for 127.0.0.1/localhost and
// writes PEM files into dir.
func writeTestCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "minos-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func startEncryptedProxy(t *testing.T) *Server {
	t.Helper()
	certFile, keyFile := writeTestCert(t, t.TempDir())
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.TLS = config.TLSListeners{
			CertFile:  certFile,
			KeyFile:   keyFile,
			DoTListen: "127.0.0.1:0",
			DoHListen: "127.0.0.1:0",
		}
	}, "ads.example.com")
	return srv
}

func TestDoTServes(t *testing.T) {
	srv := startEncryptedProxy(t)

	c := &dns.Client{
		Net:       "tcp-tls",
		Timeout:   5 * time.Second,
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	}
	// Filtering applies over DoT: the denylisted name sinks to 0.0.0.0.
	m := new(dns.Msg)
	m.SetQuestion("ads.example.com.", dns.TypeA)
	resp, _, err := c.Exchange(m, srv.DoTAddr().String())
	if err != nil {
		t.Fatalf("dot exchange: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4zero) {
		t.Errorf("blocked answer over DoT = %v, want 0.0.0.0", resp.Answer[0])
	}

	// Allowed names forward to the stub upstream.
	m2 := new(dns.Msg)
	m2.SetQuestion("innocent.example.org.", dns.TypeA)
	resp2, _, err := c.Exchange(m2, srv.DoTAddr().String())
	if err != nil {
		t.Fatalf("dot exchange: %v", err)
	}
	if a, ok := resp2.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(93, 184, 216, 34)) {
		t.Errorf("forwarded answer over DoT = %v, want stub upstream's", resp2.Answer[0])
	}
}

func dohClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func packQuery(t *testing.T, qname string) []byte {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), dns.TypeA)
	packed, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return packed
}

func unpackResponse(t *testing.T, resp *http.Response) *dns.Msg {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doh status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/dns-message" {
		t.Fatalf("Content-Type = %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := new(dns.Msg)
	if err := out.Unpack(body); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDoHPost(t *testing.T) {
	srv := startEncryptedProxy(t)
	url := "https://" + srv.DoHAddr().String() + "/dns-query"

	resp, err := dohClient().Post(url, "application/dns-message",
		bytes.NewReader(packQuery(t, "ads.example.com")))
	if err != nil {
		t.Fatal(err)
	}
	msg := unpackResponse(t, resp)
	if len(msg.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(msg.Answer))
	}
	if a, ok := msg.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4zero) {
		t.Errorf("blocked answer over DoH = %v, want 0.0.0.0", msg.Answer[0])
	}
}

func TestDoHGet(t *testing.T) {
	srv := startEncryptedProxy(t)
	b64 := base64.RawURLEncoding.EncodeToString(packQuery(t, "innocent.example.org"))
	url := "https://" + srv.DoHAddr().String() + "/dns-query?dns=" + b64

	resp, err := dohClient().Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "max-age=300" {
		t.Errorf("Cache-Control = %q, want max-age=300 (stub upstream TTL)", cc)
	}
	msg := unpackResponse(t, resp)
	if a, ok := msg.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(93, 184, 216, 34)) {
		t.Errorf("forwarded answer over DoH = %v, want stub upstream's", msg.Answer[0])
	}
}

func TestDoHRejectsGarbage(t *testing.T) {
	srv := startEncryptedProxy(t)
	base := "https://" + srv.DoHAddr().String() + "/dns-query"
	client := dohClient()

	cases := []struct {
		name string
		do   func() (*http.Response, error)
		want int
	}{
		{"missing dns param", func() (*http.Response, error) {
			return client.Get(base)
		}, http.StatusBadRequest},
		{"bad base64", func() (*http.Response, error) {
			return client.Get(base + "?dns=!!!!")
		}, http.StatusBadRequest},
		{"not a dns message", func() (*http.Response, error) {
			return client.Post(base, "application/dns-message", bytes.NewReader([]byte("junk")))
		}, http.StatusBadRequest},
		{"wrong content type", func() (*http.Response, error) {
			return client.Post(base, "text/plain", bytes.NewReader(packQuery(t, "x.example")))
		}, http.StatusUnsupportedMediaType},
		{"wrong method", func() (*http.Response, error) {
			req, _ := http.NewRequest(http.MethodDelete, base, nil)
			return client.Do(req)
		}, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		resp, err := tc.do()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}

func TestTLSConfigValidation(t *testing.T) {
	cfg := config.Default()
	cfg.DNS.TLS = config.TLSListeners{DoTListen: ":853"} // no cert
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted TLS listeners without a certificate")
	}
	cfg.DNS.TLS = config.TLSListeners{CertFile: "c.pem", KeyFile: "k.pem", DoHListen: "not-a-hostport"}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted a malformed doh_listen")
	}
}

func TestTLSMissingCertFailsStart(t *testing.T) {
	engine := filter.NewEngine()
	qlog, err := querylog.Open(querylog.Options{RingSize: 10, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer qlog.Close()
	cfg := config.Default()
	cfg.DNS.Listen = "127.0.0.1:0"
	cfg.DNS.TLS = config.TLSListeners{
		CertFile:  "does-not-exist.pem",
		KeyFile:   "does-not-exist.pem",
		DoTListen: "127.0.0.1:0",
	}
	srv, err := New(cfg, engine, qlog, clients.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err == nil {
		t.Error("Start() succeeded with a missing certificate")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
