package dnsproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

const (
	upstreamTimeout = 3 * time.Second
	// maxDoHResponse caps a DoH body read; a DNS message cannot legally
	// exceed 64 KiB.
	maxDoHResponse = 64 << 10
)

// Upstream resolves a single forwarded query against one configured resolver.
type Upstream interface {
	Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, error)
	Name() string
}

// NewUpstream builds the right transport for one config entry. The config
// is validated before it gets here; unknown protocols are a bug.
func NewUpstream(u config.Upstream) (Upstream, error) {
	switch u.Protocol {
	case "udp":
		return &classicUpstream{
			addr: u.Address,
			udp:  &dns.Client{Net: "udp", Timeout: upstreamTimeout},
			// Truncated UDP answers retry over TCP.
			tcp: &dns.Client{Net: "tcp", Timeout: upstreamTimeout},
		}, nil
	case "tcp":
		return &classicUpstream{
			addr: u.Address,
			udp:  nil,
			tcp:  &dns.Client{Net: "tcp", Timeout: upstreamTimeout},
		}, nil
	case "dot":
		host, _, err := net.SplitHostPort(u.Address)
		if err != nil {
			return nil, fmt.Errorf("dot upstream %q: %w", u.Address, err)
		}
		return &classicUpstream{
			addr: u.Address,
			tcp: &dns.Client{
				Net:       "tcp-tls",
				Timeout:   upstreamTimeout,
				TLSConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
			},
		}, nil
	case "doh":
		return newDoHUpstream(u.Address, nil), nil
	case "doh3":
		return newDoH3Upstream(u.Address, nil), nil
	case "doq":
		return newDoQUpstream(u.Address, nil)
	default:
		return nil, fmt.Errorf("unknown upstream protocol %q", u.Protocol)
	}
}

// classicUpstream is plain UDP/TCP or DoT (TCP wrapped in TLS).
type classicUpstream struct {
	addr string
	udp  *dns.Client // nil for tcp/dot
	tcp  *dns.Client
}

func (c *classicUpstream) Name() string { return c.addr }

func (c *classicUpstream) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, error) {
	if c.udp != nil {
		resp, _, err := c.udp.ExchangeContext(ctx, msg, c.addr)
		if err == nil && !resp.Truncated {
			return resp, nil
		}
		if err == nil && resp.Truncated {
			// Fall through to TCP retry
		} else if ctx.Err() != nil {
			return nil, err
		}
	}
	resp, _, err := c.tcp.ExchangeContext(ctx, msg, c.addr)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// newDoHUpstream builds a DoH client. roots overrides the system trust store
// (tests only; nil in production).
//
// HTTP/2 must be forced: a Transport with a custom TLSClientConfig silently
// disables Go's automatic HTTP/2, and that shipped — every DoH upstream spoke
// HTTP/1.1, so Quad9 (which answers HTTP/1.1 with 505) could never work and
// the rest lost request multiplexing. The pings retire a connection a NAT or
// firewall dropped silently, instead of stalling queries on it until the
// exchange timeout.
func newDoHUpstream(url string, roots *x509.CertPool) *dohUpstream {
	return &dohUpstream{
		url: url,
		client: &http.Client{
			Timeout: upstreamTimeout,
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
				ForceAttemptHTTP2:   true,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
				HTTP2: &http.HTTP2Config{
					SendPingTimeout: 30 * time.Second,
					PingTimeout:     5 * time.Second,
				},
			},
		},
	}
}

// dohUpstream speaks RFC 8484 (POST, application/dns-message).
type dohUpstream struct {
	url    string
	client *http.Client
}

func (d *dohUpstream) Name() string { return d.url }

func (d *dohUpstream) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, error) {
	// RFC 8484 wants ID 0 for cache friendliness; restore it on the way out.
	wire := msg.Copy()
	origID := msg.Id
	wire.Id = 0
	packed, err := wire.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(packed))
	if err != nil {
		return nil, fmt.Errorf("build doh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doh request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDoHResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read doh response: %w", err)
	}
	if len(body) > maxDoHResponse {
		return nil, fmt.Errorf("doh response exceeds %d bytes", maxDoHResponse)
	}
	out := new(dns.Msg)
	if err := out.Unpack(body); err != nil {
		return nil, fmt.Errorf("unpack doh response: %w", err)
	}
	out.Id = origID
	return out, nil
}
