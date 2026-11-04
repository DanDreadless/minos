package filter

import "testing"

func TestWildcardRules(t *testing.T) {
	b := NewBuilder()
	b.AddDeny("denylist", "*.Tracker.example")
	b.AddDeny("denylist", "ads*.shop.example")
	b.AddDeny("denylist", "exact.example")
	b.AddAllow("allowlist", "*.good.exact.example")
	m := b.Build()

	cases := []struct {
		name    string
		blocked bool
		rule    string
	}{
		{"a.tracker.example", true, "*.tracker.example"},
		{"a.b.tracker.example", true, "*.tracker.example"}, // any depth
		{"tracker.example", false, ""},                     // not the bare parent
		{"nottracker.example", false, ""},
		{"ads.shop.example", true, "ads*.shop.example"},
		{"ads12.shop.example", true, "ads*.shop.example"},
		{"shop.example", false, ""},
		{"x.exact.example", true, "exact.example"},              // plain domain: subdomains too
		{"a.good.exact.example", false, "*.good.exact.example"}, // allow wildcard beats exact block
		{"good.exact.example", true, "exact.example"},
	}
	for _, c := range cases {
		res := m.Match(c.name)
		if res.Blocked != c.blocked || res.Rule != c.rule {
			t.Errorf("%s: blocked=%v rule=%q, want %v %q", c.name, res.Blocked, res.Rule, c.blocked, c.rule)
		}
	}
	if m.Skipped() != 0 {
		t.Errorf("skipped %d", m.Skipped())
	}
}

func TestNormalizeWildcard(t *testing.T) {
	for in, want := range map[string]string{
		"*.Example.COM.":  "*.example.com",
		"**.example.com":  "*.example.com",
		"ads*.example":    "ads*.example",
		"*":               "",
		"*.*":             "",
		".*.example.com":  "",
		"*..example.com":  "",
		"*.exa mple.com":  "",
		"example.com":     "",
		"/^ads*\\.x$/":    "",
		"*.example.com/x": "",
	} {
		if got := NormalizeWildcard(in); got != want {
			t.Errorf("NormalizeWildcard(%q) = %q, want %q", in, got, want)
		}
	}
}

// Lists never carry wildcards into the matcher: their parsers drop
// anything that is not a plain domain first, and AdBlock lines with "*"
// are skipped.
func TestAdblockWildcardStillSkipped(t *testing.T) {
	b := NewBuilder()
	b.ParseAdblockLine("list", "||*.example.com^", false)
	if m := b.Build(); m.Rules() != 0 || m.Skipped() != 1 {
		t.Errorf("rules=%d skipped=%d, want 0 and 1", m.Rules(), m.Skipped())
	}
}
