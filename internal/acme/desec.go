package acme

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// desecProvider drives the deSEC.io API: RRsets are addressed by domain
// (zone) + subname, so _acme-challenge.dns.example.com under the zone
// example.com has subname "_acme-challenge.dns".
type desecProvider struct {
	token string
	base  string // test override; empty = the real API
}

const desecAPI = "https://desec.io/api/v1"

func (p *desecProvider) api() string {
	if p.base != "" {
		return p.base
	}
	return desecAPI
}

func (p *desecProvider) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.api()+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Token "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, err
}

// zone finds the registered deSEC domain containing fqdn by walking parent
// suffixes, mirroring the Cloudflare zone walk.
func (p *desecProvider) zone(ctx context.Context, fqdn string) (string, error) {
	name := strings.TrimSuffix(fqdn, ".")
	for {
		status, _, err := p.do(ctx, http.MethodGet, "/domains/"+name+"/", nil)
		if err != nil {
			return "", err
		}
		if status == http.StatusOK {
			return name, nil
		}
		i := strings.IndexByte(name, '.')
		if i < 0 || !strings.Contains(name[i+1:], ".") {
			return "", fmt.Errorf("desec: no registered domain found for %s", fqdn)
		}
		name = name[i+1:]
	}
}

func (p *desecProvider) rrsetPath(zone, fqdn string) string {
	subname := strings.TrimSuffix(strings.TrimSuffix(fqdn, "."), "."+zone)
	return "/domains/" + zone + "/rrsets/" + subname + "/TXT/"
}

func (p *desecProvider) Present(ctx context.Context, fqdn, txt string) error {
	zone, err := p.zone(ctx, fqdn)
	if err != nil {
		return err
	}
	// PUT replaces the whole RRset — idempotent, which is what we want.
	status, data, err := p.do(ctx, http.MethodPut, p.rrsetPath(zone, fqdn), map[string]any{
		"ttl":     3600, // deSEC's minimum
		"records": []string{`"` + txt + `"`},
	})
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("desec: put rrset: status %d: %s", status, data)
	}
	return nil
}

func (p *desecProvider) Cleanup(ctx context.Context, fqdn, txt string) error {
	zone, err := p.zone(ctx, fqdn)
	if err != nil {
		return err
	}
	status, data, err := p.do(ctx, http.MethodDelete, p.rrsetPath(zone, fqdn), nil)
	if err != nil {
		return err
	}
	if status >= 300 && status != http.StatusNotFound {
		return fmt.Errorf("desec: delete rrset: status %d: %s", status, data)
	}
	return nil
}
