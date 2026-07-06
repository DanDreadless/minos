package clients

import (
	"bufio"
	"encoding/hex"
	"io"
	"net"
	"strings"
)

// parseProcNetRoute extracts the IPv4 default-route gateway from the contents
// of /proc/net/route, or "" if there is none. The Gateway column is a
// little-endian hex word in host byte order, exactly as the kernel writes it
// (so "0101A8C0" is 192.168.1.1). Kept OS-independent so it can be tested
// anywhere; the file read lives in gateway_linux.go.
func parseProcNetRoute(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Scan() // header row
	for sc.Scan() {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask MTU ...
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[1] != "00000000" {
			continue // only the default route (destination 0.0.0.0)
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		ip := net.IPv4(b[3], b[2], b[1], b[0]) // little-endian → dotted quad
		if ip.Equal(net.IPv4zero) {
			continue
		}
		return ip.String()
	}
	return ""
}
