package dnsproxy

import (
	"net"
	"strings"

	"github.com/miekg/dns"

	"minos/internal/filter"
)

// ListRebinding attributes answers refused by DNS-rebinding protection.
const ListRebinding = "rebinding"

// DNS rebinding: a public name (evil.example) that answers with an address
// on your own network (192.168.1.1) lets a web page talk to your router,
// NAS or printer from inside the browser's same-origin rules. With the
// protection on, such answers are refused. Opt-in (dns.rebind_protection):
// some legitimate services do exactly this — Plex's *.plex.direct names
// resolve to the server's LAN address — and the fix for those is to allow
// the domain, which exempts it.

// privateSuffixes are names that are meant to resolve to LAN addresses.
var privateSuffixes = []string{
	"lan", "local", "home", "home.arpa", "internal", "localdomain", "intranet", "corp", "private",
}

// rebindExempt reports names that may legitimately point inside the
// network: single-label names and the private suffixes above.
func rebindExempt(qname string) bool {
	if !strings.Contains(qname, ".") {
		return true
	}
	for _, suf := range privateSuffixes {
		if qname == suf || strings.HasSuffix(qname, "."+suf) {
			return true
		}
	}
	return false
}

// rebindIP is the first answer address that points inside a private
// network — RFC 1918, ULA (fc00::/7), loopback or link-local — or nil.
// 0.0.0.0 and :: are not private: upstream filters answer blocks with them.
func rebindIP(resp *dns.Msg) net.IP {
	for _, rr := range resp.Answer {
		var ip net.IP
		switch r := rr.(type) {
		case *dns.A:
			ip = r.A
		case *dns.AAAA:
			ip = r.AAAA
		default:
			continue
		}
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return ip
		}
	}
	return nil
}

// rebinding decides whether resp is a rebinding answer Minos should refuse:
// protection on, the name not allowed by a rule, not a private name, not
// answered through a conditional route (the router knows the LAN), and an
// address inside the network in the answer.
func (s *Server) rebinding(qname string, verdict filter.Result, resp *dns.Msg) net.IP {
	if !s.rebindProtect.Load() || verdict.Rule != "" || rebindExempt(qname) {
		return nil
	}
	ip := rebindIP(resp)
	if ip == nil || s.fwd.Load().route(qname) != nil {
		return nil
	}
	return ip
}
