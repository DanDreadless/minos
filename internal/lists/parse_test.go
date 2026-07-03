package lists

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/filter"
)

func parseAll(t *testing.T, format, input string) (*filter.Matcher, Stats) {
	t.Helper()
	b := filter.NewBuilder()
	stats, err := Parse(format, "test", strings.NewReader(input), b)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	return b.Build(), stats
}

func TestParseHosts(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		blocked []string
		allowed []string
		rules   int
		skipped int
	}{
		{
			name: "standard entries",
			input: "# StevenBlack style header\n" +
				"127.0.0.1 localhost\n" +
				"0.0.0.0 ads.example.com\n" +
				"0.0.0.0 tracker.example.net # trailing comment\n",
			blocked: []string{"ads.example.com", "tracker.example.net", "sub.ads.example.com"},
			rules:   2,
		},
		{
			name:    "multiple hosts per line",
			input:   "0.0.0.0 a.example b.example c.example\n",
			blocked: []string{"a.example", "b.example", "c.example"},
			rules:   3,
		},
		{
			name:    "ipv6 zero entries",
			input:   ":: v6blocked.example\n::1 localhost\n",
			blocked: []string{"v6blocked.example"},
			rules:   1,
		},
		{
			name:    "real mappings are not block entries",
			input:   "192.168.1.5 nas.lan\n10.0.0.1 router.lan\n",
			allowed: []string{"nas.lan", "router.lan"},
			skipped: 2,
		},
		{
			name:    "localhost boilerplate ignored",
			input:   "127.0.0.1 localhost localhost.localdomain\n::1 ip6-localhost ip6-loopback\n",
			allowed: []string{"localhost"},
		},
		{
			name:    "invalid domains skipped",
			input:   "0.0.0.0 valid.example\n0.0.0.0 bad domain!\n0.0.0.0 ...\n",
			blocked: []string{"valid.example"},
			rules:   2, // "bad" and "domain!" split: "bad" is a valid single label
			skipped: 2,
		},
		{
			name:    "junk bytes",
			input:   "\x00\x01\x02 garbage\n0.0.0.0 good.example\n\xff\xfe\n",
			blocked: []string{"good.example"},
			rules:   1,
			skipped: 2,
		},
		{
			name:    "unicode domains rejected",
			input:   "0.0.0.0 bücher.example\n0.0.0.0 xn--bcher-kva.example\n",
			blocked: []string{"xn--bcher-kva.example"},
			rules:   1,
			skipped: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, stats := parseAll(t, "hosts", tt.input)
			for _, d := range tt.blocked {
				if r := m.Match(d); !r.Blocked {
					t.Errorf("expected %q blocked", d)
				}
			}
			for _, d := range tt.allowed {
				if r := m.Match(d); r.Blocked {
					t.Errorf("expected %q not blocked", d)
				}
			}
			if tt.rules != 0 && stats.Rules != tt.rules {
				t.Errorf("rules = %d, want %d", stats.Rules, tt.rules)
			}
			if stats.Skipped != tt.skipped {
				t.Errorf("skipped = %d, want %d", stats.Skipped, tt.skipped)
			}
		})
	}
}

func TestParsePlain(t *testing.T) {
	input := "# comment\nads.example.com\n\ntracker.example.net # inline\nnot a domain\n"
	m, stats := parseAll(t, "plain", input)
	if r := m.Match("ads.example.com"); !r.Blocked {
		t.Error("ads.example.com should be blocked")
	}
	if r := m.Match("tracker.example.net"); !r.Blocked {
		t.Error("tracker.example.net should be blocked")
	}
	if stats.Rules != 2 || stats.Skipped != 1 {
		t.Errorf("stats = %+v, want 2 rules 1 skipped", stats)
	}
}

func TestParseAdblock(t *testing.T) {
	input := strings.Join([]string{
		"[Adblock Plus 2.0]",
		"! comment",
		"||ads.example.com^",
		"@@||good.example.com^",
		"||tracker.example.net^$third-party", // unsupported option
		"##.banner",                          // element hiding: unsupported
		"bare-domain.example.org",
	}, "\n")
	m, stats := parseAll(t, "adblock", input)
	if r := m.Match("ads.example.com"); !r.Blocked {
		t.Error("ads.example.com should be blocked")
	}
	if r := m.Match("bare-domain.example.org"); !r.Blocked {
		t.Error("bare-domain.example.org should be blocked")
	}
	if r := m.Match("sub.good.example.com"); r.Blocked {
		t.Error("pardoned domain should not be blocked")
	}
	if stats.Rules != 3 || stats.Skipped != 2 {
		t.Errorf("stats = %+v, want 3 rules 2 skipped", stats)
	}
}

