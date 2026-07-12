package clients

import (
	"context"
	"log/slog"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// listenMDNS passively reads mDNS announcements: devices announce their
// address records (and _device-info TXTs) when they join the network, which
// names them within seconds — including the multicast-shy stacks that never
// answer our reverse queries. The socket only ever reads; nothing is
// transmitted. Runs until ctx ends; a failed group join (something else
// owns 5353 exclusively) logs once at Debug and gives up — the active
// queries still work.
func (r *Registry) listenMDNS(ctx context.Context) {
	conns := joinMDNSGroup()
	if len(conns) == 0 {
		slog.Debug("mDNS listener unavailable; passive announcements disabled")
		return
	}
	stop := context.AfterFunc(ctx, func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	defer stop()
	for _, conn := range conns {
		go r.readMDNSAnnouncements(conn)
	}
	<-ctx.Done()
}

// joinMDNSGroup joins 224.0.0.251:5353 on every eligible interface.
// net.ListenMulticastUDP sets address reuse, so coexisting with another
// mDNS stack on the host (Avahi, Bonjour) works.
func joinMDNSGroup() []*net.UDPConn {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var conns []*net.UDPConn
	for i := range ifaces {
		ifi := &ifaces[i]
		const want = net.FlagUp | net.FlagMulticast
		if ifi.Flags&want != want || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if conn, err := net.ListenMulticastUDP("udp4", ifi, mdnsGroup); err == nil {
			conns = append(conns, conn)
		}
	}
	return conns
}

// readMDNSAnnouncements consumes packets until the socket is closed. Every
// packet is untrusted: only self-claims are accepted — an address record is
// used only when it names the packet's own source IP, so a device can name
// itself but never a neighbour.
func (r *Registry) readMDNSAnnouncements(conn *net.UDPConn) {
	buf := make([]byte, 1500)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // closed on shutdown
		}
		msg := new(dns.Msg)
		if msg.Unpack(buf[:n]) != nil || !msg.Response {
			continue
		}
		r.harvestAnnouncement(src.IP, msg)
	}
}

func (r *Registry) harvestAnnouncement(src net.IP, msg *dns.Msg) {
	srcIP := src.String()
	if _, known := r.seen.Load(srcIP); !known {
		return // only devices that actually query Minos get identity rows
	}
	records := make([]dns.RR, 0, len(msg.Answer)+len(msg.Extra))
	records = append(records, msg.Answer...)
	records = append(records, msg.Extra...)
	// Cap the work a hostile burst can cause per packet.
	if len(records) > 64 {
		records = records[:64]
	}
	for _, rr := range records {
		switch v := rr.(type) {
		case *dns.A:
			if v.A.Equal(src) {
				r.setLocalName(srcIP, v.Hdr.Name)
			}
		case *dns.AAAA:
			if v.AAAA.Equal(src) {
				r.setLocalName(srcIP, v.Hdr.Name)
			}
		case *dns.TXT:
			if strings.HasSuffix(strings.ToLower(v.Hdr.Name), "._device-info._tcp.local.") {
				if model := extractMDNSModel(&dns.Msg{Answer: []dns.RR{v}}); model != "" {
					r.setModel(srcIP, "", model, SourceMDNS)
				}
			}
		}
	}
}

// setLocalName records an announced .local hostname for its own announcer.
func (r *Registry) setLocalName(ip, fqdn string) {
	name := sanitizeDiscoveredName(strings.TrimSuffix(fqdn, "."))
	if name == "" || !strings.HasSuffix(strings.ToLower(name), ".local") {
		return
	}
	r.setHostname(ip, name, SourceMDNS)
}
