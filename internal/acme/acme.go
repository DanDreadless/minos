// Package acme obtains and renews the DoT/DoH certificate automatically
// via the ACME DNS-01 challenge — the only challenge type a LAN-only host
// can complete. The protocol (JWS, accounts, orders) is handled by
// golang.org/x/crypto/acme; this package orchestrates issuance, fulfils
// the challenge through a DNS provider API, and swaps renewed certificates
// into live listeners atomically.
package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	xacme "golang.org/x/crypto/acme"

	"minos/internal/config"
)

const (
	// letsEncryptURL is the default CA directory.
	letsEncryptURL = "https://acme-v02.api.letsencrypt.org/directory"
	// renewBefore renews once less than this much validity remains.
	renewBefore = 30 * 24 * time.Hour
	// checkInterval is the renewal-check cadence once a cert is held.
	checkInterval = 24 * time.Hour
	// retryInterval is the cadence while issuance is failing.
	retryInterval = time.Hour
	issueTimeout  = 10 * time.Minute
)

// Manager owns the certificate lifecycle for one domain.
type Manager struct {
	cfg      config.ACMEConfig
	cacheDir string
	provider Provider
	cert     atomic.Pointer[tls.Certificate]
	// httpClient overrides the ACME transport in tests (Pebble's
	// directory serves a self-signed certificate).
	httpClient *http.Client

	// onFailure, when set (before Run), fires once per failure streak so
	// a notification can go out without spamming on every hourly retry.
	onFailure func(err error)
	failing   bool
}

// NewManager builds a manager from validated config. configPath anchors the
// default cache directory next to the config file.
func NewManager(cfg config.ACMEConfig, configPath string) (*Manager, error) {
	provider, err := newProvider(cfg)
	if err != nil {
		return nil, err
	}
	dir := cfg.CacheDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(configPath), "acme")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create acme cache dir: %w", err)
	}
	m := &Manager{cfg: cfg, cacheDir: dir, provider: provider}
	m.loadCachedCert()
	return m, nil
}

// OnFailure registers the issuance-failure callback. Call before Run.
func (m *Manager) OnFailure(fn func(err error)) { m.onFailure = fn }

// GetCertificate implements tls.Config.GetCertificate: the current
// certificate, or a clear error before the first issuance completes.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c := m.cert.Load(); c != nil {
		return c, nil
	}
	return nil, errors.New("acme: certificate not issued yet — check the minos logs")
}

// Run ensures a certificate now and keeps it renewed until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		interval := checkInterval
		if err := m.ensure(ctx); err != nil {
			interval = retryInterval
			slog.Warn("acme issuance failed; will retry",
				"domain", m.cfg.Domain, "retry_in", interval, "err", err)
			if !m.failing && m.onFailure != nil {
				m.onFailure(err)
			}
			m.failing = true
		} else {
			m.failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ensure issues a certificate when none is held or renewal is due.
func (m *Manager) ensure(ctx context.Context) error {
	if c := m.cert.Load(); c != nil && !needsRenewal(c.Leaf, time.Now()) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()
	return m.issue(ctx)
}

// needsRenewal reports whether the certificate is inside the renewal
// window (or already expired / unparsed).
func needsRenewal(leaf *x509.Certificate, now time.Time) bool {
	if leaf == nil {
		return true
	}
	return now.After(leaf.NotAfter.Add(-renewBefore))
}

// issue runs one full ACME order and swaps the result in on success.
func (m *Manager) issue(ctx context.Context) error {
	client, err := m.client(ctx)
	if err != nil {
		return err
	}

	slog.Info("acme: ordering certificate", "domain", m.cfg.Domain)
	order, err := client.AuthorizeOrder(ctx, xacme.DomainIDs(m.cfg.Domain))
	if err != nil {
		return fmt.Errorf("authorize order: %w", err)
	}

	for _, authzURL := range order.AuthzURLs {
		if err := m.fulfil(ctx, client, authzURL); err != nil {
			return err
		}
	}

	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return fmt.Errorf("wait order: %w", err)
	}

	// Fresh leaf key per issuance.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate leaf key: %w", err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: m.cfg.Domain},
		DNSNames: []string{m.cfg.Domain},
	}, leafKey)
	if err != nil {
		return fmt.Errorf("create csr: %w", err)
	}
	chainDER, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		// Some CAs (Pebble among them) answer the finalize POST with the
		// order in "processing" and no Location header, which trips
		// CreateOrderCert's internal wait (it polls an empty URL). We hold
		// the real order URI, so poll it ourselves and fetch the cert.
		o, werr := client.WaitOrder(ctx, order.URI)
		if werr != nil || o.CertURL == "" {
			return fmt.Errorf("finalize order: %w", err)
		}
		chainDER, err = client.FetchCert(ctx, o.CertURL, true)
		if err != nil {
			return fmt.Errorf("fetch certificate: %w", err)
		}
	}

	cert, err := assembleCert(chainDER, leafKey)
	if err != nil {
		return err
	}
	if err := m.saveCert(chainDER, leafKey); err != nil {
		// The cert is valid even if caching failed; serve it and complain.
		slog.Warn("acme: certificate issued but caching failed", "err", err)
	}
	m.cert.Store(cert)
	slog.Info("acme: certificate issued",
		"domain", m.cfg.Domain, "expires", cert.Leaf.NotAfter.Format(time.RFC3339))
	return nil
}

