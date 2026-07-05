//go:build acme_e2e

package acme

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"minos/internal/config"
)

// This file runs the full ACME protocol against Pebble (Let's Encrypt's
// test CA) with pebble-challtestsrv playing the DNS provider. It is build-
// tagged: CI starts the two services and runs
//
//	go test -tags acme_e2e ./internal/acme -run TestE2E
//
// with PEBBLE_DIR_URL and CHALLTESTSRV_URL set.

// challtestProvider fulfils DNS-01 via challtestsrv's management API.
type challtestProvider struct {
	base string
}

func (p *challtestProvider) post(ctx context.Context, path string, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.base+path, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("challtestsrv %s: status %s", path, resp.Status)
	}
	return nil
}

func (p *challtestProvider) Present(ctx context.Context, fqdn, txt string) error {
	return p.post(ctx, "/set-txt",
		fmt.Sprintf(`{"host": %q, "value": %q}`, fqdn+".", txt))
}

func (p *challtestProvider) Cleanup(ctx context.Context, fqdn, txt string) error {
	return p.post(ctx, "/clear-txt", fmt.Sprintf(`{"host": %q}`, fqdn+"."))
}

func TestE2EIssuance(t *testing.T) {
	dirURL := os.Getenv("PEBBLE_DIR_URL")
	mgmtURL := os.Getenv("CHALLTESTSRV_URL")
	dnsAddr := os.Getenv("CHALLTESTSRV_DNS")
	if dirURL == "" || mgmtURL == "" || dnsAddr == "" {
		t.Skip("PEBBLE_DIR_URL / CHALLTESTSRV_URL / CHALLTESTSRV_DNS not set")
	}

	// The propagation poll must ask challtestsrv's mock DNS, and the ACME
	// transport must accept Pebble's self-signed directory certificate.
	oldResolvers, oldInterval := propagationResolvers, propagationInterval
	propagationResolvers = []string{dnsAddr}
	propagationInterval = time.Second
	defer func() { propagationResolvers, propagationInterval = oldResolvers, oldInterval }()

	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:        "e2e@example.test",
		Domain:       "dns.example.test",
		Provider:     "cloudflare", // replaced below; config just needs to validate
		APIToken:     "unused",
		DirectoryURL: dirURL,
		CacheDir:     dir,
	}
	m, err := NewManager(cfg, filepath.Join(dir, "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m.provider = &challtestProvider{base: mgmtURL}
	m.httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := m.ensure(ctx); err != nil {
		t.Fatalf("issuance against Pebble failed: %v", err)
	}

	cert, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after issuance: %v", err)
	}
	if len(cert.Leaf.DNSNames) == 0 || cert.Leaf.DNSNames[0] != "dns.example.test" {
		t.Errorf("issued cert DNSNames = %v", cert.Leaf.DNSNames)
	}
	if needsRenewal(cert.Leaf, time.Now()) {
		t.Error("freshly issued cert already inside the renewal window")
	}

	// The cache must round-trip: a second manager picks the cert up
	// without touching the CA.
	m2, err := NewManager(cfg, filepath.Join(dir, "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.GetCertificate(nil); err != nil {
		t.Errorf("cached cert not loaded by a fresh manager: %v", err)
	}
}
