package clients

import "strings"

// sanitizeDiscoveredName vets a device-supplied string (mDNS name or TXT
// value, SSDP field, DHCP hostname) before it enters the device table:
// trimmed, printable ASCII only, capped at 64 bytes. Everything a device
// sends is attacker-controllable; a hostile value yields "" rather than
// control characters in the UI or logs. NetBIOS keeps its own field-level
// variant (fixed-width padding rules).
func sanitizeDiscoveredName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7E {
			return ""
		}
	}
	return s
}
