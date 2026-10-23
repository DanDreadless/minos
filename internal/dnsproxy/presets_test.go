//go:build presets

// The resolver presets in web/src/lib/resolvers.ts are one-click choices, so a
// preset that cannot answer is a broken upstream for whoever picks it — and
// the breaker light used to hide that. This exchanges a real query with every
// preset through the production transport for its protocol — DoH, DoH3 or
// DoQ (never curl, which negotiates differently: that is how both Quad9
// presets shipped unusable).
//
//	go test -tags presets -v ./internal/dnsproxy -run TestResolverPresetsAnswer
//
// Network-dependent, so it stays behind a build tag; `go test ./...` never
// touches the internet.
package dnsproxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

var presetRe = regexp.MustCompile(`id:\s*'([^']*)'.*?address:\s*'([^']*)',\s*protocol:\s*'([^']*)'`)

func TestResolverPresetsAnswer(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "resolvers.ts"))
	if err != nil {
		t.Fatalf("read presets: %v", err)
	}
	matches := presetRe.FindAllStringSubmatch(string(src), -1)
	if want := strings.Count(string(src), "{ id: '"); len(matches) == 0 || len(matches) != want {
		t.Fatalf("parsed %d presets, file declares %d — regex and file have drifted", len(matches), want)
	}
	for _, m := range matches {
		id, addr, proto := m[1], m[2], m[3]
		t.Run(id, func(t *testing.T) {
			// IP literals by policy: a resolver must not need a resolver to
			// find it. Encrypted protocols only.
			switch proto {
			case "doh", "doh3", "doq":
			default:
				t.Fatalf("preset %s is %s; presets are encrypted (doh, doh3, doq) by policy", id, proto)
			}
			if !ipLiteralPreset(addr) {
				t.Fatalf("preset %s (%s) must address its resolver by IP", id, addr)
			}
			// A preset fails only if it fails every attempt: one timeout from a
			// CI runner's network is noise, and an alert that fires on noise
			// stops being read. (First run on GitHub: Quad9 timed out once
			// while its sibling 9.9.9.10 answered.)
			var lastErr error
			for attempt := 1; attempt <= presetAttempts; attempt++ {
				lastErr = presetAnswers(addr, proto)
				if lastErr == nil {
					t.Logf("%-20s %s ok (attempt %d)", id, addr, attempt)
					return
				}
				t.Logf("%s attempt %d: %v", id, attempt, lastErr)
				time.Sleep(2 * time.Second)
			}
			t.Fatalf("%s (%s) failed %d attempts in a row: %v", id, addr, presetAttempts, lastErr)
		})
	}
}

const presetAttempts = 3

// ipLiteralPreset reports an address whose host is an IP literal.
func ipLiteralPreset(addr string) bool {
	host := addr
	if strings.HasPrefix(addr, "https://") {
		host = strings.SplitN(strings.TrimPrefix(addr, "https://"), "/", 2)[0]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(strings.Trim(host, "[]")) != nil
}

// presetAnswers exchanges one query through a fresh production client for
// the preset's protocol.
func presetAnswers(addr, proto string) error {
	up, err := NewUpstream(config.Upstream{Address: addr, Protocol: proto})
	if err != nil {
		return err
	}
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := up.Exchange(ctx, q)
	if err != nil {
		return err
	}
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		return fmt.Errorf("rcode %s, %d answers", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	return nil
}
