package dnsproxy

import (
	"context"

	"github.com/miekg/dns"
)

// safeSearchHosts maps exact query names to the provider's enforced-safe
// host. Exact names only — never whole subtrees, or accounts.google.com and
// friends would break. The safe host's addresses are resolved upstream at
// answer time (and cached under the safe host's own key), then served with
// a CNAME so clients see why the answer differs.
var safeSearchHosts = map[string]string{
	// Google Search (common country domains; www and bare)
	"google.com": "forcesafesearch.google.com", "www.google.com": "forcesafesearch.google.com",
	"google.co.uk": "forcesafesearch.google.com", "www.google.co.uk": "forcesafesearch.google.com",
	"google.ca": "forcesafesearch.google.com", "www.google.ca": "forcesafesearch.google.com",
	"google.com.au": "forcesafesearch.google.com", "www.google.com.au": "forcesafesearch.google.com",
	"google.co.nz": "forcesafesearch.google.com", "www.google.co.nz": "forcesafesearch.google.com",
	"google.co.in": "forcesafesearch.google.com", "www.google.co.in": "forcesafesearch.google.com",
	"google.ie": "forcesafesearch.google.com", "www.google.ie": "forcesafesearch.google.com",
	"google.de": "forcesafesearch.google.com", "www.google.de": "forcesafesearch.google.com",
	"google.fr": "forcesafesearch.google.com", "www.google.fr": "forcesafesearch.google.com",
	"google.es": "forcesafesearch.google.com", "www.google.es": "forcesafesearch.google.com",
	"google.it": "forcesafesearch.google.com", "www.google.it": "forcesafesearch.google.com",
	"google.nl": "forcesafesearch.google.com", "www.google.nl": "forcesafesearch.google.com",

	// Bing
	"bing.com": "strict.bing.com", "www.bing.com": "strict.bing.com",

	// DuckDuckGo
	"duckduckgo.com": "safe.duckduckgo.com", "www.duckduckgo.com": "safe.duckduckgo.com",
	"start.duckduckgo.com": "safe.duckduckgo.com",

	// YouTube — moderate restriction (restrict.youtube.com is the strict tier)
	"youtube.com": "restrictmoderate.youtube.com", "www.youtube.com": "restrictmoderate.youtube.com",
	"m.youtube.com": "restrictmoderate.youtube.com", "youtubei.googleapis.com": "restrictmoderate.youtube.com",
	"youtube.googleapis.com": "restrictmoderate.youtube.com", "www.youtube-nocookie.com": "restrictmoderate.youtube.com",
}

// answerSafeSearch serves a safe-search rewrite for qname → target: the safe
// host's records under a CNAME, or an empty NOERROR for HTTPS/SVCB queries
// so encrypted-hello hints can't sidestep the rewrite. ok is false only for
// query types that should keep flowing down the normal path.
func (s *Server) answerSafeSearch(w dns.ResponseWriter, req *dns.Msg, q dns.Question, target string) bool {
	switch q.Qtype {
	case dns.TypeA, dns.TypeAAAA:
	case dns.TypeHTTPS, dns.TypeSVCB:
		reply := new(dns.Msg)
		reply.SetReply(req)
		_ = w.WriteMsg(reply)
		return true
	default:
		return false
	}

	// Resolve the safe host, reusing the response cache under the safe
	// host's own key so rewrites never poison normal lookups.
	var resp *dns.Msg
	cache := s.cache.Load()
	key := cacheKey(target, q.Qtype, req)
	if cache != nil {
		// A stale safe-host answer is treated as a miss: this path has no
		// background refresh, so resolve fresh instead.
		if hit, stale := cache.get(key, req); hit != nil && !stale {
			resp = hit
		}
	}
	if resp == nil {
		lookup := new(dns.Msg)
		lookup.SetQuestion(dns.Fqdn(target), q.Qtype)
		ctx, cancel := context.WithTimeout(context.Background(), 2*upstreamTimeout)
		defer cancel()
		var err error
		resp, _, _, err = s.forward(ctx, lookup, target)
		if err != nil {
			reply := new(dns.Msg)
			reply.SetRcode(req, dns.RcodeServerFailure)
			_ = w.WriteMsg(reply)
			return true
		}
		if cache != nil {
			cache.put(key, resp)
		}
	}

	reply := new(dns.Msg)
	reply.SetReply(req)
	reply.Rcode = resp.Rcode
	reply.Answer = make([]dns.RR, 0, len(resp.Answer)+1)
	reply.Answer = append(reply.Answer, &dns.CNAME{
		Hdr: dns.RR_Header{
			Name: q.Name, Rrtype: dns.TypeCNAME,
			Class: dns.ClassINET, Ttl: safeSearchTTL(resp),
		},
		Target: dns.Fqdn(target),
	})
	reply.Answer = append(reply.Answer, resp.Answer...)
	_ = w.WriteMsg(reply)
	return true
}

// safeSearchTTL keeps the synthesized CNAME in step with the real records.
func safeSearchTTL(resp *dns.Msg) uint32 {
	if len(resp.Answer) > 0 {
		return resp.Answer[0].Header().Ttl
	}
	return 60
}
