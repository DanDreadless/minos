package dnsproxy

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Who may use Minos, and how fast. Both run first in handle(), before any
// other work, because they protect everything after them.
//
// Access control: by default only private and local networks are answered
// (dns.allowed_networks empty). A resolver that answers the whole internet
// is an "open resolver" — used to amplify attacks on third parties — and a
// home DNS server exposed by a port-forward or a bad firewall rule should
// not become one by accident.
//
// Rate limiting: each device gets dns.rate_limit queries per minute
// (default 1000, as Pi-hole; 0 disables). A runaway app or an infected
// device then cannot drown the server or the query log.

// ListRateLimit attributes the logged refusal of a device over its limit.
const ListRateLimit = "rate-limit"

// privateNets are the networks answered when no allow list is configured.
var privateNets = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT, and Tailscale
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// accessList is the compiled dns.allowed_networks. auto is the default
// list: private networks plus the subnets of this machine's own
// interfaces. The latter matters for IPv6, where LAN devices usually have
// global addresses: without it every IPv6 client would be refused.
type accessList struct {
	prefixes []netip.Prefix
	auto     bool
	built    int64 // minute the interface subnets were read (auto only)
}

func buildAccessList(networks []string, now time.Time) *accessList {
	if len(networks) == 0 {
		p := append(append([]netip.Prefix{}, privateNets...), localSubnets()...)
		return &accessList{prefixes: p, auto: true, built: now.Unix() / 60}
	}
	a := &accessList{}
	for _, n := range networks {
		if p, err := netip.ParsePrefix(n); err == nil {
			a.prefixes = append(a.prefixes, p.Masked())
		} else if ip, err := netip.ParseAddr(n); err == nil {
			a.prefixes = append(a.prefixes, netip.PrefixFrom(ip, ip.BitLen()))
		}
	}
	return a
}

func (a *accessList) allows(ip netip.Addr) bool {
	for _, p := range a.prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// localSubnets are the networks this machine's interfaces sit on.
func localSubnets() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		ones, _ := n.Mask.Size()
		if p, err := ip.Unmap().Prefix(ones); err == nil && ones > 0 {
			out = append(out, p)
		}
	}
	return out
}

// remoteAddr is the client's address without allocating a string.
func remoteAddr(addr net.Addr) netip.Addr {
	var ip net.IP
	switch a := addr.(type) {
	case *net.UDPAddr:
		ip = a.IP
	case *net.TCPAddr:
		ip = a.IP
	default:
		if ap, err := netip.ParseAddrPort(addr.String()); err == nil {
			return ap.Addr().Unmap()
		}
		return netip.Addr{}
	}
	ip4, _ := netip.AddrFromSlice(ip)
	return ip4.Unmap()
}

// rateLimiter counts queries per client in fixed one-minute windows —
// the same model as Pi-hole's rate_limit, and cheap: one map load and two
// atomic adds per query, no locks.
type rateLimiter struct {
	perMinute int64
	clients   sync.Map // netip.Addr → *rateBucket
	swept     atomic.Int64
	now       func() time.Time
}

type rateBucket struct {
	window atomic.Int64 // minute number the count belongs to
	count  atomic.Int64
	warned atomic.Int64 // last window a refusal was logged for this client
}

func newRateLimiter(perMinute int) *rateLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &rateLimiter{perMinute: int64(perMinute), now: time.Now}
}

// allow counts one query from ip and reports whether it is within the
// limit, and — for a refusal — whether it is the first this window (so the
// query log gets one line per client per minute, not thousands).
func (l *rateLimiter) allow(ip netip.Addr) (ok, firstRefusal bool) {
	minute := l.now().Unix() / 60
	v, found := l.clients.Load(ip)
	if !found {
		v, _ = l.clients.LoadOrStore(ip, &rateBucket{})
	}
	b := v.(*rateBucket)
	if w := b.window.Load(); w != minute && b.window.CompareAndSwap(w, minute) {
		b.count.Store(0)
	}
	if l.swept.Load() != minute && l.swept.CompareAndSwap(l.swept.Load(), minute) {
		go l.sweep(minute)
	}
	if b.count.Add(1) <= l.perMinute {
		return true, false
	}
	return false, b.warned.Swap(minute) != minute
}

// sweep forgets clients idle since before the previous window, so the map
// holds only devices seen in the last couple of minutes.
func (l *rateLimiter) sweep(minute int64) {
	l.clients.Range(func(k, v any) bool {
		if v.(*rateBucket).window.Load() < minute-1 {
			l.clients.Delete(k)
		}
		return true
	})
}
