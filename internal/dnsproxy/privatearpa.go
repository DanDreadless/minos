package dnsproxy

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// Private reverse zones (RFC 6303, plus RFC 7793's CGNAT range) are
// answered locally and never forwarded: an upstream cannot know your LAN,
// so forwarding a 192.168.x.x PTR is always a wasted WAN round trip and a
// small privacy leak. Local records (auto-PTR) and conditional-forwarding
// routes take precedence — that is how LAN reverse lookups are answered
// for real; this is the backstop for everything neither covers.
var privateArpaZones = func() map[string]struct{} {
	zones := []string{
		// RFC 1918 + special-use IPv4 (RFC 6303 §4.1-4.2)
		"10.in-addr.arpa",
		"168.192.in-addr.arpa",
		"0.in-addr.arpa",
		"127.in-addr.arpa",
		"254.169.in-addr.arpa",
		"2.0.192.in-addr.arpa",
		"100.51.198.in-addr.arpa",
		"113.0.203.in-addr.arpa",
		"255.255.255.255.in-addr.arpa",
		// IPv6 ULA, link-local, and documentation (RFC 6303 §4.3-4.5)
		"d.f.ip6.arpa",
		"8.e.f.ip6.arpa",
		"9.e.f.ip6.arpa",
		"a.e.f.ip6.arpa",
		"b.e.f.ip6.arpa",
		"8.b.d.0.1.0.0.2.ip6.arpa",
		// IPv6 loopback (::1) and unspecified (::)
		"1." + strings.Repeat("0.", 31) + "ip6.arpa",
		strings.Repeat("0.", 32) + "ip6.arpa",
	}
	for i := 16; i <= 31; i++ { // 172.16.0.0/12
		zones = append(zones, fmt.Sprintf("%d.172.in-addr.arpa", i))
	}
	for i := 64; i <= 127; i++ { // 100.64.0.0/10 CGNAT (RFC 7793)
		zones = append(zones, fmt.Sprintf("%d.100.in-addr.arpa", i))
	}
	set := make(map[string]struct{}, len(zones))
	for _, z := range zones {
		set[z] = struct{}{}
	}
	return set
}()

// matchPrivateArpa returns the private reverse zone containing qname
// (which may be the zone apex itself), or "". qname must be normalized.
func matchPrivateArpa(qname string) string {
	// Cheap guard: every zone ends in .arpa, so most queries skip the walk.
	if !strings.HasSuffix(qname, ".arpa") {
		return ""
	}
	if _, ok := privateArpaZones[qname]; ok {
		return qname
	}
	for i := 0; i < len(qname); i++ {
		if qname[i] == '.' {
			if _, ok := privateArpaZones[qname[i+1:]]; ok {
				return qname[i+1:]
			}
		}
	}
	return ""
}

// privateArpaSOA is the synthesized empty-zone SOA (RFC 6303 §3 timers).
func privateArpaSOA(zone string) dns.RR {
	return &dns.SOA{
		Hdr: dns.RR_Header{
			Name: dns.Fqdn(zone), Rrtype: dns.TypeSOA,
			Class: dns.ClassINET, Ttl: 10800,
		},
		Ns: "localhost.", Mbox: "nobody.invalid.",
		Serial: 1, Refresh: 604800, Retry: 86400,
		Expire: 2419200, Minttl: 10800,
	}
}

// privateArpaAnswer synthesizes the empty-zone response: NXDOMAIN with the
// SOA in authority (so clients negative-cache), and NOERROR + SOA for a
// query of the zone apex itself.
func privateArpaAnswer(req *dns.Msg, q dns.Question, qname, zone string) *dns.Msg {
	reply := new(dns.Msg)
	if qname == zone && q.Qtype == dns.TypeSOA {
		reply.SetReply(req)
		reply.Authoritative = true
		reply.Answer = []dns.RR{privateArpaSOA(zone)}
		return reply
	}
	reply.SetRcode(req, dns.RcodeNameError)
	reply.Authoritative = true
	reply.Ns = []dns.RR{privateArpaSOA(zone)}
	return reply
}
