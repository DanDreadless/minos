package clients

import (
	"context"
	"maps"
	"net"
	"strings"
	"time"
)

const (
	arpRefreshInterval = 60 * time.Second
	ptrTimeout         = 2 * time.Second
	// scheduleTick re-evaluates group schedules so windows open and close
	// within half a minute of their configured times.
	scheduleTick = 30 * time.Second
)

// Run performs background enrichment until ctx ends: reverse-DNS lookups for
// newly seen devices and periodic ARP/neighbor table refreshes for MACs.
// Everything here is best-effort — a home network with no PTR records or an
// off-subnet client simply yields blank fields.
func (r *Registry) Run(ctx context.Context) {
	r.revResolvers = reverseResolvers(defaultGateway())
	r.refreshMACs()
	ticker := time.NewTicker(arpRefreshInterval)
	defer ticker.Stop()
	schedTicker := time.NewTicker(scheduleTick)
	defer schedTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ip := <-r.enrichCh:
			r.enrichOne(ctx, ip)
			r.emitNew(ip) // after enrichment so MAC/hostname ride along
		case <-ticker.C:
			r.refreshMACs()
		case now := <-schedTicker.C:
			if r.hasSchedules() {
				r.rebuildPolicies(now)
			}
		}
	}
}

func (r *Registry) enrichOne(ctx context.Context, ip string) {
	if mac, ok := neighborTable()[ip]; ok {
		r.setMAC(ip, mac)
	}
	if name := r.lookupHostname(ctx, ip); name != "" {
		r.setHostname(ip, name)
	}
}

// neighborTable merges the IPv4 ARP table with the IPv6 neighbour table:
// one IP → MAC map covering both families. The MAC keys the physical-device
// merge, so an IPv6-preferring host collapses into the same row (and
// inherits the same policy) as its IPv4 addresses with no further code.
func neighborTable() map[string]string {
	table := readARPTable()
	v6 := ipv6Neighbors()
	if len(v6) == 0 {
		return table
	}
	if table == nil {
		table = make(map[string]string, len(v6))
	}
	maps.Copy(table, v6)
	return table
}

// lookupHostname reverse-resolves ip, trying each source in turn and taking the
// first non-empty answer: unicast PTR (gateway, then system resolver), then a
// NetBIOS node-status query, then multicast DNS. NetBIOS comes before mDNS
// because it's a cheap unicast that fast-fails on non-Windows hosts and gives
// the canonical machine name for the Windows/Samba boxes mDNS can't see; mDNS
// remains the fallback for Apple/IoT/.local devices and the case where the
// router won't answer PTR.
func (r *Registry) lookupHostname(ctx context.Context, ip string) string {
	for _, res := range r.revResolvers {
		lookupCtx, cancel := context.WithTimeout(ctx, ptrTimeout)
		names, err := res.LookupAddr(lookupCtx, ip)
		cancel()
		if err == nil && len(names) > 0 {
			return strings.TrimSuffix(names[0], ".")
		}
	}
	if name := lookupNetBIOS(ip); name != "" {
		return name
	}
	return lookupMDNS(ip)
}

// reverseResolvers returns the resolvers to try for PTR lookups, in order:
// the LAN gateway (which knows DHCP device names) when one is known, then the
// system resolver as a fallback. Aiming PTR at the gateway avoids looping the
// query back into Minos's own private-reverse backstop, which is why home
// deployments otherwise see blank hostnames.
func reverseResolvers(gateway string) []*net.Resolver {
	var out []*net.Resolver
	if gateway != "" {
		out = append(out, resolverAt(net.JoinHostPort(gateway, "53")))
	}
	return append(out, net.DefaultResolver)
}

// resolverAt builds a Go resolver that sends its queries to addr rather than
// the system-configured servers.
func resolverAt(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: ptrTimeout}
			return d.DialContext(ctx, network, addr)
		},
	}
}

// refreshMACs re-reads the ARP + IPv6 neighbour tables and updates every
// known device.
func (r *Registry) refreshMACs() {
	table := neighborTable()
	if len(table) == 0 {
		return
	}
	r.seen.Range(func(k, _ any) bool {
		ip := k.(string)
		if mac, ok := table[ip]; ok {
			r.setMAC(ip, mac)
		}
		return true
	})
}
