package clients

import (
	"strings"

	"minos/internal/oui"
)

// Traffic-pattern OS hints: some devices offer nothing — no PTR, no
// NetBIOS, no mDNS, no UPnP, a randomized MAC, DHCP unseen. But Minos sees
// every DNS query they make, and first-party connectivity checks are
// unmistakable. A curated signature table turns the query log into a
// device-type guess: the fallback that costs nothing, computed on the
// enrichment ticker for unnamed devices only — never on the query path.
//
// Curation rules (mirrors the services catalog): provider-owned,
// unambiguous, first-party check/telemetry hostnames only. Every entry
// says what it identifies. A name browsed by a human (youtube.com) can
// never be a signature; a name only an OS resolves can.
var trafficHints = []struct{ suffix, hint string }{
	// Connectivity checks baked into the OS network stack.
	{"msftconnecttest.com", "Windows PC"},               // Windows NCSI probe
	{"msftncsi.com", "Windows PC"},                      // older Windows NCSI
	{"connectivitycheck.gstatic.com", "Android device"}, // Android captive check
	{"connectivitycheck.android.com", "Android device"},
	{"captive.apple.com", "Apple device"}, // Apple captive check
	// First-party push/update endpoints only the OS itself talks to.
	{"push.apple.com", "Apple device"}, // APNs (couriers live at N-courier.push.apple.com)
	{"mesu.apple.com", "Apple device"}, // Apple software update
	// Consoles.
	{"xboxlive.com", "Xbox"},
	{"playstation.net", "PlayStation"}, // infra domain, not the web store
	{"nintendowifi.net", "Nintendo Switch"},
	// TVs and streamers phone their own clouds.
	{"roku.com", "Roku"},
	{"lgtvsdp.com", "LG TV"}, // LG smart-TV service platform
	{"samsungcloudsolution.com", "Samsung TV"},
	{"amazonalexa.com", "Amazon Echo"},
	{"device-metrics-us.amazon.com", "Amazon device"},
	// IoT vendor clouds (device-only domains, not shopping sites).
	{"tplinkcloud.com", "TP-Link smart device"},
	{"tuyaeu.com", "Tuya smart device"},
	{"tuyaus.com", "Tuya smart device"},
}

// hintFromQNames matches recent query names against the signature table.
// Suffix matches respect label boundaries, so notmsftconnecttest.com can
// never impersonate a signature.
func hintFromQNames(qnames []string) string {
	for _, q := range qnames {
		q = strings.ToLower(q)
		for _, sig := range trafficHints {
			if q == sig.suffix || strings.HasSuffix(q, "."+sig.suffix) {
				return sig.hint
			}
		}
	}
	return ""
}

// SetQNameSource injects the recent-queries reader (the querylog ring) the
// hint sweep uses. Wired in main; call before Run.
func (r *Registry) SetQNameSource(fn func(ip string, n int) []string) { r.qnameSource = fn }

// sweepHints gives at most one still-anonymous device per tick a traffic
// hint: lazy, bounded, and off the hot path. A device with any real
// identity — a name, a self-description, a registry vendor, or an
// existing hint — is skipped; hints are strictly the last resort.
func (r *Registry) sweepHints() {
	if r.qnameSource == nil {
		return
	}
	var target string
	r.seen.Range(func(k, v any) bool {
		d := v.(*device)
		if d.hostname.Load() != nil || d.model.Load() != nil || d.hint.Load() != nil {
			return true
		}
		if m := d.mac.Load(); m != nil && oui.Vendor(*m) != "" {
			return true // the registry already names its maker
		}
		target = k.(string)
		return false // one device per tick
	})
	if target == "" {
		return
	}
	if hint := hintFromQNames(r.qnameSource(target, 200)); hint != "" {
		r.setHint(target, hint)
	}
}
