package dnsproxy

import "github.com/miekg/dns"

// Extended DNS Errors (RFC 8914) say *why* an answer looks the way it
// does — "blocked by Minos, list StevenBlack" rather than a bare 0.0.0.0 or
// SERVFAIL — so tools like dig, kdig and browsers' network panels can show
// the reason. They ride in the OPT record, so they are only ever added for
// a client that sent EDNS0 itself: a client that did not cannot parse them.

// ednsUDPSize is the size advertised on an OPT record Minos adds itself:
// the DNS-flag-day default, safe on any path.
const ednsUDPSize = 1232

// withEDE attaches an extended error to reply when the request carried
// EDNS0, reusing reply's OPT record if it already has one. The text is
// prefix+detail, joined only once it is known to be needed: building it at
// the call site cost every blocked query an allocation, EDNS0 or not.
func withEDE(req, reply *dns.Msg, code uint16, prefix, detail string) {
	ro := req.IsEdns0()
	if ro == nil {
		return
	}
	text := prefix + detail
	opt := reply.IsEdns0()
	if opt == nil {
		opt = &dns.OPT{Hdr: dns.RR_Header{Name: ".", Rrtype: dns.TypeOPT}}
		opt.SetUDPSize(ednsUDPSize)
		if ro.Do() {
			opt.SetDo()
		}
		reply.Extra = append(reply.Extra, opt)
	}
	opt.Option = append(opt.Option, &dns.EDNS0_EDE{InfoCode: code, ExtraText: text})
}
