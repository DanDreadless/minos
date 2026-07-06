package clients

import "os"

// defaultGateway returns the IPv4 default-route gateway (the LAN router), or
// "" if none can be determined. Reverse-DNS enrichment aims PTR queries here:
// the router knows the DHCP-assigned device names, whereas asking Minos
// itself hits the RFC 6303 private-reverse backstop and returns NXDOMAIN.
func defaultGateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	return parseProcNetRoute(f)
}
