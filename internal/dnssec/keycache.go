package dnssec

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// maxKeyTTL caps how long a validated DNSKEY set is reused regardless of
// its record TTL, so a compromised-then-rolled key ages out within a day.
const maxKeyTTL = 24 * time.Hour

// keyCache holds DNSKEY sets that already verified up to a trust anchor,
// making the steady-state cost of validation one signature check per
// answer. Same shape as dnsproxy's response cache: lock-free sync.Map
// reads, a bounded sweep on the insert that crossed the cap.
type keyCache struct {
	entries sync.Map // zone string → *keyEntry
	size    atomic.Int64
	max     int64
}

type keyEntry struct {
	keys    []*dns.DNSKEY
	expires time.Time
}

func newKeyCache(max int64) *keyCache {
	return &keyCache{max: max}
}

func (c *keyCache) get(zone string, now time.Time) []*dns.DNSKEY {
	v, ok := c.entries.Load(zone)
	if !ok {
		return nil
	}
	e := v.(*keyEntry)
	if now.After(e.expires) {
		c.deleteKey(zone)
		return nil
	}
	return e.keys
}

func (c *keyCache) put(zone string, keys []*dns.DNSKEY, ttl uint32, now time.Time) {
	life := min(time.Duration(ttl)*time.Second, maxKeyTTL)
	e := &keyEntry{keys: keys, expires: now.Add(life)}
	if _, loaded := c.entries.Swap(zone, e); !loaded {
		if c.size.Add(1) > c.max {
			c.evict(now)
		}
	}
}

// evict brings the cache back under its cap: expired entries first, then
// arbitrary ones. Zones re-validate on their next lookup, so eviction can
// never be worse than a cold start.
func (c *keyCache) evict(now time.Time) {
	over := c.size.Load() - c.max + 1
	c.entries.Range(func(k, v any) bool {
		if now.After(v.(*keyEntry).expires) && c.deleteKey(k.(string)) {
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

func (c *keyCache) deleteKey(zone string) bool {
	if _, loaded := c.entries.LoadAndDelete(zone); loaded {
		c.size.Add(-1)
		return true
	}
	return false
}
