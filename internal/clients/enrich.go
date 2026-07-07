package clients

import (
	"context"
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
	if mac, ok := readARPTable()[ip]; ok {
		r.setMAC(ip, mac)
	}
	if name := r.lookupHostname(ctx, ip); name != "" {
		r.setHostname(ip, name)
	}
}

// lookupHostname reverse-resolves ip, trying each source in turn and taking
// the first non-empty answer: unicast PTR (gateway, then system resolver),
// then multicast DNS. mDNS is last because it is slower and only some devices
// answer — but it is the one source that works when the router won't do PTR.
func (r *Registry) lookupHostname(ctx context.Context, ip string) string {
	for _, res := range r.revResolvers {
		lookupCtx, cancel := context.WithTimeout(ctx, ptrTimeout)
		names, err := res.LookupAddr(lookupCtx, ip)
		cancel()
		if err == nil && len(names) > 0 {
			return strings.TrimSuffix(names[0], ".")
		}
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

// refreshMACs re-reads the ARP/neighbor table and updates every known device.
func (r *Registry) refreshMACs() {
	table := readARPTable()
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
