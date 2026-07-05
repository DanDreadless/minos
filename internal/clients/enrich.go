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
	lookupCtx, cancel := context.WithTimeout(ctx, ptrTimeout)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(lookupCtx, ip)
	if err == nil && len(names) > 0 {
		r.setHostname(ip, strings.TrimSuffix(names[0], "."))
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
