package dnsproxy

import (
	"crypto/x509"
	"net"
	"strconv"

	"github.com/miekg/dns"
)

// Discovery of Designated Resolvers (DDR, RFC 9462): a device that uses
// Minos over plain DNS asks for _dns.resolver.arpa SVCB to learn whether
// Minos also speaks DNS-over-TLS or DNS-over-HTTPS — and Windows 11, iOS
// and macOS then switch to the encrypted one by themselves. Clients verify
// the upgrade: the certificate must name the address they were using
// (an IP SAN), or they keep the plain connection.
//
// resolver.arpa is special-use and is never forwarded. Forwarding it would
// let the upstream (Cloudflare, say) answer with its own encrypted
// endpoint, and a device could move its DNS off Minos altogether.

const ddrQName = "_dns.resolver.arpa"

// certName is the first DNS name a certificate covers ("" for none).
func certName(der []byte) string {
	c, err := x509.ParseCertificate(der)
	if err != nil || len(c.DNSNames) == 0 {
		return ""
	}
	return c.DNSNames[0]
}

// answerDDR answers _dns.resolver.arpa itself: SVCB records for each
// encrypted listener when there are any, otherwise an empty answer.
func (s *Server) answerDDR(req *dns.Msg, q dns.Question) *dns.Msg {
	reply := new(dns.Msg)
	reply.SetReply(req)
	reply.Authoritative = true
	target := s.ddrTarget
	if q.Qtype != dns.TypeSVCB || target == "" {
		return reply // no designated resolver: NODATA, never forwarded
	}
	hdr := dns.RR_Header{Name: q.Name, Rrtype: dns.TypeSVCB, Class: dns.ClassINET, Ttl: 300}
	prio := uint16(1)
	if s.dotAddr != nil {
		reply.Answer = append(reply.Answer, &dns.SVCB{
			Hdr: hdr, Priority: prio, Target: dns.Fqdn(target),
			Value: []dns.SVCBKeyValue{&dns.SVCBAlpn{Alpn: []string{"dot"}}, &dns.SVCBPort{Port: portOf(s.dotAddr)}},
		})
		prio++
	}
	if s.dohAddr != nil {
		reply.Answer = append(reply.Answer, &dns.SVCB{
			Hdr: hdr, Priority: prio, Target: dns.Fqdn(target),
			Value: []dns.SVCBKeyValue{
				&dns.SVCBAlpn{Alpn: []string{"h2"}},
				&dns.SVCBPort{Port: portOf(s.dohAddr)},
				&dns.SVCBDoHPath{Template: "/dns-query{?dns}"},
			},
		})
	}
	return reply
}

func portOf(a net.Addr) uint16 {
	_, p, err := net.SplitHostPort(a.String())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return uint16(n)
}
