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
		go func(src string) { found <- queryMDNSFrom(query, src) }(src)
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

// queryMDNSFrom sends the query bound to source IP src (empty = default
// interface) and returns the first PTR name in a reply, or "" on timeout.
func queryMDNSFrom(query []byte, src string) string {
	conn, err := net.ListenPacket("udp4", net.JoinHostPort(src, "0"))
	if err != nil {
		return ""
	}
	defer conn.Close()
	if _, err := conn.WriteTo(query, mdnsGroup); err != nil {
		return ""
	}
	_ = conn.SetReadDeadline(time.Now().Add(mdnsTimeout))
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return "" // deadline reached, or read error
		}
		resp := new(dns.Msg)
		if resp.Unpack(buf[:n]) != nil {
			continue
		}
		for _, ans := range resp.Answer {
			if ptr, ok := ans.(*dns.PTR); ok && ptr.Ptr != "" {
				return strings.TrimSuffix(ptr.Ptr, ".")
			}
		}
	}
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
