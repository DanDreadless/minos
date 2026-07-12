package clients

import "strings"

// ipv6Neighbors returns the kernel's IPv6 neighbour table as IP → canonical
// MAC. Package var so the Registry tests can inject a fake table; the real
// implementation is chosen per-OS at build time (Linux execs iproute2 —
// there is no /proc equivalent for the IPv6 neighbour table, and raw
// netlink isn't worth hand-rolling under the no-new-deps rule).
var ipv6Neighbors = readIPv6Neighbors

// parseNeighOutput parses `ip -6 neigh show` lines, e.g.
//
//	fe80::1c2d:3e4f dev eth0 lladdr aa:bb:cc:dd:ee:ff router REACHABLE
//	2001:db8::5 dev eth0 lladdr aa:bb:cc:dd:ee:ff STALE
//	2001:db8::9 dev eth0 INCOMPLETE
//
// keeping entries whose state suggests a live (or recently live)
// association. Link-local addresses are wanted: they carry the MAC that
// merges an IPv6 client into its physical-device row.
func parseNeighOutput(out string) map[string]string {
	table := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimRight(line, "\r"))
		if len(fields) < 2 {
			continue
		}
		ip := fields[0]
		var mac string
		usable := false
		for i, f := range fields[1:] {
			switch f {
			case "lladdr":
				// The MAC follows the lladdr keyword.
				if i+2 < len(fields) {
					mac = fields[i+2]
				}
			case "REACHABLE", "STALE", "DELAY", "PROBE", "PERMANENT":
				usable = true
			case "FAILED", "INCOMPLETE":
				usable = false
			}
		}
		if !usable || mac == "" {
			continue
		}
		if norm := NormalizeMAC(mac); norm != "" && strings.Count(norm, ":") == 5 {
			table[ip] = norm
		}
	}
	return table
}
