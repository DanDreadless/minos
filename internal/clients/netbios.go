package clients

import (
	"encoding/binary"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const (
	netbiosTimeout = 500 * time.Millisecond
	// nbstatType is the NBSTAT (node-status) query type; nbClassIN is the
	// NetBIOS internet class. RFC 1002 §4.2.
	nbstatType = 0x0021
	nbClassIN  = 0x0001
)

// lookupNetBIOS resolves ip to a hostname via a NetBIOS node-status (NBSTAT)
// query to UDP 137 — the machine's own name table, read straight off the
// device. It fills the gap left by mDNS: Windows / Samba hosts typically run
// no mDNS responder, and a router that NXDOMAINs private PTR gives nothing
// either. IPv4 only, best-effort, off the hot path — returns "" on any failure
// or timeout. Used as the hostname source between unicast PTR and mDNS.
//
// The socket is connected (net.Dial), so a host with no NBNS listener replies
// ICMP port-unreachable and Read fails immediately rather than waiting out the
// deadline — a fast miss for the common non-Windows device.
func lookupNetBIOS(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return "" // NBNS is IPv4 only (also matches the ARP/mDNS limitation)
	}
	conn, err := net.Dial("udp", net.JoinHostPort(ip, "137"))
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(buildNBStatQuery()); err != nil {
		return ""
	}
	_ = conn.SetReadDeadline(time.Now().Add(netbiosTimeout))
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return "" // deadline, or ICMP port-unreachable on a host with no NBNS
	}
	return parseNBStatResponse(buf[:n])
}

// buildNBStatQuery packs a 50-byte NBNS node-status request for the wildcard
// name "*": a 12-byte header (random transaction ID, one question) followed by
// the length-prefixed encoded name, NBSTAT type, and IN class.
func buildNBStatQuery() []byte {
	name := encodeNetBIOSName("*")
	buf := make([]byte, 0, 12+1+len(name)+1+4)
	var hdr [12]byte
	binary.BigEndian.PutUint16(hdr[0:2], dns.Id()) // transaction ID
	// flags left 0x0000: standard query, opcode 0, unicast (no broadcast).
	binary.BigEndian.PutUint16(hdr[4:6], 1) // QDCOUNT = 1
	buf = append(buf, hdr[:]...)
	buf = append(buf, byte(len(name))) // label length prefix (0x20)
	buf = append(buf, name...)         // 32 encoded bytes
	buf = append(buf, 0x00)            // name terminator (empty scope)
	buf = binary.BigEndian.AppendUint16(buf, nbstatType)
	buf = binary.BigEndian.AppendUint16(buf, nbClassIN)
	return buf
}

// encodeNetBIOSName applies RFC 1001 first-level encoding to the wildcard
// node-status name: the 16-byte NetBIOS name ("*" right-padded with NULs, the
// 16th byte being the 0x00 suffix) is expanded so each byte becomes two ASCII
// letters — its high and low nibbles each added to 'A'. "*" → "CKAAAA…" (32
// bytes). Only ever used for the "*" name, which is NUL-padded (unlike a real
// name, which pads with spaces).
func encodeNetBIOSName(name string) []byte {
	var raw [16]byte
	copy(raw[:], name)
	out := make([]byte, 0, 32)
	for _, b := range raw {
		out = append(out, 'A'+(b>>4), 'A'+(b&0x0F))
	}
	return out
}

// parseNBStatResponse reads a NBNS node-status reply and returns the device's
// machine name: the unique (GROUP flag clear) entry whose suffix is 0x00, the
// Workstation service. Returns "" on any malformation — the response is
// attacker-controllable, so every field is bounds-checked before use and the
// name is sanitised, and a hostile packet yields "" rather than a panic.
func parseNBStatResponse(b []byte) string {
	const headerLen = 12
	if len(b) < headerLen {
		return ""
	}
	off, ok := skipNBName(b, headerLen)
	if !ok {
		return ""
	}
	// TYPE(2) + CLASS(2) + TTL(4) + RDLENGTH(2) = 10 bytes before RDATA.
	if off+10 > len(b) {
		return ""
	}
	off += 10
	if off >= len(b) {
		return ""
	}
	numNames := int(b[off])
	off++
	const entryLen = 18 // 15-byte name + 1-byte suffix + 2-byte flags
	if numNames <= 0 || off+numNames*entryLen > len(b) {
		return ""
	}
	for i := 0; i < numNames; i++ {
		entry := b[off : off+entryLen]
		off += entryLen
		suffix := entry[15]
		flags := binary.BigEndian.Uint16(entry[16:18])
		const groupFlag = 0x8000
		if suffix != 0x00 || flags&groupFlag != 0 {
			continue // want only the unique Workstation-service name
		}
		if name := sanitizeNetBIOSName(entry[:15]); name != "" {
			return name
		}
	}
	return ""
}

// skipNBName advances past the NetBIOS name at off, returning the offset just
// after it. Handles both a length-prefixed label (the 0x20-length encoded name
// plus its 0x00 terminator) and a compression pointer (RFC 1002 §4.2.1.2).
func skipNBName(b []byte, off int) (int, bool) {
	for off < len(b) {
		l := int(b[off])
		switch {
		case l == 0:
			return off + 1, true
		case l&0xC0 == 0xC0: // compression pointer: two bytes
			if off+2 > len(b) {
				return 0, false
			}
			return off + 2, true
		default:
			off += 1 + l
		}
	}
	return 0, false
}

// sanitizeNetBIOSName trims the space/NUL padding from a 15-byte NetBIOS name
// field and rejects anything containing a non-printable byte, so a hostile
// responder can't inject control characters into the device table or logs.
// Returns "" when nothing usable remains.
func sanitizeNetBIOSName(field []byte) string {
	name := strings.TrimRight(string(field), " \x00")
	if name == "" {
		return ""
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] > 0x7E {
			return ""
		}
	}
	return name
}
