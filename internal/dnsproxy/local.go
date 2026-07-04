package dnsproxy

import (
	"net"
	"strings"

	"github.com/miekg/dns"

	"minos/internal/config"
	"minos/internal/filter"
)

// maxCNAMEHops caps local CNAME chasing so alias cycles terminate.
const maxCNAMEHops = 8

// localZone answers configured local records without forwarding. Local names
// deliberately beat the blocklists: an explicit record is stronger intent
// than a subscribed list, and local queries must never leak upstream.
// Like a Matcher, a zone is immutable — config changes swap in a new one.
type localZone struct {
	exact map[string]*localRecord
	// wild is keyed by the parent name ("home.lab" for "*.home.lab") and
	// matches subdomains at any depth, but not the bare parent.
	wild map[string]*localRecord
	// ptr maps reverse names ("10.1.168.192.in-addr.arpa") to FQDNs,
	// synthesized from the address records above.
	ptr map[string]string
	ttl uint32
}

type localRecord struct {
	name  string // as configured; the "rule" shown in the query log
	a     []net.IP
	aaaa  []net.IP
	cname string // normalized target, "" if none
}

// buildLocalZone compiles the config's local records; nil when there are none.
func buildLocalZone(cfg *config.Config) *localZone {
	if len(cfg.DNS.LocalRecords) == 0 {
		return nil
	}
	z := &localZone{
		exact: make(map[string]*localRecord),
		wild:  make(map[string]*localRecord),
		ptr:   make(map[string]string),
		ttl:   cfg.DNS.LocalTTL,
	}
	for _, rec := range cfg.DNS.LocalRecords {
		r := &localRecord{
			name:  rec.Name,
			cname: filter.NormalizeDomain(rec.CNAME),
		}
		for _, s := range rec.A {
			r.a = append(r.a, net.ParseIP(s))
		}
		for _, s := range rec.AAAA {
			r.aaaa = append(r.aaaa, net.ParseIP(s))
		}
		if wildParent, isWild := strings.CutPrefix(rec.Name, "*."); isWild {
			z.wild[filter.NormalizeDomain(wildParent)] = r
			continue // a wildcard has no single address to reverse
		}
		name := filter.NormalizeDomain(rec.Name)
		z.exact[name] = r
		for _, s := range append(rec.A, rec.AAAA...) {
			if rev, err := dns.ReverseAddr(s); err == nil {
				if _, taken := z.ptr[filter.NormalizeDomain(rev)]; !taken {
					z.ptr[filter.NormalizeDomain(rev)] = dns.Fqdn(name)
				}
			}
		}
	}
	return z
}

// lookup finds the record for a normalized qname: exact match first, then
// the nearest wildcard parent.
func (z *localZone) lookup(qname string) *localRecord {
	if r, ok := z.exact[qname]; ok {
		return r
	}
	for i := 0; i < len(qname); i++ {
		if qname[i] == '.' {
			if r, ok := z.wild[qname[i+1:]]; ok {
				return r
			}
		}
	}
	return nil
}

// answer resolves q against the zone. ok is false when the name is not
// local and the query should continue down the normal judge/forward path.
// A local name always terminates here — unsupported query types get an
// empty NOERROR rather than leaking upstream.
func (z *localZone) answer(req *dns.Msg, q dns.Question, qname string) (reply *dns.Msg, rule string, ok bool) {
	if q.Qclass != dns.ClassINET {
		return nil, "", false
	}
	if q.Qtype == dns.TypePTR {
		target, found := z.ptr[qname]
		if !found {
			return nil, "", false
		}
		reply = z.reply(req)
		reply.Answer = []dns.RR{&dns.PTR{
			Hdr: z.header(q.Name, dns.TypePTR),
			Ptr: target,
		}}
		return reply, qname, true
	}
	r := z.lookup(qname)
	if r == nil {
		return nil, "", false
	}
	rule = r.name // the record that matched, even after a CNAME chase
	reply = z.reply(req)
	owner := q.Name
	for hops := 0; r != nil && hops < maxCNAMEHops; hops++ {
		if r.cname != "" {
			target := dns.Fqdn(r.cname)
			reply.Answer = append(reply.Answer, &dns.CNAME{
				Hdr:    z.header(owner, dns.TypeCNAME),
				Target: target,
			})
			if q.Qtype == dns.TypeCNAME {
				break
			}
			owner = target
			r = z.lookup(r.cname)
			continue
		}
		switch q.Qtype {
		case dns.TypeA:
			for _, ip := range r.a {
				reply.Answer = append(reply.Answer, &dns.A{
					Hdr: z.header(owner, dns.TypeA), A: ip,
				})
			}
		case dns.TypeAAAA:
			for _, ip := range r.aaaa {
				reply.Answer = append(reply.Answer, &dns.AAAA{
					Hdr: z.header(owner, dns.TypeAAAA), AAAA: ip,
				})
			}
		}
		break
	}
	return reply, rule, true
}

func (z *localZone) reply(req *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	return m
}

func (z *localZone) header(name string, rrtype uint16) dns.RR_Header {
	return dns.RR_Header{Name: name, Rrtype: rrtype, Class: dns.ClassINET, Ttl: z.ttl}
}
