package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

func TestNeedsRenewal(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		notAfter time.Time
		want     bool
	}{
		{"plenty of validity", now.Add(60 * 24 * time.Hour), false},
		{"just outside window", now.Add(31 * 24 * time.Hour), false},
		{"inside window", now.Add(29 * 24 * time.Hour), true},
		{"expired", now.Add(-time.Hour), true},
	}
	for _, tc := range cases {
		leaf := &x509.Certificate{NotAfter: tc.notAfter}
		if got := needsRenewal(leaf, now); got != tc.want {
			t.Errorf("%s: needsRenewal = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !needsRenewal(nil, now) {
		t.Error("nil leaf must need renewal")
	}
}

// writeCachedCert fabricates a self-signed cert+key pair in dir, expiring
// at notAfter, in the exact files loadCachedCert reads.
func writeCachedCert(t *testing.T, dir, domain string, notAfter time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testACMEConfig(dir string) config.ACMEConfig {
	return config.ACMEConfig{
		Email:    "admin@example.com",
		Domain:   "dns.example.com",
		Provider: "cloudflare",
		APIToken: "test-token",
		CacheDir: dir,
	}
}

func TestManagerLoadsCachedCert(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, "dns.example.com", time.Now().Add(60*24*time.Hour))

	m, err := NewManager(testACMEConfig(dir), filepath.Join(dir, "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after cache load: %v", err)
	}
	if cert.Leaf == nil || cert.Leaf.DNSNames[0] != "dns.example.com" {
		t.Errorf("loaded cert = %+v, want the cached one", cert.Leaf)
	}
	// A healthy cached cert means ensure() is a no-op (no network).
	if err := m.ensure(context.Background()); err != nil {
		t.Errorf("ensure() with a fresh cached cert erred: %v", err)
	}
}

func TestManagerIgnoresExpiredCache(t *testing.T) {
	dir := t.TempDir()
	writeCachedCert(t, dir, "dns.example.com", time.Now().Add(-time.Hour))

	m, err := NewManager(testACMEConfig(dir), filepath.Join(dir, "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetCertificate(nil); err == nil {
		t.Error("expired cached cert was served; want not-issued-yet error")
	}
}

func TestAccountKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(testACMEConfig(dir), filepath.Join(dir, "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	k1, err := m.accountKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := m.accountKey()
	if err != nil {
		t.Fatal(err)
	}
	if !k1.Equal(k2) {
		t.Error("account key not stable across loads")
	}
}

// --- Cloudflare provider ---

// cfFake is a minimal Cloudflare v4 API: one zone, records in memory.
type cfFake struct {
	t       *testing.T
	records map[string]map[string]string // id → {name, content}
	nextID  int
}

func (f *cfFake) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, `{"success":false,"errors":[{"message":"bad token"}]}`, http.StatusForbidden)
			return
		}
		write := func(result any) {
			raw, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": json.RawMessage(raw)})
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == "example.com" {
				write([]map[string]string{{"id": "zone1"}})
				return
			}
			write([]map[string]string{})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone1/dns_records":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["type"] != "TXT" {
				f.t.Errorf("record type = %v, want TXT", body["type"])
			}
			f.nextID++
			id := "rec" + string(rune('0'+f.nextID))
			f.records[id] = map[string]string{
				"name":    body["name"].(string),
				"content": body["content"].(string),
			}
			write(map[string]string{"id": id})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone1/dns_records":
			var out []map[string]string
			for id, rec := range f.records {
				if rec["name"] == r.URL.Query().Get("name") {
					out = append(out, map[string]string{"id": id, "content": rec["content"]})
				}
			}
			write(out)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/zone1/dns_records/"):
			id := strings.TrimPrefix(r.URL.Path, "/zones/zone1/dns_records/")
			delete(f.records, id)
			write(map[string]string{"id": id})
		default:
			f.t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, `{"success":false}`, http.StatusNotFound)
		}
	})
}

func TestCloudflarePresentCleanup(t *testing.T) {
	fake := &cfFake{t: t, records: map[string]map[string]string{}}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	p := &cloudflareProvider{token: "test-token", base: ts.URL}
	ctx := context.Background()
	fqdn := "_acme-challenge.dns.example.com"

	if err := p.Present(ctx, fqdn, "txt-value-1"); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if len(fake.records) != 1 {
		t.Fatalf("records after Present = %d, want 1", len(fake.records))
	}
	for _, rec := range fake.records {
		if rec["name"] != fqdn || rec["content"] != "txt-value-1" {
			t.Errorf("record = %v", rec)
		}
	}

	// Cleanup removes only the matching value.
	if err := p.Present(ctx, fqdn, "txt-value-2"); err != nil {
		t.Fatal(err)
	}
	if err := p.Cleanup(ctx, fqdn, "txt-value-1"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(fake.records) != 1 {
		t.Fatalf("records after Cleanup = %d, want the other value to survive", len(fake.records))
	}
	for _, rec := range fake.records {
		if rec["content"] != "txt-value-2" {
			t.Errorf("surviving record = %v, want txt-value-2", rec)
		}
	}
}

func TestCloudflareZoneWalk(t *testing.T) {
	// The zone query for the full name misses; the walk must find the
	// parent zone "example.com".
	fake := &cfFake{t: t, records: map[string]map[string]string{}}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()
	p := &cloudflareProvider{token: "test-token", base: ts.URL}
	zone, err := p.zoneID(context.Background(), "_acme-challenge.deep.sub.dns.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if zone != "zone1" {
		t.Errorf("zone = %q, want zone1", zone)
	}
}

// --- propagation poller ---

func TestWaitForTXT(t *testing.T) {
	// A local DNS server that starts answering the TXT after two queries.
	var served int
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{
		PacketConn: pc,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			served++
			reply := new(dns.Msg)
			reply.SetReply(req)
			if served > 2 {
				reply.Answer = []dns.RR{&dns.TXT{
					Hdr: dns.RR_Header{
						Name: req.Question[0].Name, Rrtype: dns.TypeTXT,
						Class: dns.ClassINET, Ttl: 60,
					},
					Txt: []string{"expected-value"},
				}}
			}
			_ = w.WriteMsg(reply)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })

	oldResolvers, oldInterval := propagationResolvers, propagationInterval
	propagationResolvers = []string{pc.LocalAddr().String()}
	propagationInterval = 20 * time.Millisecond
	defer func() { propagationResolvers, propagationInterval = oldResolvers, oldInterval }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForTXT(ctx, "_acme-challenge.dns.example.com", "expected-value"); err != nil {
		t.Fatalf("waitForTXT: %v", err)
	}
	if served < 3 {
		t.Errorf("resolver served %d queries, want at least 3 (polled until visible)", served)
	}
}
