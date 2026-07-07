package clients

import (
	"os"
	"testing"

	"github.com/miekg/dns"
)

func TestBuildMDNSQuery(t *testing.T) {
	packed, ok := buildMDNSQuery("192.168.1.5")
	if !ok {
		t.Fatal("buildMDNSQuery(ipv4) ok=false, want true")
	}
	var m dns.Msg
	if err := m.Unpack(packed); err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(m.Question) != 1 {
		t.Fatalf("got %d questions, want 1", len(m.Question))
	}
	q := m.Question[0]
	if q.Name != "5.1.168.192.in-addr.arpa." {
		t.Errorf("reverse name = %q", q.Name)
	}
	if q.Qtype != dns.TypePTR {
		t.Errorf("qtype = %d, want PTR", q.Qtype)
	}
	if q.Qclass != dns.ClassINET|mdnsUnicastBit {
		t.Errorf("qclass = %#x, want unicast-response bit set on ClassINET", q.Qclass)
	}
}

func TestBuildMDNSQueryRejectsNonIPv4(t *testing.T) {
	for _, in := range []string{"::1", "fe80::1", "not-an-ip", ""} {
		if _, ok := buildMDNSQuery(in); ok {
			t.Errorf("buildMDNSQuery(%q) ok=true, want false", in)
		}
	}
}

func TestLookupMDNSInvalidReturnsEmpty(t *testing.T) {
	// No network round trip for invalid input — must fail fast, not block.
	if got := lookupMDNS("not-an-ip"); got != "" {
		t.Errorf("lookupMDNS(invalid) = %q, want empty", got)
	}
}

// Live smoke test: set MINOS_MDNS_LIVE=<ip> to probe a real device on the LAN,
// e.g. MINOS_MDNS_LIVE=192.168.68.130 go test ./internal/clients -run MDNSLive.
// Skipped by default so CI never depends on the network.
func TestMDNSLive(t *testing.T) {
	ip := os.Getenv("MINOS_MDNS_LIVE")
	if ip == "" {
		t.Skip("set MINOS_MDNS_LIVE=<device-ip> to run the live mDNS probe")
	}
	t.Logf("mDNS reverse lookup for %s = %q", ip, lookupMDNS(ip))
}
