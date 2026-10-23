//go:build presets

// The resolver presets in web/src/lib/resolvers.ts are one-click choices, so a
// preset that cannot answer is a broken upstream for whoever picks it — and
// the breaker light used to hide that. This exchanges a real query with every
// preset through the production DoH transport (never curl, which negotiates
// differently: that is how both Quad9 presets shipped unusable).
//
//	go test -tags presets -v ./internal/dnsproxy -run TestResolverPresetsAnswer
//
// Network-dependent, so it stays behind a build tag; `go test ./...` never
// touches the internet.
package dnsproxy

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
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
			if proto != "doh" {
				t.Fatalf("preset %s is %s; presets are IP-literal DoH by policy", id, proto)
			}
			up := newDoHUpstream(addr, nil)
			q := new(dns.Msg)
			q.SetQuestion("example.com.", dns.TypeA)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			resp, err := up.Exchange(ctx, q)
			if err != nil {
				t.Fatalf("%s (%s): %v", id, addr, err)
			}
			if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
				t.Fatalf("%s (%s): rcode %s, %d answers", id, addr, dns.RcodeToString[resp.Rcode], len(resp.Answer))
			}
			t.Logf("%-20s %s ok", id, addr)
		})
	}
}
