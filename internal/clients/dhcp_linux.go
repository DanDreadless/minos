package clients

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// dhcpBindRecheck is how often the listener reconciles its socket with the
// discovery.dhcp_listen setting — bind when enabled, release the port when
// disabled, so the toggle applies live and never holds :67 hostage from a
// DHCP server the user later runs on this host.
const dhcpBindRecheck = time.Minute

var dhcpBindFailed sync.Once

// listenDHCP passively reads DHCP client broadcasts on UDP :67 until ctx
// ends. On the standard deployment the router serves DHCP, so the port is
// free here; if something local owns it (the user runs a DHCP server on
// this box), the bind fails, we log once at Info, and retry occasionally —
// never fighting for the port. CAP_NET_BIND_SERVICE from the systemd unit
// covers port 67 like it covers 53.
func (r *Registry) listenDHCP(ctx context.Context) {
	var conn *net.UDPConn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()
	ticker := time.NewTicker(dhcpBindRecheck)
	defer ticker.Stop()
	for {
		enabled := false
		if cfg := r.cfg.Load(); cfg != nil {
			enabled = cfg.Discovery.DHCPListen
		}
		switch {
		case enabled && conn == nil:
			c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 67})
			if err != nil {
				dhcpBindFailed.Do(func() {
					slog.Info("dhcp listener: port 67 unavailable; lease-time device names disabled",
						"err", err)
				})
			} else {
				conn = c
				go r.readDHCP(conn)
			}
		case !enabled && conn != nil:
			_ = conn.Close() // ends the read loop; port released
			conn = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// readDHCP consumes broadcasts until the socket closes. Read-only: nothing
// is ever transmitted, allocated, or answered — a listener, not a server.
func (r *Registry) readDHCP(conn *net.UDPConn) {
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // closed (shutdown or toggle-off)
		}
		if id, ok := parseDHCPRequest(buf[:n]); ok {
			r.applyDHCPIdentity(id)
		}
	}
}
