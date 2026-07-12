package clients

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
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

func TestExtractMDNSModel(t *testing.T) {
	txt := func(name string, strs ...string) *dns.Msg {
		return &dns.Msg{Answer: []dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET},
			Txt: strs,
		}}}
	}
	cases := []struct {
		name string
		msg  *dns.Msg
		want string
	}{
		{"well-formed", txt("Mac._device-info._tcp.local.", "model=MacBookPro18,3"), "MacBookPro18,3"},
		{"other keys first", txt("x._device-info._tcp.local.", "osxvers=23", "model=J293AP"), "J293AP"},
		{"no model key", txt("x._device-info._tcp.local.", "osxvers=23"), ""},
		{"empty value", txt("x._device-info._tcp.local.", "model="), ""},
		{"control bytes rejected", txt("x._device-info._tcp.local.", "model=evil\x1b[2J"), ""},
		{"oversized record rejected", txt("x._device-info._tcp.local.",
			strings.Repeat("a", 600), "model=late"), ""},
		{"oversized value rejected", txt("x._device-info._tcp.local.",
			"model="+strings.Repeat("a", 100)), ""},
		{"no txt at all", &dns.Msg{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractMDNSModel(tc.msg); got != tc.want {
				t.Errorf("extractMDNSModel = %q, want %q", got, tc.want)
			}
		})
	}
}

// The passive listener accepts only self-claims: a device may name itself,
// never a neighbour, and only devices already seen get identity rows.
func TestHarvestAnnouncement(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())
	r.Touch("192.168.1.30", false, time.Now())

	aRec := func(name, ip string) *dns.A {
		return &dns.A{
			Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET},
			A:   net.ParseIP(ip).To4(),
		}
	}
	deviceInfo := &dns.TXT{
		Hdr: dns.RR_Header{Name: "TV._device-info._tcp.local.", Rrtype: dns.TypeTXT, Class: dns.ClassINET},
		Txt: []string{"model=QE55Q80A"},
	}

	// Self-claim: A record matches the announcing source → name + model land.
	msg := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true},
		Answer: []dns.RR{aRec("tv.local.", "192.168.1.30"), deviceInfo}}
	r.harvestAnnouncement(net.ParseIP("192.168.1.30"), msg)
	if n, s := hostnameOf(r, "192.168.1.30"); n != "tv.local" || s != SourceMDNS {
		t.Errorf("self-claim name = %q/%q, want tv.local/mdns", n, s)
	}
	devs := r.Devices(config.Default())
	if len(devs) != 1 || devs[0].Model != "QE55Q80A" {
		t.Errorf("model = %+v, want QE55Q80A", devs)
	}

	// Third-party claim: src announces a record for a DIFFERENT IP → ignored.
	r.Touch("192.168.1.31", false, time.Now())
	spoof := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true},
		Answer: []dns.RR{aRec("evil.local.", "192.168.1.31")}}
	r.harvestAnnouncement(net.ParseIP("192.168.1.99"), spoof)
	if n, _ := hostnameOf(r, "192.168.1.31"); n != "" {
		t.Errorf("third-party claim landed: %q", n)
	}

	// Unknown source (never queried Minos) → no identity row created.
	ghost := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true},
		Answer: []dns.RR{aRec("ghost.local.", "192.168.1.200")}}
	r.harvestAnnouncement(net.ParseIP("192.168.1.200"), ghost)
	if _, ok := r.seen.Load("192.168.1.200"); ok {
		t.Error("announcement created a row for a device that never queried")
	}

	// Non-.local names never land (that would let mDNS spoof LAN DNS names).
	notLocal := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true},
		Answer: []dns.RR{aRec("router.lan.", "192.168.1.30")}}
	r.harvestAnnouncement(net.ParseIP("192.168.1.30"), notLocal)
	if n, _ := hostnameOf(r, "192.168.1.30"); n != "tv.local" {
		t.Errorf("non-.local name replaced the mDNS one: %q", n)
	}
}

func TestLookupMDNSModelGuards(t *testing.T) {
	// Only .local names identify an mDNS speaker; anything else must not
	// trigger network traffic (returns immediately).
	if got := lookupMDNSModel("192.168.1.5", "printer.lan"); got != "" {
		t.Errorf("non-.local name = %q, want empty", got)
	}
	if got := lookupMDNSModel("192.168.1.5", ".local"); got != "" {
		t.Errorf("empty instance = %q, want empty", got)
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
