package clients

import (
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const (
	mdnsTimeout = 750 * time.Millisecond
	// mdnsUnicastBit is the top bit of the question QCLASS (RFC 6762 §5.4):
	// it asks responders to reply unicast to the querier, so we can read the
	// answer on our own ephemeral socket instead of binding :5353.
	mdnsUnicastBit = 1 << 15
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// lookupMDNS resolves ip to a hostname via multicast DNS (RFC 6762): it sends
// a reverse PTR query to the mDNS group and reads a unicast reply from a device
// running Avahi/Bonjour that owns the address. IPv4 only for now, best-effort,
// off the hot path — returns "" on any failure or timeout. Used as the last
// hostname source, after unicast PTR (gateway, system) comes up empty.
//
// The query is sent bound to every suitable interface, concurrently: multicast
// egresses the interface a socket is bound to, so a multi-homed host (eth+wlan,
// Docker) still reaches the LAN where the device lives. First non-empty answer
// wins.
func lookupMDNS(ip string) string {
	query, ok := buildMDNSQuery(ip)
	if !ok {
		return ""
	}
	srcs := multicastSourceIPs()
	found := make(chan string, len(srcs))
	for _, src := range srcs {
		go func(src string) { found <- queryMDNSFrom(query, src, extractMDNSPTR) }(src)
	}
	timeout := time.After(mdnsTimeout + 250*time.Millisecond)
	for range srcs {
		select {
		case name := <-found:
			if name != "" {
				return name
			}
		case <-timeout:
			return ""
		}
	}
	return ""
}

// lookupMDNSDirect asks the device itself, unicast to ip:5353. Many stacks
// (Apple, Android, printers, ESPHome) answer direct queries they ignore on
// multicast, and the connected socket fast-fails on ICMP unreachable like
// the NetBIOS client, so a non-mDNS host costs milliseconds, not the timer.
func lookupMDNSDirect(ip string) string {
	query, ok := buildMDNSQuery(ip)
	if !ok {
		return ""
	}
	return queryMDNSUnicast(query, ip, extractMDNSPTR)
}

// queryMDNSFrom sends the query bound to source IP src (empty = default
// interface) and returns the first extracted answer, or "" on timeout.
func queryMDNSFrom(query []byte, src string, extract func(*dns.Msg) string) string {
	conn, err := net.ListenPacket("udp4", net.JoinHostPort(src, "0"))
	if err != nil {
		return ""
	}
	defer conn.Close()
	if _, err := conn.WriteTo(query, mdnsGroup); err != nil {
		return ""
	}
	return readMDNSReplies(conn, extract)
}

// queryMDNSUnicast sends the query on a connected socket straight to
// ip:5353 and reads replies on it.
func queryMDNSUnicast(query []byte, ip string, extract func(*dns.Msg) string) string {
	conn, err := net.DialTimeout("udp4", net.JoinHostPort(ip, "5353"), mdnsTimeout)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(query); err != nil {
		return ""
	}
	return readMDNSReplies(replyReader{conn}, extract)
}

// replyReader adapts a connected net.Conn to the ReadFrom shape the shared
// reply loop uses.
type replyReader struct{ net.Conn }

func (r replyReader) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := r.Read(b)
	return n, nil, err
}

func readMDNSReplies(conn interface {
	ReadFrom([]byte) (int, net.Addr, error)
	SetReadDeadline(time.Time) error
}, extract func(*dns.Msg) string,
) string {
	_ = conn.SetReadDeadline(time.Now().Add(mdnsTimeout))
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return "" // deadline reached, or ICMP-unreachable fast-fail
		}
		resp := new(dns.Msg)
		if resp.Unpack(buf[:n]) != nil {
			continue
		}
		if got := extract(resp); got != "" {
			return got
		}
	}
}

// extractMDNSPTR pulls the first usable PTR target out of a reply.
func extractMDNSPTR(m *dns.Msg) string {
	for _, ans := range m.Answer {
		if ptr, ok := ans.(*dns.PTR); ok && ptr.Ptr != "" {
			return sanitizeDiscoveredName(strings.TrimSuffix(ptr.Ptr, "."))
		}
	}
	return ""
}

// extractMDNSModel pulls a model= value from a _device-info TXT reply.
// The record is attacker-controllable: only the first well-formed key in a
// bounded record is considered, and the value is sanitised like any name.
func extractMDNSModel(m *dns.Msg) string {
	for _, ans := range m.Answer {
		txt, ok := ans.(*dns.TXT)
		if !ok {
			continue
		}
		budget := 512 // bytes of TXT considered, total
		for _, s := range txt.Txt {
			if budget -= len(s); budget < 0 {
				return ""
			}
			if v, found := strings.CutPrefix(s, "model="); found {
				return sanitizeDiscoveredName(v)
			}
		}
	}
	return ""
}

// lookupMDNSModel fetches the device's self-reported hardware model from
// its _device-info._tcp TXT record (an Apple convention much of the mDNS
// world follows), given its already-discovered .local hostname. Direct
// unicast first, multicast fallback, like hostname lookups.
func lookupMDNSModel(ip, localName string) string {
	instance := strings.TrimSuffix(localName, ".local")
	if instance == "" || instance == localName {
		return "" // only meaningful for mDNS .local names
	}
	m := new(dns.Msg)
	m.Id = dns.Id()
	m.Question = []dns.Question{{
		Name:   dns.Fqdn(instance + "._device-info._tcp.local"),
		Qtype:  dns.TypeTXT,
		Qclass: dns.ClassINET | mdnsUnicastBit,
	}}
	query, err := m.Pack()
	if err != nil {
		return ""
	}
	if model := queryMDNSUnicast(query, ip, extractMDNSModel); model != "" {
		return model
	}
	for _, src := range multicastSourceIPs() {
		if model := queryMDNSFrom(query, src, extractMDNSModel); model != "" {
			return model
		}
	}
	return ""
}

// multicastSourceIPs returns one IPv4 address per up, multicast-capable,
// non-loopback interface — the set to send the query from so it reaches every
// attached LAN. Falls back to the default interface ("") if none are found.
func multicastSourceIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{""}
	}
	var out []string
	for _, ifi := range ifaces {
		const want = net.FlagUp | net.FlagMulticast
		if ifi.Flags&want != want || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, ip4.String())
				break // one address per interface is enough
			}
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// buildMDNSQuery packs a reverse-PTR mDNS question for ip with the unicast
// response bit set, or reports ok=false for a non-IPv4 or invalid address.
func buildMDNSQuery(ip string) (packed []byte, ok bool) {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return nil, false // IPv4 only for now (mirrors the ARP limitation)
	}
	rev, err := dns.ReverseAddr(ip)
	if err != nil {
		return nil, false
	}
	m := new(dns.Msg)
	m.Id = dns.Id()
	m.Question = []dns.Question{{
		Name:   rev,
		Qtype:  dns.TypePTR,
		Qclass: dns.ClassINET | mdnsUnicastBit,
	}}
	b, err := m.Pack()
	if err != nil {
		return nil, false
	}
	return b, true
}