// fulfil completes one authorization via the DNS-01 challenge.
func (m *Manager) fulfil(ctx context.Context, client *xacme.Client, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("get authorization: %w", err)
	}
	if authz.Status == xacme.StatusValid {
		return nil
	}
	var challenge *xacme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "dns-01" {
			challenge = c
			break
		}
	}
	if challenge == nil {
		return errors.New("CA offered no dns-01 challenge")
	}
	txt, err := client.DNS01ChallengeRecord(challenge.Token)
	if err != nil {
		return fmt.Errorf("challenge record: %w", err)
	}
	fqdn := "_acme-challenge." + m.cfg.Domain

	slog.Info("acme: publishing challenge record", "fqdn", fqdn, "provider", m.cfg.Provider)
	if err := m.provider.Present(ctx, fqdn, txt); err != nil {
		return fmt.Errorf("publish TXT via %s: %w", m.cfg.Provider, err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.provider.Cleanup(cleanupCtx, fqdn, txt); err != nil {
			slog.Debug("acme: challenge record cleanup failed", "err", err)
		}
	}()

	if err := waitForTXT(ctx, fqdn, txt); err != nil {
		return err
	}
	if _, err := client.Accept(ctx, challenge); err != nil {
		return fmt.Errorf("accept challenge: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
		return fmt.Errorf("authorization failed: %w", err)
	}
	return nil
}

// client builds the ACME client with the (created-on-first-use) account.
func (m *Manager) client(ctx context.Context) (*xacme.Client, error) {
	key, err := m.accountKey()
	if err != nil {
		return nil, err
	}
	dir := m.cfg.DirectoryURL
	if dir == "" {
		dir = letsEncryptURL
	}
	client := &xacme.Client{Key: key, DirectoryURL: dir, UserAgent: "minos-dns"}
	if m.httpClient != nil {
		client.HTTPClient = m.httpClient
	}
	_, err = client.Register(ctx, &xacme.Account{
		Contact: []string{"mailto:" + m.cfg.Email},
	}, xacme.AcceptTOS)
	if err != nil && !errors.Is(err, xacme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("register account: %w", err)
	}
	return client, nil
}

// accountKey loads or creates the persistent ACME account key.
func (m *Manager) accountKey() (*ecdsa.PrivateKey, error) {
	path := filepath.Join(m.cacheDir, "account.key")
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("parse %s: not PEM", path)
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("write account key: %w", err)
	}
	return key, nil
}

// saveCert caches the issued chain and leaf key as PEM.
func (m *Manager) saveCert(chainDER [][]byte, key *ecdsa.PrivateKey) error {
	var certPEM []byte
	for _, der := range chainDER {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(m.cacheDir, "cert.pem"), certPEM, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.cacheDir, "key.pem"), keyPEM, 0o600)
}

// loadCachedCert restores a previously issued certificate so restarts
// don't re-issue (Let's Encrypt rate limits are real).
func (m *Manager) loadCachedCert() {
	cert, err := tls.LoadX509KeyPair(
		filepath.Join(m.cacheDir, "cert.pem"),
		filepath.Join(m.cacheDir, "key.pem"))
	if err != nil {
		return // nothing cached yet
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || time.Now().After(leaf.NotAfter) {
		return // expired or unreadable: force re-issuance
	}
	cert.Leaf = leaf
	m.cert.Store(&cert)
	slog.Info("acme: using cached certificate",
		"domain", m.cfg.Domain, "expires", leaf.NotAfter.Format(time.RFC3339))
}

// assembleCert builds the tls.Certificate served to clients.
func assembleCert(chainDER [][]byte, key *ecdsa.PrivateKey) (*tls.Certificate, error) {
	if len(chainDER) == 0 {
		return nil, errors.New("CA returned an empty chain")
	}
	leaf, err := x509.ParseCertificate(chainDER[0])
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}
	return &tls.Certificate{
		Certificate: chainDER,
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}
