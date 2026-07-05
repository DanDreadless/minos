package acme

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// cloudflareProvider drives the Cloudflare v4 API with a scoped token
// (Zone.Zone read + Zone.DNS edit).
type cloudflareProvider struct {
	token string
	base  string // test override; empty = the real API
}

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

func (p *cloudflareProvider) api() string {
	if p.base != "" {
		return p.base
	}
	return cloudflareAPI
}

type cfResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (p *cloudflareProvider) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.api()+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out cfResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("cloudflare: parse response: %w", err)
	}
	if !out.Success {
		msg := "unknown error"
		if len(out.Errors) > 0 {
			msg = out.Errors[0].Message
		}
		return nil, fmt.Errorf("cloudflare: %s", msg)
	}
	return out.Result, nil
}

// zoneID finds the zone containing fqdn by walking parent suffixes:
// _acme-challenge.dns.example.com → dns.example.com → example.com.
func (p *cloudflareProvider) zoneID(ctx context.Context, fqdn string) (string, error) {
	name := strings.TrimSuffix(fqdn, ".")
	for {
		result, err := p.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(name), nil)
		if err != nil {
			return "", err
		}
		var zones []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(result, &zones); err != nil {
			return "", fmt.Errorf("cloudflare: parse zones: %w", err)
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
		i := strings.IndexByte(name, '.')
		if i < 0 || !strings.Contains(name[i+1:], ".") {
			return "", fmt.Errorf("cloudflare: no zone found for %s (token needs Zone read)", fqdn)
		}
		name = name[i+1:]
	}
}

func (p *cloudflareProvider) Present(ctx context.Context, fqdn, txt string) error {
	zone, err := p.zoneID(ctx, fqdn)
	if err != nil {
		return err
	}
	_, err = p.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", map[string]any{
		"type":    "TXT",
		"name":    fqdn,
		"content": txt,
		"ttl":     60,
	})
	return err
}

func (p *cloudflareProvider) Cleanup(ctx context.Context, fqdn, txt string) error {
	zone, err := p.zoneID(ctx, fqdn)
	if err != nil {
		return err
	}
	result, err := p.do(ctx, http.MethodGet,
		"/zones/"+zone+"/dns_records?type=TXT&name="+url.QueryEscape(strings.TrimSuffix(fqdn, ".")), nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result, &records); err != nil {
		return fmt.Errorf("cloudflare: parse records: %w", err)
	}
	for _, r := range records {
		if strings.Trim(r.Content, `"`) != txt {
			continue
		}
		if _, err := p.do(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+r.ID, nil); err != nil {
			return err
		}
	}
	return nil
}
