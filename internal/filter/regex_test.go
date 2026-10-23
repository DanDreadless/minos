package filter

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

func TestRegexRulePrecedence(t *testing.T) {
	b := NewBuilder()
	b.AddDeny("blocklist", "ads.example.com")
	b.AddDeny("regex", `/^ad[0-9]+\./`)
	b.AddDeny("regex", `/(^|\.)tracking\./`)
	b.AddAllow("allowlist", "ok.tracking.example")
	b.AddAllow("allow-regex", `/^good-ads\./`)
	b.AddAllow("allow-regex", `/\.ads\.example\.com$/`)
	m := b.Build()

	for _, c := range []struct {
		name    string
		blocked bool
		rule    string
	}{
		{"ad12.example.net", true, `/^ad[0-9]+\./`},
		{"x.tracking.example.org", true, `/(^|\.)tracking\./`},
		{"ok.tracking.example", false, "ok.tracking.example"},    // exact allow beats a block regex
		{"ads.example.com", true, "ads.example.com"},             // exact block, no allow regex matches
		{"cdn.ads.example.com", false, `/\.ads\.example\.com$/`}, // an allow regex beats an exact block
		{"good-ads.example", false, `/^good-ads\./`},
		{"nothing.example", false, ""},
		{"AD7.EXAMPLE.NET", true, `/^ad[0-9]+\./`}, // case-insensitive (names arrive lowercased anyway)
	} {
		r := m.Match(strings.ToLower(c.name))
		if r.Blocked != c.blocked || r.Rule != c.rule {
			t.Errorf("%s: got %+v, want blocked=%v rule=%q", c.name, r, c.blocked, c.rule)
		}
	}
	if m.Rules() != 3 || m.AllowRules() != 3 {
		t.Errorf("rule counts %d/%d, want 3/3", m.Rules(), m.AllowRules())
	}
}

func TestUnsupportedRegexIsSkipped(t *testing.T) {
	b := NewBuilder()
	b.AddDeny("x", `/(a)\1/`)               // backreference: not RE2
	b.AddDeny("x", `/^ads;querytype=AAAA/`) // Pi-hole option
	b.AddDeny("x", `/(?=ads)/`)             // lookahead
	b.AddDeny("x", "/"+strings.Repeat("a", 600)+"/")
	b.AddDeny("x", `/^ok\./`)
	m := b.Build()
	if m.Rules() != 1 || m.Skipped() != 4 {
		t.Errorf("rules %d skipped %d, want 1 and 4", m.Rules(), m.Skipped())
	}
}

func TestAdblockRegexLines(t *testing.T) {
	b := NewBuilder()
	b.ParseAdblockLine("list", `/^banner[0-9]*\./`, false)
	b.ParseAdblockLine("list", `@@/^banner-ok\./`, false)
	b.ParseAdblockLine("list", `/regex/$important`, false) // options: still skipped
	m := b.Build()
	if !m.Match("banner3.example").Blocked || m.Match("banner-ok.example").Blocked {
		t.Error("adblock regex lines were not applied")
	}
	if m.Skipped() != 1 {
		t.Errorf("skipped = %d, want 1", m.Skipped())
	}
}

func TestRegexRuleCap(t *testing.T) {
	b := NewBuilder()
	for i := 0; i < MaxRegexRules+5; i++ {
		b.AddDeny("x", fmt.Sprintf(`/^r%d\./`, i))
	}
	if m := b.Build(); m.Rules() != MaxRegexRules || m.Skipped() != 5 {
		t.Errorf("rules %d skipped %d", m.Rules(), m.Skipped())
	}
}

// The prefilter may only ever skip rules that would not have matched.
func TestNeedlesNeverHideAMatch(t *testing.T) {
	patterns := []string{
		`^ad[sz]?[0-9]*\.`, `\.track(ing)?\.`, `(^|\.)metrics\.`, `^(www|ads)\.example`,
		`telemetry`, `[a-z]+-ads\.`, `^.*$`, `x(ab|cd)+y`, `(foo|bar)baz`, `ab{2,3}c`,
		`^[0-9]+\.ads$`, `pixel\d*`, `(?:beacon|collect)\.`, `^a`, `(us|eu)-west`,
	}
	rng := rand.New(rand.NewSource(1))
	alphabet := "abcdefghiklmnoprstuwxyz0123456789.-"
	words := []string{"ads", "ad", "track", "tracking", "metrics", "www", "example", "telemetry",
		"pixel", "beacon", "collect", "foo", "bar", "baz", "abbc", "xaby", "us-west", "eu"}
	for _, p := range patterns {
		re := regexp.MustCompile("(?i)" + p)
		needles := needlesFor(p)
		for i := 0; i < 3000; i++ {
			var sb strings.Builder
			for j := 0; j < 1+rng.Intn(5); j++ {
				if j > 0 {
					sb.WriteByte('.')
				}
				if rng.Intn(2) == 0 {
					sb.WriteString(words[rng.Intn(len(words))])
				}
				for k := 0; k < rng.Intn(4); k++ {
					sb.WriteByte(alphabet[rng.Intn(len(alphabet))])
				}
			}
			name := sb.String()
			if re.MatchString(name) && needles != nil && !containsAny(name, needles) {
				t.Fatalf("pattern %q: prefilter %q would hide the match on %q", p, needles, name)
			}
		}
	}
	if needlesFor(`^.*$`) != nil || needlesFor(`[a-z]+`) != nil {
		t.Error("a pattern with no required text must have no prefilter")
	}
	if n := needlesFor(`\.track(ing)?\.`); len(n) == 0 {
		t.Error("a pattern with required text should get a prefilter")
	}
}
