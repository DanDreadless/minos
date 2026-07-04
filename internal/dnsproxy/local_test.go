package dnsproxy

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/querylog"
)

func startLocalProxy(t *testing.T) (*Server, *querylog.Log) {
	t.Helper()
	return startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.LocalTTL = 120
		c.DNS.LocalRecords = []config.LocalRecord{
			{Name: "nas.home.lab", A: []string{"192.168.1.10"}, AAAA: []string{"fd00::10"}},
			{Name: "*.home.lab", A: []string{"192.168.1.20"}},
			{Name: "media.home.lab", CNAME: "nas.home.lab"},
		}
	}, "nas.home.lab") // deliberately denylisted: local records must win
}

func TestLocalRecordAnswersA(t *testing.T) {
	srv, qlog := startLocalProxy(t)
	resp := query(t, srv.UDPAddr().String(), "nas.home.lab", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("rcode=%s answers=%d, want NOERROR with 1 answer",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok || !a.A.Equal(net.IPv4(192, 168, 1, 10)) {
		t.Errorf("answer = %v, want 192.168.1.10", resp.Answer[0])
	}
	if a.Hdr.Ttl != 120 {
		t.Errorf("ttl = %d, want local_ttl 120", a.Hdr.Ttl)
	}
	if !resp.Authoritative {
		t.Error("local answers must be authoritative")
	}

	// Local beats the blocklist: the same name is on the denylist, yet the
	// docket must attribute the answer to the local record.
	waitFor(t, func() bool {
		recent := qlog.Recent(1)
		return len(recent) == 1 && recent[0].List == "local" &&
			recent[0].Rule == "nas.home.lab" && recent[0].Upstream == "local"
	}, "local docket entry")
}

func TestLocalRecordAAAA(t *testing.T) {
	srv, _ := startLocalProxy(t)
	resp := query(t, srv.UDPAddr().String(), "nas.home.lab", dns.TypeAAAA)
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	if aaaa, ok := resp.Answer[0].(*dns.AAAA); !ok || !aaaa.AAAA.Equal(net.ParseIP("fd00::10")) {
		t.Errorf("answer = %v, want fd00::10", resp.Answer[0])
	}
}

func TestLocalWildcard(t *testing.T) {
	srv, _ := startLocalProxy(t)
	addr := srv.UDPAddr().String()

	for _, qname := range []string{"printer.home.lab", "deep.sub.home.lab"} {
		resp := query(t, addr, qname, dns.TypeA)
		if len(resp.Answer) != 1 {
			t.Fatalf("%s: answers = %d, want 1 via wildcard", qname, len(resp.Answer))
		}
		if a, ok := resp.Answer[0].(*dns.A); !ok || !a.A.Equal(net.IPv4(192, 168, 1, 20)) {
			t.Errorf("%s: answer = %v, want wildcard's 192.168.1.20", qname, resp.Answer[0])
		}
	}

	// The bare parent is NOT matched by the wildcard; it forwards upstream.
	resp := query(t, addr, "home.lab", dns.TypeA)
	if len(resp.Answer) == 1 {
		if a, ok := resp.Answer[0].(*dns.A); ok && a.A.Equal(net.IPv4(192, 168, 1, 20)) {
			t.Error("bare parent matched the wildcard; it must not")
		}
	}
}

func TestLocalCNAMEChase(t *testing.T) {
	srv, _ := startLocalProxy(t)
	resp := query(t, srv.UDPAddr().String(), "media.home.lab", dns.TypeA)
	if len(resp.Answer) != 2 {
		t.Fatalf("answers = %d, want CNAME + A", len(resp.Answer))
	}
	cname, ok := resp.Answer[0].(*dns.CNAME)
	if !ok || cname.Target != "nas.home.lab." {
		t.Errorf("first answer = %v, want CNAME to nas.home.lab.", resp.Answer[0])
	}
	a, ok := resp.Answer[1].(*dns.A)
	if !ok || !a.A.Equal(net.IPv4(192, 168, 1, 10)) {
		t.Errorf("second answer = %v, want chased A 192.168.1.10", resp.Answer[1])
	}
	if a.Hdr.Name != "nas.home.lab." {
		t.Errorf("chased A owner = %s, want nas.home.lab.", a.Hdr.Name)
	}
}

func TestLocalPTR(t *testing.T) {
	srv, _ := startLocalProxy(t)
	resp := query(t, srv.UDPAddr().String(), "10.1.168.192.in-addr.arpa", dns.TypePTR)
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1 PTR", len(resp.Answer))
	}
	if ptr, ok := resp.Answer[0].(*dns.PTR); !ok || ptr.Ptr != "nas.home.lab." {
		t.Errorf("answer = %v, want PTR to nas.home.lab.", resp.Answer[0])
	}
}

func TestLocalUnsupportedTypeStaysLocal(t *testing.T) {
	srv, _ := startLocalProxy(t)
	// TXT for a local name: empty NOERROR, never forwarded (the stub
	// upstream would have answered nothing for TXT anyway, so assert
	// authoritative to prove it came from the zone).
	resp := query(t, srv.UDPAddr().String(), "nas.home.lab", dns.TypeTXT)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 || !resp.Authoritative {
		t.Errorf("rcode=%s answers=%d aa=%v, want authoritative empty NOERROR",
			dns.RcodeToString[resp.Rcode], len(resp.Answer), resp.Authoritative)
	}
}

func TestLocalRecordValidation(t *testing.T) {
	cases := []struct {
		name string
		rec  config.LocalRecord
	}{
		{"bad name", config.LocalRecord{Name: "not a domain!", A: []string{"192.168.1.1"}}},
		{"bad ipv4", config.LocalRecord{Name: "x.lab", A: []string{"fd00::1"}}},
		{"bad ipv6", config.LocalRecord{Name: "x.lab", AAAA: []string{"192.168.1.1"}}},
		{"cname and a", config.LocalRecord{Name: "x.lab", A: []string{"192.168.1.1"}, CNAME: "y.lab"}},
		{"empty record", config.LocalRecord{Name: "x.lab"}},
		{"bad cname", config.LocalRecord{Name: "x.lab", CNAME: "bad cname"}},
	}
	for _, tc := range cases {
		cfg := config.Default()
		cfg.DNS.LocalRecords = []config.LocalRecord{tc.rec}
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted %+v", tc.name, tc.rec)
		}
	}

	good := config.Default()
	good.DNS.LocalRecords = []config.LocalRecord{
		{Name: "*.home.lab", A: []string{"10.0.0.1"}},
		{Name: "alias.home.lab", CNAME: "target.home.lab"},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("Validate() rejected valid records: %v", err)
	}
}

// waitFor polls until cond is true or the deadline passes (the query log
// writer is asynchronous).
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never arrived", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
