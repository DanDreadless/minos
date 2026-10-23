package dnsproxy

import (
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

func exchangeEDNS(t *testing.T, addr, qname string, qtype uint16, edns bool) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), qtype)
	if edns {
		m.SetEdns0(1232, false)
	}
	c := &dns.Client{Timeout: 5 * time.Second}
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("query %s: %v", qname, err)
	}
	return resp
}

func ede(m *dns.Msg) *dns.EDNS0_EDE {
	opt := m.IsEdns0()
	if opt == nil {
		return nil
	}
	for _, o := range opt.Option {
		if e, ok := o.(*dns.EDNS0_EDE); ok {
			return e
		}
	}
	return nil
}

func TestEDEOnBlockedAnswer(t *testing.T) {
	srv, _ := startProxy(t, "zero_ip", "ads.example.com")
	addr := srv.UDPAddr().String()

	resp := exchangeEDNS(t, addr, "tracker.ads.example.com", dns.TypeA, true)
	e := ede(resp)
	if e == nil || e.InfoCode != dns.ExtendedErrorCodeBlocked || !strings.Contains(e.ExtraText, "testlist") {
		t.Fatalf("EDE = %+v, want Blocked naming the list", e)
	}
	if len(resp.Answer) != 1 {
		t.Errorf("the 0.0.0.0 answer is still there: %v", resp.Answer)
	}

	// A client that sent no EDNS0 gets none back — it could not parse it.
	if resp := exchangeEDNS(t, addr, "tracker.ads.example.com", dns.TypeA, false); resp.IsEdns0() != nil {
		t.Errorf("an OPT record was added for a client without EDNS0: %v", resp.Extra)
	}
	// Allowed answers carry no error.
	if e := ede(exchangeEDNS(t, addr, "fine.example.org", dns.TypeA, true)); e != nil {
		t.Errorf("an allowed answer carried EDE %+v", e)
	}
}

func TestEDEOnRefusedDevice(t *testing.T) {
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Clients = []config.Client{{IP: "127.0.0.1", Blocked: true}}
	})
	resp := exchangeEDNS(t, srv.UDPAddr().String(), "example.org", dns.TypeA, true)
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("rcode = %s", dns.RcodeToString[resp.Rcode])
	}
	if e := ede(resp); e == nil || e.InfoCode != dns.ExtendedErrorCodeProhibited {
		t.Errorf("EDE = %+v, want Prohibited", e)
	}
}

func TestEDEOnUnreachableUpstream(t *testing.T) {
	dead := deadUpstreamAddr(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: dead, Protocol: "tcp"}}
	})
	resp := exchangeEDNS(t, srv.UDPAddr().String(), "example.org", dns.TypeA, true)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("rcode = %s", dns.RcodeToString[resp.Rcode])
	}
	if e := ede(resp); e == nil || e.InfoCode != dns.ExtendedErrorCodeNetworkError {
		t.Errorf("EDE = %+v, want Network Error", e)
	}
}
