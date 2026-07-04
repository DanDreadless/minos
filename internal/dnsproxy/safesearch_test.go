package dnsproxy

import (
	"net"
	"testing"

	"github.com/miekg/dns"

	"minos/internal/config"
)

func TestSafeSearchGlobalRewrite(t *testing.T) {
	upstream, count := startCountingUpstream(t)
	srv, qlog := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
		c.Blocking.SafeSearch = true
	})
	addr := srv.UDPAddr().String()

	resp := query(t, addr, "www.google.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 2 {
		t.Fatalf("rcode=%s answers=%d, want NOERROR with CNAME + A",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	cname, ok := resp.Answer[0].(*dns.CNAME)
	if !ok || cname.Target != "forcesafesearch.google.com." {
		t.Errorf("first answer = %v, want CNAME to forcesafesearch.google.com.", resp.Answer[0])
	}
	if cname.Hdr.Name != "www.google.com." {
		t.Errorf("CNAME owner = %s, want the queried name", cname.Hdr.Name)
	}
	a, ok := resp.Answer[1].(*dns.A)
	if !ok || !a.A.Equal(net.IPv4(93, 184, 216, 34)) {
		t.Errorf("second answer = %v, want the stub's A record", resp.Answer[1])
	}
	if a.Hdr.Name != "forcesafesearch.google.com." {
		t.Errorf("A owner = %s, want the safe host", a.Hdr.Name)
	}

	// A second query serves the safe host from cache: still one upstream hit.
	query(t, addr, "google.com", dns.TypeA)
	if got := count.Load(); got != 1 {
		t.Errorf("upstream served %d, want 1 (safe host cached under its own key)", got)
	}

	// The docket attributes the rewrite.
	waitFor(t, func() bool {
		recent := qlog.Recent(1)
		return len(recent) == 1 && recent[0].List == "safesearch" &&
			recent[0].Rule == "forcesafesearch.google.com"
	}, "safesearch docket entry")

	// Unrelated names are untouched (and Google subdomains are NOT rewritten).
	mail := query(t, addr, "mail.google.com", dns.TypeA)
	if len(mail.Answer) != 1 {
		t.Fatalf("mail.google.com answers = %d, want 1 plain answer", len(mail.Answer))
	}
	if _, isCNAME := mail.Answer[0].(*dns.CNAME); isCNAME {
		t.Error("mail.google.com was rewritten; only exact search names may be")
	}
}

func TestSafeSearchBlanksHTTPSQueries(t *testing.T) {
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Blocking.SafeSearch = true
	})
	resp := query(t, srv.UDPAddr().String(), "www.google.com", dns.TypeHTTPS)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 {
		t.Errorf("HTTPS query: rcode=%s answers=%d, want empty NOERROR",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
}

func TestSafeSearchPerGroup(t *testing.T) {
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Groups = []config.Group{
			{Name: "kids", Mode: "filter", SafeSearch: true},
			{Name: "adults", Mode: "bypass"},
		}
		// Tests query from 127.0.0.1, so assign it to the kids group.
		c.Clients = []config.Client{{IP: "127.0.0.1", Group: "kids"}}
	})
	resp := query(t, srv.UDPAddr().String(), "duckduckgo.com", dns.TypeA)
	if len(resp.Answer) < 1 {
		t.Fatal("no answers")
	}
	cname, ok := resp.Answer[0].(*dns.CNAME)
	if !ok || cname.Target != "safe.duckduckgo.com." {
		t.Errorf("first answer = %v, want CNAME to safe.duckduckgo.com.", resp.Answer[0])
	}
}

func TestSafeSearchExemptions(t *testing.T) {
	// Global safe search on, but the client is in a bypass group: no rewrite.
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.Blocking.SafeSearch = true
		c.Groups = []config.Group{{Name: "adults", Mode: "bypass"}}
		c.Clients = []config.Client{{IP: "127.0.0.1", Group: "adults"}}
	})
	resp := query(t, srv.UDPAddr().String(), "www.bing.com", dns.TypeA)
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 plain answer", len(resp.Answer))
	}
	if _, isCNAME := resp.Answer[0].(*dns.CNAME); isCNAME {
		t.Error("bypass client got a safe-search rewrite")
	}
}
