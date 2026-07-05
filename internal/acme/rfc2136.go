package acme

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// rfc2136Provider sends TSIG-signed dynamic updates (RFC 2136) — the path
// for people running their own authoritative DNS (BIND, Knot, PowerDNS).
type rfc2136Provider struct {
	server   string
	tsigName string // fully qualified key name
	tsigAlgo string // fully qualified algorithm name
	tsigKey  string // base64 secret
	zone     string // discovered per call via SOA when empty
}

var tsigAlgorithms = map[string]string{
	"hmac-sha1":   dns.HmacSHA1,
	"hmac-sha224": dns.HmacSHA224,
	"hmac-sha256": dns.HmacSHA256,
	"hmac-sha384": dns.HmacSHA384,
	"hmac-sha512": dns.HmacSHA512,
}

func newRFC2136Provider(cfg config.ACMEConfig) (Provider, error) {
	algo := cfg.TSIGAlgorithm
	if algo == "" {
		algo = "hmac-sha256"
	}
	fqAlgo, ok := tsigAlgorithms[strings.ToLower(strings.TrimSuffix(algo, "."))]
	if !ok {
		return nil, fmt.Errorf("rfc2136: unsupported tsig_algorithm %q (hmac-sha1/224/256/384/512)", algo)
	}
	return &rfc2136Provider{
		server:   cfg.Server,
		tsigName: dns.Fqdn(cfg.TSIGName),
		tsigAlgo: fqAlgo,
		tsigKey:  cfg.TSIGSecret,
	}, nil
}

// findZone asks the target server which zone owns fqdn by walking parent
// names until a SOA answers authoritatively.
func (p *rfc2136Provider) findZone(ctx context.Context, fqdn string) (string, error) {
	client := &dns.Client{Timeout: 10 * time.Second}
	name := dns.Fqdn(fqdn)
	for name != "." {
		msg := new(dns.Msg)
		msg.SetQuestion(name, dns.TypeSOA)
		resp, _, err := client.ExchangeContext(ctx, msg, p.server)
		if err != nil {
			return "", fmt.Errorf("rfc2136: soa query: %w", err)
		}
		for _, rr := range resp.Answer {
			if soa, ok := rr.(*dns.SOA); ok {
				return soa.Hdr.Name, nil
			}
		}
		i := strings.IndexByte(name, '.')
		if i < 0 || i == len(name)-1 {
			break
		}
		name = name[i+1:]
	}
	return "", fmt.Errorf("rfc2136: no zone found for %s on %s", fqdn, p.server)
}

func (p *rfc2136Provider) sendFor(ctx context.Context, fqdn string, build func(*dns.Msg, string)) error {
	zone := p.zone
	if zone == "" {
		var err error
		if zone, err = p.findZone(ctx, fqdn); err != nil {
			return err
		}
	}
	msg := new(dns.Msg)
	msg.SetUpdate(zone)
	build(msg, zone)
	msg.SetTsig(p.tsigName, p.tsigAlgo, 300, time.Now().Unix())

	client := &dns.Client{
		Net:        "tcp", // updates can exceed UDP sizes; TCP is simplest
		Timeout:    10 * time.Second,
		TsigSecret: map[string]string{p.tsigName: p.tsigKey},
	}
	resp, _, err := client.ExchangeContext(ctx, msg, p.server)
	if err != nil {
		return fmt.Errorf("rfc2136: update: %w", err)
	}
	if resp.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("rfc2136: update refused: %s", dns.RcodeToString[resp.Rcode])
	}
	return nil
}

func (p *rfc2136Provider) txtRR(fqdn, txt string) *dns.TXT {
	return &dns.TXT{
		Hdr: dns.RR_Header{
			Name: dns.Fqdn(fqdn), Rrtype: dns.TypeTXT,
			Class: dns.ClassINET, Ttl: 60,
		},
		Txt: []string{txt},
	}
}

func (p *rfc2136Provider) Present(ctx context.Context, fqdn, txt string) error {
	return p.sendFor(ctx, fqdn, func(msg *dns.Msg, _ string) {
		msg.Insert([]dns.RR{p.txtRR(fqdn, txt)})
	})
}

func (p *rfc2136Provider) Cleanup(ctx context.Context, fqdn, txt string) error {
	return p.sendFor(ctx, fqdn, func(msg *dns.Msg, _ string) {
		msg.Remove([]dns.RR{p.txtRR(fqdn, txt)})
	})
}
