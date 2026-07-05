package acme

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// duckdnsProvider drives DuckDNS's one-endpoint API. DuckDNS hosts exactly
// one TXT record per subdomain, set or cleared for the whole name — fine
// for DNS-01, which needs exactly one value at a time.
type duckdnsProvider struct {
	token  string
	domain string // the configured certificate domain, e.g. myhome.duckdns.org
	base   string // test override; empty = the real API
}

const duckdnsAPI = "https://www.duckdns.org"

func (p *duckdnsProvider) api() string {
	if p.base != "" {
		return p.base
	}
	return duckdnsAPI
}

// subdomain extracts the DuckDNS name ("myhome" from myhome.duckdns.org).
func (p *duckdnsProvider) subdomain() string {
	name := strings.TrimSuffix(p.domain, ".")
	return strings.TrimSuffix(name, ".duckdns.org")
}

func (p *duckdnsProvider) update(ctx context.Context, params url.Values) error {
	params.Set("domains", p.subdomain())
	params.Set("token", p.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.api()+"/update?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if answer := strings.TrimSpace(string(body)); answer != "OK" {
		return fmt.Errorf("duckdns: update answered %q (check the token and subdomain)", answer)
	}
	return nil
}

func (p *duckdnsProvider) Present(ctx context.Context, _, txt string) error {
	return p.update(ctx, url.Values{"txt": []string{txt}})
}

func (p *duckdnsProvider) Cleanup(ctx context.Context, _, txt string) error {
	return p.update(ctx, url.Values{"txt": []string{txt}, "clear": []string{"true"}})
}
