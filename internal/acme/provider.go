package acme

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// Provider publishes and removes the DNS-01 challenge TXT record through
// one DNS host's API. Implementations must be idempotent: Present may be
// called for a record that already exists, Cleanup for one already gone.
type Provider interface {
	Present(ctx context.Context, fqdn, txt string) error
	Cleanup(ctx context.Context, fqdn, txt string) error
}

// newProvider maps validated config onto an implementation.
func newProvider(cfg config.ACMEConfig) (Provider, error) {
	switch cfg.Provider {
	case "cloudflare":
		return &cloudflareProvider{token: cfg.APIToken}, nil
	default:
		return nil, fmt.Errorf("unknown acme provider %q", cfg.Provider)
	}
}

// Propagation polling: public resolvers must see the TXT record before the
// CA is told to validate, or the order burns a failed attempt.
var (
	propagationResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}
	propagationTimeout   = 5 * time.Minute
	propagationInterval  = 10 * time.Second
)

// waitForTXT polls until any propagation resolver returns the expected
// value for fqdn, or the timeout/context expires.
func waitForTXT(ctx context.Context, fqdn, want string) error {
	deadline := time.Now().Add(propagationTimeout)
	client := &dns.Client{Timeout: 5 * time.Second}
	for {
		for _, resolver := range propagationResolvers {
			if txtVisible(ctx, client, resolver, fqdn, want) {
				slog.Info("acme: challenge record visible", "fqdn", fqdn, "resolver", resolver)
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("TXT record for %s not visible after %s", fqdn, propagationTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(propagationInterval):
		}
	}
}

func txtVisible(ctx context.Context, client *dns.Client, resolver, fqdn, want string) bool {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(fqdn), dns.TypeTXT)
	resp, _, err := client.ExchangeContext(ctx, msg, resolver)
	if err != nil {
		return false
	}
	for _, rr := range resp.Answer {
		if txt, ok := rr.(*dns.TXT); ok {
			for _, v := range txt.Txt {
				if v == want {
					return true
				}
			}
		}
	}
	return false
}
