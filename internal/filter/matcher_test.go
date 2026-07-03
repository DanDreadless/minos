package filter

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"example.com", "example.com"},
		{"Example.COM", "example.com"},
		{"example.com.", "example.com"},
		{"sub.example.com", "sub.example.com"},
		{"xn--bcher-kva.example", "xn--bcher-kva.example"},
		{"under_score.example.com", "under_score.example.com"},
		{"digits123.example", "digits123.example"},
		{"", ""},
		{".", ""},
		{"..", ""},
		{".example.com", ""},
		{"exa mple.com", ""},
		{"bücher.example", ""},
		{"exam\x00ple.com", ""},
		{strings.Repeat("a", 64) + ".com", ""}, // label too long
		{strings.Repeat("a.", 127) + "toolongoverall", ""}, // name too long
		{"a..b", ""},
	}
	for _, tt := range tests {
		if got := NormalizeDomain(tt.in); got != tt.want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReverseLabels(t *testing.T) {
	tests := []struct{ in, want string }{
		{"com", "com."},
		{"example.com", "com.example."},
		{"ads.doubleclick.com", "com.doubleclick.ads."},
	}
	for _, tt := range tests {
		if got := reverseLabels(tt.in); got != tt.want {
			t.Errorf("reverseLabels(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if back := unreverseLabels(tt.want); back != tt.in {
			t.Errorf("unreverseLabels(%q) = %q, want %q", tt.want, back, tt.in)
		}
	}
}

func TestMatcherSubdomains(t *testing.T) {
	b := NewBuilder()
	b.AddDeny("ads", "doubleclick.com")
	b.AddDeny("tracking", "metrics.example.org")
	m := b.Build()

	tests := []struct {
		qname   string
		blocked bool
		rule    string
	}{
		{"doubleclick.com", true, "doubleclick.com"},
		{"ads.doubleclick.com", true, "doubleclick.com"},
		{"a.b.c.doubleclick.com", true, "doubleclick.com"},
		{"notdoubleclick.com", false, ""},
		{"doubleclick.com.evil.net", false, ""},
		{"example.org", false, ""},
		{"metrics.example.org", true, "metrics.example.org"},
		{"deep.metrics.example.org", true, "metrics.example.org"},
		{"com", false, ""},
	}
	for _, tt := range tests {
		got := m.Match(tt.qname)
		if got.Blocked != tt.blocked || got.Rule != tt.rule {
			t.Errorf("Match(%q) = %+v, want blocked=%v rule=%q", tt.qname, got, tt.blocked, tt.rule)
		}
	}
}

func TestMatcherAllowBeatsDeny(t *testing.T) {
	b := NewBuilder()
	b.AddDeny("ads", "example.com")
	b.AddAllow("allowlist", "good.example.com")
	m := b.Build()

	if r := m.Match("example.com"); !r.Blocked {
		t.Error("example.com should be blocked")
	}
	if r := m.Match("good.example.com"); r.Blocked {
		t.Errorf("good.example.com should be pardoned, got %+v", r)
	}
	if r := m.Match("sub.good.example.com"); r.Blocked {
		t.Errorf("subdomain of pardon should be allowed, got %+v", r)
	}
	if r := m.Match("bad.example.com"); !r.Blocked {
		t.Error("bad.example.com should still be blocked")
	}
}

func TestMatcherRecordsListAndRule(t *testing.T) {
	b := NewBuilder()
	b.AddDeny("first-list", "example.com")
	b.AddDeny("second-list", "example.com") // duplicate: first list wins
	m := b.Build()

	r := m.Match("www.example.com")
	if !r.Blocked || r.List != "first-list" || r.Rule != "example.com" {
		t.Errorf("want blocked by first-list/example.com, got %+v", r)
	}
}

func TestParseAdblockLine(t *testing.T) {
	tests := []struct {
		line    string
		blocked string // domain expected blocked, "" if none
		allowed string
		skipped bool
	}{
		{"||ads.example.com^", "ads.example.com", "", false},
		{"||ads.example.com", "ads.example.com", "", false},
		{"@@||good.example.com^", "", "good.example.com", false},
		{"plain-domain.example.com", "plain-domain.example.com", "", false},
		{"! a comment", "", "", false},
		{"[Adblock Plus 2.0]", "", "", false},
		{"", "", "", false},
		{"||example.com^$third-party", "", "", true},
		{"||example.com/path", "", "", true},
		{"##.ad-banner", "", "", true},
		{"/banner/*/img^", "", "", true},
		{"|http://example.com|", "", "", true},
		{"||*.example.com^", "", "", true},
	}
	for _, tt := range tests {
		b := NewBuilder()
		b.ParseAdblockLine("test", tt.line)
		m := b.Build()
		if tt.blocked != "" {
			if r := m.Match(tt.blocked); !r.Blocked {
				t.Errorf("line %q: expected %q blocked", tt.line, tt.blocked)
			}
		}
		if tt.allowed != "" {
			if m.AllowRules() != 1 {
				t.Errorf("line %q: expected one allow rule", tt.line)
			}
		}
		if tt.skipped != (m.Skipped() > 0) {
			t.Errorf("line %q: skipped=%d, want skipped=%v", tt.line, m.Skipped(), tt.skipped)
		}
	}
}

func TestEnginePause(t *testing.T) {
	e := NewEngine()
	b := NewBuilder()
	b.AddDeny("ads", "blocked.example")
	e.Swap(b.Build())

	if r := e.Match("blocked.example"); !r.Blocked {
		t.Fatal("expected block before pause")
	}
	e.Pause(time.Hour)
	if paused, until := e.Paused(); !paused || until.IsZero() {
		t.Error("expected timed pause")
	}
	if r := e.Match("blocked.example"); r.Blocked {
		t.Error("expected pass during recess")
	}
	e.Resume()
	if r := e.Match("blocked.example"); !r.Blocked {
		t.Error("expected block after resume")
	}
	e.Pause(0) // indefinite
	if paused, until := e.Paused(); !paused || !until.IsZero() {
		t.Error("expected indefinite pause")
	}
	e.Resume()
}

// buildMatcher compiles n synthetic domains for benchmarks.
func buildMatcher(n int) *Matcher {
	b := NewBuilder()
	for i := 0; i < n; i++ {
		b.AddDeny("bench", fmt.Sprintf("host%d.tracker%d.example.com", i, i%1000))
	}
	return b.Build()
}

func BenchmarkMatchHit(b *testing.B) {
	m := buildMatcher(1_000_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := m.Match("sub.host42.tracker42.example.com")
		if !r.Blocked {
			b.Fatal("expected hit")
		}
	}
}

func BenchmarkMatchMiss(b *testing.B) {
	m := buildMatcher(1_000_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := m.Match("www.perfectly-innocent.example.net"); r.Blocked {
			b.Fatal("unexpected hit")
		}
	}
}

func BenchmarkBuild100k(b *testing.B) {
	domains := make([]string, 100_000)
	for i := range domains {
		domains[i] = fmt.Sprintf("host%d.tracker%d.example.com", i, i%1000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bl := NewBuilder()
		for _, d := range domains {
			bl.AddDeny("bench", d)
		}
		if bl.Build().Rules() != len(domains) {
			b.Fatal("bad build")
		}
	}
}
