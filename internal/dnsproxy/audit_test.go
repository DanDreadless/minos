package dnsproxy

import (
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/filter"
)

// auditEngineWith compiles the given denies into a fresh audit engine.
func auditEngineWith(denied ...string) *filter.Engine {
	e := filter.NewEngine()
	b := filter.NewBuilder()
	for _, d := range denied {
		b.AddDeny("strictlist", d)
	}
	e.Swap(b.Build())
	return e
}

// TestAuditAttributesWithoutEnforcing: a query only an audit list matches is
// forwarded normally, and its docket entry carries the would-block marker.
func TestAuditAttributesWithoutEnforcing(t *testing.T) {
	srv, qlog := startProxy(t, "zero_ip")
	srv.SetAuditEngine(auditEngineWith("ads.example.com"))

	resp := query(t, srv.UDPAddr().String(), "tracker.ads.example.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("rcode=%s answers=%d, want the forwarded upstream answer",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || a.A.String() == "0.0.0.0" {
		t.Errorf("answer = %v, want the real upstream A record (never enforced)", resp.Answer[0])
	}
	waitEntries(t, qlog, 1)
	e := qlog.Recent(1)[0]
	if e.Verdict != "allowed" || e.AuditList != "strictlist" || e.AuditRule != "ads.example.com" {
		t.Errorf("entry = %+v, want allowed with strictlist would-block attribution", e)
	}
}

// TestAuditEnforcementWins: a query both an enforcing and an audit list match
// is simply blocked; auditing an already-blocked query is pointless.
func TestAuditEnforcementWins(t *testing.T) {
	srv, qlog := startProxy(t, "zero_ip", "ads.example.com")
	srv.SetAuditEngine(auditEngineWith("ads.example.com"))

	resp := query(t, srv.UDPAddr().String(), "ads.example.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want the blocked 0.0.0.0", len(resp.Answer))
	}
	if a, ok := resp.Answer[0].(*dns.A); !ok || a.A.String() != "0.0.0.0" {
		t.Errorf("answer = %v, want 0.0.0.0 (enforcement wins)", resp.Answer[0])
	}
	waitEntries(t, qlog, 1)
	e := qlog.Recent(1)[0]
	if e.Verdict != "blocked" || e.List != "testlist" || e.AuditList != "" {
		t.Errorf("entry = %+v, want blocked by testlist with no audit fields", e)
	}
}

// TestAuditWithPardon: a pardon that saves a name from an enforcing list can
// coexist with a would-block marker — both attributions land in the docket.
func TestAuditWithPardon(t *testing.T) {
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Blocking.Denylist = []string{"ads.example.com"}
		c.Blocking.Allowlist = []string{"good.ads.example.com"}
	})
	// The harness compiles denied... into the engine directly; build the
	// config-shaped rules the same way the manager would.
	b := filter.NewBuilder()
	b.AddDeny("denylist", "ads.example.com")
	b.AddAllow("allowlist", "good.ads.example.com")
	srv.engine.Swap(b.Build())
	srv.SetAuditEngine(auditEngineWith("ads.example.com"))

	resp := query(t, srv.UDPAddr().String(), "good.ads.example.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want the forwarded answer (pardon wins)", len(resp.Answer))
	}
	waitEntries(t, qlog, 1)
	e := qlog.Recent(1)[0]
	if e.Verdict != "allowed" || e.List != "allowlist" ||
		e.AuditList != "strictlist" || e.AuditRule != "ads.example.com" {
		t.Errorf("entry = %+v, want pardon attribution plus strictlist would-block", e)
	}
}

// TestAuditSkipsBypassDevices: a bypass device is exempt from all filtering,
// including audit attribution — "would block" for an unfiltered device would
// be noise.
func TestAuditSkipsBypassDevices(t *testing.T) {
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Groups = []config.Group{{Name: "trusted", Mode: "bypass"}}
		c.Clients = []config.Client{{IP: "127.0.0.1", Group: "trusted"}}
	})
	srv.SetAuditEngine(auditEngineWith("ads.example.com"))

	query(t, srv.UDPAddr().String(), "ads.example.com", dns.TypeA)
	waitEntries(t, qlog, 1)
	e := qlog.Recent(1)[0]
	if e.AuditList != "" || e.AuditRule != "" {
		t.Errorf("entry = %+v, want no audit attribution for a bypass device", e)
	}
}