func TestParseHugeLine(t *testing.T) {
	// A 10 MB single line must not balloon memory or kill the parse.
	huge := strings.Repeat("a", 10<<20)
	input := "0.0.0.0 before.example\n" + huge + "\n0.0.0.0 after.example\n"
	m, stats := parseAll(t, "hosts", input)
	if r := m.Match("before.example"); !r.Blocked {
		t.Error("entry before huge line lost")
	}
	if r := m.Match("after.example"); !r.Blocked {
		t.Error("entry after huge line lost")
	}
	if stats.Skipped != 1 {
		t.Errorf("skipped = %d, want 1 (the huge line)", stats.Skipped)
	}
}

func TestParseEmptyAndNoTrailingNewline(t *testing.T) {
	m, stats := parseAll(t, "plain", "only.example.com")
	if r := m.Match("only.example.com"); !r.Blocked {
		t.Error("entry without trailing newline lost")
	}
	if stats.Rules != 1 {
		t.Errorf("rules = %d, want 1", stats.Rules)
	}
	_, stats = parseAll(t, "plain", "")
	if stats.Rules != 0 || stats.Skipped != 0 {
		t.Errorf("empty input produced stats %+v", stats)
	}
}

func newTestStore(t *testing.T, mutate func(*config.Config)) *config.Store {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	store, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		if err := store.Update(func(c *config.Config) error { mutate(c); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestManagerRefreshAndRebuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0.0.0.0 fetched.example.com\n"))
	}))
	defer srv.Close()

	engine := filter.NewEngine()
	store := newTestStore(t, func(c *config.Config) {
		c.Lists.Sources = []config.ListSource{
			{Name: "test", URL: srv.URL, Format: "hosts", Enabled: true},
		}
		c.Blocking.Denylist = []string{"custom-sentence.example"}
		c.Blocking.Allowlist = []string{"fetched.example.com"} // pardon beats list
	})
	mgr := NewManager(engine, store)
	mgr.Refresh(t.Context())

	if r := engine.Match("custom-sentence.example"); !r.Blocked || r.List != "denylist" {
		t.Errorf("config denylist entry not enforced: %+v", r)
	}
	if r := engine.Match("fetched.example.com"); r.Blocked {
		t.Errorf("pardon did not beat fetched list: %+v", r)
	}
	if r := engine.Match("sub.fetched.example.com"); r.Blocked {
		t.Errorf("pardon should cover subdomains: %+v", r)
	}

	status := mgr.Status()
	if len(status) != 1 || status[0].Rules != 1 || status[0].LastError != "" {
		t.Errorf("status = %+v, want 1 rule, no error", status)
	}
	if status[0].LastRefresh.IsZero() || time.Since(status[0].LastRefresh) > time.Minute {
		t.Errorf("LastRefresh not set: %+v", status[0])
	}
}

func TestManagerKeepsCacheOnFetchFailure(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("0.0.0.0 cached.example.com\n"))
	}))
	defer srv.Close()

	engine := filter.NewEngine()
	store := newTestStore(t, func(c *config.Config) {
		c.Lists.Sources = []config.ListSource{
			{Name: "flaky", URL: srv.URL, Format: "hosts", Enabled: true},
		}
	})
	mgr := NewManager(engine, store)
	mgr.Refresh(t.Context())
	if r := engine.Match("cached.example.com"); !r.Blocked {
		t.Fatal("initial fetch should block cached.example.com")
	}

	healthy = false
	mgr.Refresh(t.Context())
	if r := engine.Match("cached.example.com"); !r.Blocked {
		t.Error("fetch failure must keep last good list data")
	}
	status := mgr.Status()
	if len(status) != 1 || status[0].LastError == "" {
		t.Errorf("status should record the fetch error: %+v", status)
	}
}

func TestFetchSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream more than the cap without a Content-Length lie mattering.
		chunk := strings.Repeat("0.0.0.0 x.example\n", 1<<16) // ~1.1 MB
		for i := 0; i < 70; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	engine := filter.NewEngine()
	store := newTestStore(t, func(c *config.Config) {
		c.Lists.Sources = []config.ListSource{
			{Name: "huge", URL: srv.URL, Format: "hosts", Enabled: true},
		}
	})
	mgr := NewManager(engine, store)
	mgr.Refresh(t.Context())

	status := mgr.Status()
	if len(status) != 1 || !strings.Contains(status[0].LastError, "cap") {
		t.Errorf("oversized list should be rejected, status = %+v", status)
	}
}
