package dnsproxy

import (
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/querylog"
)

// Integration tests query from 127.0.0.1, so policies are assigned to it.

func TestClientBlockedGetsRefused(t *testing.T) {
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Clients = []config.Client{{IP: "127.0.0.1", Blocked: true}}
	})
	resp := query(t, srv.UDPAddr().String(), "example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeRefused {
		t.Errorf("rcode = %s, want REFUSED", dns.RcodeToString[resp.Rcode])
	}
	waitEntries(t, qlog, 1)
	e := qlog.Recent(1)[0]
	if e.Verdict != "blocked" || e.List != "clients" {
		t.Errorf("entry = %+v, want blocked by clients", e)
	}
}

func TestBypassGroupSkipsRules(t *testing.T) {
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Groups = []config.Group{{Name: "trusted", Mode: "bypass"}}
		c.Clients = []config.Client{{IP: "127.0.0.1", Group: "trusted"}}
	}, "ads.example.com") // globally denied…
	resp := query(t, srv.UDPAddr().String(), "ads.example.com", dns.TypeA)
	// …but a bypass client gets the real upstream answer.
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 (forwarded)", len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || a.A.String() == "0.0.0.0" {
		t.Errorf("answer = %v, want real upstream A record", resp.Answer[0])
	}
	waitEntries(t, qlog, 1)
	e := qlog.Recent(1)[0]
	if e.Verdict != "allowed" || e.List != "group:trusted" || e.Rule != "bypass" {
		t.Errorf("entry = %+v, want allowed via group:trusted bypass", e)
	}
}

func TestGroupOverlayDenyAndAllow(t *testing.T) {
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Groups = []config.Group{{
			Name: "kids", Mode: "filter",
			Denylist:  []string{"videos.example.com"},
			Allowlist: []string{"ads.example.com"}, // group pardon beats global deny
		}}
		c.Clients = []config.Client{{IP: "127.0.0.1", Group: "kids"}}
	}, "ads.example.com")
	addr := srv.UDPAddr().String()

	// Overlay deny: blocked even though no global rule matches.
	resp := query(t, addr, "videos.example.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("overlay deny: answers = %d, want 1 (0.0.0.0)", len(resp.Answer))
	}
	if a := resp.Answer[0].(*dns.A); a.A.String() != "0.0.0.0" {
		t.Errorf("overlay deny: answer = %s, want 0.0.0.0", a.A)
	}

	// Overlay allow: forwarded even though globally denied.
	resp = query(t, addr, "ads.example.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("overlay allow: answers = %d, want 1 (forwarded)", len(resp.Answer))
	}
	if a := resp.Answer[0].(*dns.A); a.A.String() == "0.0.0.0" {
		t.Error("overlay allow: got sinkhole answer, want real upstream record")
	}

	// Unrelated names still hit the global rules path (forwarded fine).
	resp = query(t, addr, "plain.example.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Errorf("global path: answers = %d, want 1", len(resp.Answer))
	}
}

// waitEntries polls until the async query log writer has consumed n entries.
func waitEntries(t *testing.T, qlog *querylog.Log, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(qlog.Recent(0)) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("query log did not receive %d entries in time", n)
}
