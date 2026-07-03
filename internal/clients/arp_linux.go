package clients

import (
	"bufio"
	"os"
	"strings"
)

// readARPTable parses /proc/net/arp (IPv4 neighbors). IPv6 neighbor
// discovery would need netlink; left out until it earns its keep.
func readARPTable() map[string]string {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()
	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	sc.Scan() // header line
	for sc.Scan() {
		// IP address  HW type  Flags  HW address  Mask  Device
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		ip, flags, mac := fields[0], fields[2], fields[3]
		if flags == "0x0" || mac == "00:00:00:00:00:00" {
			continue // incomplete entry
		}
		out[ip] = strings.ToLower(mac)
	}
	return out
}
