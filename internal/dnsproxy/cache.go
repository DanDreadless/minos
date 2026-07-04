package dnsproxy

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// defaultNegativeTTL caches NXDOMAIN/NODATA answers lacking an SOA record
// (which would normally supply the negative TTL per RFC 2308).
const defaultNegativeTTL = 30

// dnsCache is a bounded, TTL-respecting cache of forwarded responses. It sits
// after the filter: blocked answers are synthesized and never cached, so
// pardons, sentences, and recess all keep working against live rules.
//
// Reads are one sync.Map load (no mutexes on the hot path); the size cap is
// enforced by a bounded sweep on the insert that crossed it. A cache instance
// is immutable in configuration — config changes swap in a fresh one.
type dnsCache struct {
	entries sync.Map // key string → *cacheEntry
	size    atomic.Int64
	max     int64
	minTTL  uint32
	maxTTL  uint32
	now     func() time.Time // stubbed in tests
}

type cacheEntry struct {
	// msg is the sanitized upstream response: original TTLs, no OPT record,
	// ID zeroed. Served copies are re-stamped per request.
	msg      *dns.Msg
	storedAt time.Time
	expires  time.Time
}

func newCache(cfg config.CacheConfig) *dnsCache {
	return &dnsCache{
		max:    int64(cfg.MaxEntries),
		minTTL: cfg.MinTTL,
		maxTTL: cfg.MaxTTL,
		now:    time.Now,
	}
}

// cacheKey identifies one cacheable question. The DNSSEC OK bit is part of
// the key: validating clients get answers with DNSSEC records, others don't.
func cacheKey(qname string, qtype uint16, req *dns.Msg) string {
	key := qname + "|" + strconv.FormatUint(uint64(qtype), 10)
	if opt := req.IsEdns0(); opt != nil && opt.Do() {
		key += "|d"
	}
	return key
}

// get returns a response ready to send for req, or nil on a miss.
func (c *dnsCache) get(key string, req *dns.Msg) *dns.Msg {
	v, ok := c.entries.Load(key)
	if !ok {
		return nil
	}
	e := v.(*cacheEntry)
	now := c.now()
	if now.After(e.expires) {
		c.deleteKey(key)
		return nil
	}
	resp := e.msg.Copy()
	elapsed := uint32(now.Sub(e.storedAt) / time.Second)
	for _, sec := range [][]dns.RR{resp.Answer, resp.Ns, resp.Extra} {
		for _, rr := range sec {
			h := rr.Header()
			if h.Ttl > elapsed {
				h.Ttl -= elapsed
			} else {
				h.Ttl = 0
			}
		}
	}
	resp.Id = req.Id
	resp.Response = true
	resp.RecursionAvailable = true
	resp.Question = req.Question
	if opt := req.IsEdns0(); opt != nil {
		resp.SetEdns0(opt.UDPSize(), opt.Do())
	}
	return resp
}

// put stores a forwarded response if it is cacheable. The message is copied
// and sanitized, so the caller's copy can be sent to the client unchanged.
func (c *dnsCache) put(key string, resp *dns.Msg) {
	ttl, ok := c.cacheableTTL(resp)
	if !ok {
		return
	}
	stored := resp.Copy()
	stored.Id = 0
	// Drop OPT: EDNS is hop-by-hop, and the served copy re-adds one only
	// when the requesting client used EDNS itself.
	if len(stored.Extra) > 0 {
		kept := stored.Extra[:0]
		for _, rr := range stored.Extra {
			if rr.Header().Rrtype != dns.TypeOPT {
				kept = append(kept, rr)
			}
		}
		stored.Extra = kept
	}
	now := c.now()
	e := &cacheEntry{
		msg:      stored,
		storedAt: now,
		expires:  now.Add(time.Duration(ttl) * time.Second),
	}
	if _, loaded := c.entries.Swap(key, e); !loaded {
		if c.size.Add(1) > c.max {
			c.evict()
		}
	}
}

// cacheableTTL decides whether a response may be cached and for how long
// (seconds, clamped to [minTTL, maxTTL]).
func (c *dnsCache) cacheableTTL(resp *dns.Msg) (uint32, bool) {
	if resp.Truncated {
		return 0, false
	}
	var ttl uint32
	switch resp.Rcode {
	case dns.RcodeSuccess:
		if len(resp.Answer) == 0 {
			ttl = negativeTTL(resp) // NODATA
			break
		}
		ttl = resp.Answer[0].Header().Ttl
		for _, rr := range resp.Answer[1:] {
			if t := rr.Header().Ttl; t < ttl {
				ttl = t
			}
		}
	case dns.RcodeNameError:
		ttl = negativeTTL(resp)
	default:
		return 0, false
	}
	if ttl < c.minTTL {
		ttl = c.minTTL
	}
	if ttl > c.maxTTL {
		ttl = c.maxTTL
	}
	return ttl, ttl > 0
}

// negativeTTL derives the RFC 2308 negative-caching TTL from the SOA record
// in the authority section.
func negativeTTL(resp *dns.Msg) uint32 {
	for _, rr := range resp.Ns {
		if soa, ok := rr.(*dns.SOA); ok {
			if soa.Minttl < soa.Hdr.Ttl {
				return soa.Minttl
			}
			return soa.Hdr.Ttl
		}
	}
	return defaultNegativeTTL
}

// evict brings the cache back under its cap plus headroom (an extra 1/8 of
// the cap) so steady-state inserts don't sweep on every call. Expired
// entries go first, then arbitrary ones.
func (c *dnsCache) evict() {
	target := c.max - c.max/8
	over := c.size.Load() - target
	if over <= 0 {
		return
	}
	now := c.now()
	c.entries.Range(func(k, v any) bool {
		if now.After(v.(*cacheEntry).expires) && c.deleteKey(k.(string)) {
			over--
		}
		return over > 0
	})
	if over <= 0 {
		return
	}
	c.entries.Range(func(k, _ any) bool {
		if c.deleteKey(k.(string)) {
			over--
		}
		return over > 0
	})
}

func (c *dnsCache) deleteKey(key string) bool {
	if _, loaded := c.entries.LoadAndDelete(key); loaded {
		c.size.Add(-1)
		return true
	}
	return false
}
