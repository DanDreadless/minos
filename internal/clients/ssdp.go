package clients

import (
	"bufio"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SSDP/UPnP discovery — the smart-TV namer. One multicast M-SEARCH per
// sweep, then one strictly-policed description fetch per responding device,
// yields friendlyName ("Living Room TV"), manufacturer, and modelName for
// exactly the devices (TVs, consoles, IoT) that answer nothing else.
// Everything here runs on the discovery ticker, never the query path, and
// is switchable via discovery.ssdp (default on).

const (
	ssdpInterval   = 5 * time.Minute
	ssdpListenFor  = 3 * time.Second
	ssdpFetchLimit = 64 << 10 // description XML cap; it is attacker-sized
	ssdpCacheTTL   = time.Hour
)

var ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}

const ssdpSearch = "M-SEARCH * HTTP/1.1\r\n" +
	"HOST: 239.255.255.250:1900\r\n" +
	"MAN: \"ssdp:discover\"\r\n" +
	"MX: 2\r\n" +
	"ST: upnp:rootdevice\r\n\r\n"

// ssdpFetched caps re-fetching on a chatty network: a device's description
// is fetched at most once per ssdpCacheTTL. Discovery-goroutine-only state,
// but a mutex keeps it honest.
var (
	ssdpMu      sync.Mutex
	ssdpFetched = map[string]time.Time{}
)

// deviceDescription is the slice of the UPnP device XML we care about.
type deviceDescription struct {
	FriendlyName string `xml:"device>friendlyName"`
	Manufacturer string `xml:"device>manufacturer"`
	ModelName    string `xml:"device>modelName"`
}

// discoverSSDP runs one search sweep: M-SEARCH out of every eligible
// interface, then a description fetch per fresh responder, feeding the
// registry under ssdp provenance. setHostname/setModel only store for
// devices that have queried Minos, so a chatty UPnP box that never uses
// this resolver creates no ghost rows.
func (r *Registry) discoverSSDP() {
	for _, src := range multicastSourceIPs() {
		conn, err := net.ListenPacket("udp4", net.JoinHostPort(src, "0"))
		if err != nil {
			continue
		}
		if _, err := conn.WriteTo([]byte(ssdpSearch), ssdpGroup); err != nil {
			_ = conn.Close()
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(ssdpListenFor))
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				break // deadline
			}
			udp, ok := from.(*net.UDPAddr)
			if !ok {
				continue
			}
			ip := udp.IP.String()
			location, ok := parseSSDPResponse(buf[:n])
			if !ok || !ssdpShouldFetch(ip) {
				continue
			}
			if desc, ok := fetchDeviceDescription(location, ip); ok {
				r.applySSDP(ip, desc)
			}
		}
		_ = conn.Close()
	}
}

func (r *Registry) applySSDP(ip string, d deviceDescription) {
	if name := sanitizeDiscoveredName(d.FriendlyName); name != "" {
		r.setHostname(ip, name, SourceSSDP)
	}
	manufacturer := sanitizeDiscoveredName(d.Manufacturer)
	model := sanitizeDiscoveredName(d.ModelName)
	if manufacturer != "" || model != "" {
		r.setModel(ip, manufacturer, model, SourceSSDP)
	}
}

// ssdpShouldFetch rate-limits description fetches per device IP.
func ssdpShouldFetch(ip string) bool {
	ssdpMu.Lock()
	defer ssdpMu.Unlock()
	if t, ok := ssdpFetched[ip]; ok && time.Since(t) < ssdpCacheTTL {
		return false
	}
	ssdpFetched[ip] = time.Now()
	return true
}

// parseSSDPResponse pulls the LOCATION header out of an M-SEARCH response.
// The packet is attacker-controllable; anything malformed yields ok=false.
func parseSSDPResponse(b []byte) (location string, ok bool) {
	rd := bufio.NewReader(strings.NewReader(string(b)))
	status, err := rd.ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 200") {
		return "", false
	}
	// textproto-style header read, size already bounded by the UDP buffer.
	for {
		line, err := rd.ReadString('\n')
		if err != nil || line == "\r\n" || line == "\n" {
			break
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), "location") {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// fetchDeviceDescription GETs the LOCATION URL under a strict policy: the
// URL must point at the responding device itself (a response pointing
// anywhere else is classic SSRF bait and is dropped), plain http, no
// redirects, 5 s, 64 KB.
func fetchDeviceDescription(location, responderIP string) (deviceDescription, bool) {
	var desc deviceDescription
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "http" || u.Hostname() != responderIP {
		return desc, false
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // never follow
		},
	}
	resp, err := client.Get(u.String())
	if err != nil {
		return desc, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return desc, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, ssdpFetchLimit))
	if err != nil {
		return desc, false
	}
	if xml.Unmarshal(body, &desc) != nil {
		return desc, false
	}
	return desc, desc.FriendlyName != "" || desc.Manufacturer != "" || desc.ModelName != ""
}
