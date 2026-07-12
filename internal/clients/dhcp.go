package clients

import (
	"strings"
	"time"
)

// Passive DHCP identity: DISCOVER/REQUEST broadcasts are where devices
// introduce themselves — hostname (option 12), vendor class (option 60),
// and the option-55 parameter list that fingerprints the OS. Pi-hole sees
// this by being the DHCP server; Minos sees the broadcast half without
// serving a single lease. This file is the portable parsing/plumbing; the
// listener socket lives in dhcp_linux.go. Explicitly a listener, never a
// server: nothing is transmitted, allocated, or answered.

// dhcpIdentity is what one broadcast reveals about its sender.
type dhcpIdentity struct {
	mac      string
	hostname string
	osHint   string
	at       time.Time
}

// pendingDHCPTTL bounds how long a lease-time introduction waits for the
// neighbour table to associate the MAC with an IP.
const pendingDHCPTTL = time.Hour

// parseDHCPRequest extracts identity from a BOOTREQUEST, or ok=false for
// anything else. The packet arrives off the wire from arbitrary hosts:
// every offset is bounds-checked, the options walk is capped, and strings
// are sanitised — a hostile packet yields ok=false, never a panic.
func parseDHCPRequest(b []byte) (dhcpIdentity, bool) {
	var id dhcpIdentity
	const (
		headerLen  = 240 // BOOTP fixed header + magic cookie
		optsBudget = 1024
	)
	if len(b) < headerLen {
		return id, false
	}
	// op=BOOTREQUEST, htype=ethernet, hlen=6.
	if b[0] != 1 || b[1] != 1 || b[2] != 6 {
		return id, false
	}
	// Magic cookie 99.130.83.99 ends the fixed header.
	if b[236] != 99 || b[237] != 130 || b[238] != 83 || b[239] != 99 {
		return id, false
	}
	id.mac = NormalizeMAC(macString(b[28:34]))

	opts := b[headerLen:]
	if len(opts) > optsBudget {
		opts = opts[:optsBudget]
	}
	var paramList []byte
	for i := 0; i < len(opts); {
		code := opts[i]
		switch code {
		case 0: // pad
			i++
			continue
		case 255: // end
			i = len(opts)
			continue
		}
		if i+1 >= len(opts) {
			break // truncated option header
		}
		l := int(opts[i+1])
		if i+2+l > len(opts) {
			break // length runs past the packet
		}
		val := opts[i+2 : i+2+l]
		switch code {
		case 12: // hostname
			id.hostname = sanitizeDiscoveredName(string(val))
		case 60: // vendor class identifier
			id.osHint = osHintFromVendorClass(string(val))
		case 55: // parameter request list (OS fingerprint)
			paramList = val
		}
		i += 2 + l
	}
	if id.osHint == "" {
		id.osHint = osHintFromParamList(paramList)
	}
	id.at = time.Now()
	return id, id.mac != "" && (id.hostname != "" || id.osHint != "")
}

func macString(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 17)
	for i, x := range b {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexdigits[x>>4], hexdigits[x&0xf])
	}
	return string(out)
}

// osHintFromVendorClass maps well-known option-60 vendor class prefixes to
// a coarse OS hint. Deliberately tiny and provider-published: "MSFT 5.0"
// is Windows' documented class, "android-dhcp-<ver>" is Android's, and
// "dhcpcd" is the BSD/Linux client many IoT distros ship. Ambiguous
// classes (udhcp — every busybox gadget) map to nothing.
func osHintFromVendorClass(v string) string {
	v = strings.ToLower(sanitizeDiscoveredName(v))
	switch {
	case strings.HasPrefix(v, "msft"):
		return "Windows PC"
	case strings.HasPrefix(v, "android-dhcp"):
		return "Android device"
	case strings.HasPrefix(v, "dhcpcd"):
		return "Linux/BSD device"
	}
	return ""
}

// osHintFromParamList recognises the classic Apple option-55 fingerprint:
// macOS and iOS request exactly 1,121,3,6,15,119,252 (subnet, classless
// routes, router, DNS, domain, search domains, proxy autodiscovery) — the
// long-documented signature DHCP fingerprint databases key on. Apple sends
// no vendor class, so this is the one whiff of identity its clients give.
func osHintFromParamList(params []byte) string {
	apple := []byte{1, 121, 3, 6, 15, 119, 252}
	if len(params) == len(apple) {
		match := true
		for i := range apple {
			if params[i] != apple[i] {
				match = false
				break
			}
		}
		if match {
			return "Apple device"
		}
	}
	return ""
}

// applyDHCPIdentity attaches a broadcast's identity to the device carrying
// its MAC. A DISCOVER has no address yet, so when no live IP carries the
// MAC the identity waits (bounded) for the neighbour table to make the
// association — setMAC drains the pending entry.
func (r *Registry) applyDHCPIdentity(id dhcpIdentity) {
	ips := r.IPsForMAC(id.mac)
	if len(ips) == 0 {
		r.pendingDHCP.Store(id.mac, id)
		return
	}
	for _, ip := range ips {
		r.applyDHCPToIP(ip, id)
	}
}

func (r *Registry) applyDHCPToIP(ip string, id dhcpIdentity) {
	if id.hostname != "" {
		r.setHostname(ip, id.hostname, SourceDHCP)
	}
	if id.osHint != "" {
		r.setHint(ip, id.osHint)
	}
}

// drainPendingDHCP applies a waiting lease-time introduction once mac↔ip
// is known. Called from setMAC (enrichment worker only).
func (r *Registry) drainPendingDHCP(ip, mac string) {
	v, ok := r.pendingDHCP.Load(mac)
	if !ok {
		return
	}
	id := v.(dhcpIdentity)
	if time.Since(id.at) > pendingDHCPTTL {
		r.pendingDHCP.Delete(mac)
		return
	}
	r.applyDHCPToIP(ip, id)
}
