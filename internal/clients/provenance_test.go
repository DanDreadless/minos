package clients

import (
	"testing"
	"time"

	"minos/internal/config"
)

func hostnameOf(r *Registry, ip string) (name, source string) {
	v, ok := r.seen.Load(ip)
	if !ok {
		return "", ""
	}
	if h := v.(*device).hostname.Load(); h != nil {
		return h.name, h.source
	}
	return "", ""
}

// Higher-trust sources replace, equal-trust refreshes, lower-trust never
// downgrades, and anything fills empty.
func TestNamePrecedence(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())
	r.Touch("10.0.0.9", false, time.Now())

	r.setHostname("10.0.0.9", "ptr-name.lan", SourcePTR)
	if n, s := hostnameOf(r, "10.0.0.9"); n != "ptr-name.lan" || s != SourcePTR {
		t.Fatalf("fill-empty = %q/%q", n, s)
	}
	r.setHostname("10.0.0.9", "NBNAME", SourceNetBIOS)
	if n, _ := hostnameOf(r, "10.0.0.9"); n != "NBNAME" {
		t.Errorf("netbios should replace ptr, got %q", n)
	}
	r.setHostname("10.0.0.9", "stale-ptr.lan", SourcePTR)
	if n, s := hostnameOf(r, "10.0.0.9"); n != "NBNAME" || s != SourceNetBIOS {
		t.Errorf("ptr must not downgrade netbios, got %q/%q", n, s)
	}
	r.setHostname("10.0.0.9", "NBNAME2", SourceNetBIOS)
	if n, _ := hostnameOf(r, "10.0.0.9"); n != "NBNAME2" {
		t.Errorf("equal trust should refresh, got %q", n)
	}
	r.setHostname("10.0.0.9", "phone-of-dan", SourceDHCP)
	r.setHostname("10.0.0.9", "mdns-name.local", SourceMDNS)
	if n, s := hostnameOf(r, "10.0.0.9"); n != "phone-of-dan" || s != SourceDHCP {
		t.Errorf("dhcp must survive mdns, got %q/%q", n, s)
	}
	// Empty names never clobber.
	r.setHostname("10.0.0.9", "", SourceDHCP)
	if n, _ := hostnameOf(r, "10.0.0.9"); n != "phone-of-dan" {
		t.Errorf("empty name clobbered, got %q", n)
	}
}

// The same trust order applies to discovered manufacturer/model
// self-descriptions, and the device view prefers them over the OUI vendor.
func TestModelPrecedenceAndView(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	r.ApplyConfig(cfg)
	r.Touch("10.0.0.7", false, time.Now())
	r.setMAC("10.0.0.7", "d6:11:22:33:44:55") // randomized: no OUI vendor

	r.setModel("10.0.0.7", "", "MacBookPro18,3", SourceMDNS)
	r.setModel("10.0.0.7", "Samsung Electronics", "QE55Q80A", SourceSSDP)
	r.setModel("10.0.0.7", "", "stale", SourceMDNS) // must not downgrade

	devs := r.Devices(cfg)
	if len(devs) != 1 {
		t.Fatalf("want 1 device, got %d", len(devs))
	}
	d := devs[0]
	if d.Model != "QE55Q80A" {
		t.Errorf("model = %q, want the SSDP one", d.Model)
	}
	// The device's own claim names a randomized-MAC device the registry
	// never could — and the private flag still rides along.
	if d.Vendor != "Samsung Electronics" || !d.PrivateMAC {
		t.Errorf("vendor/private = %q/%v, want Samsung Electronics/true", d.Vendor, d.PrivateMAC)
	}
}

// Across a device's IPs, the best-trusted name wins the merged row even when
// it isn't the primary (most recently active) address.
func TestMergedRowPrefersTrustedName(t *testing.T) {
	r := NewRegistry()
	cfg := config.Default()
	r.ApplyConfig(cfg)
	now := time.Now()

	r.Touch("192.168.1.50", false, now.Add(-time.Hour)) // older lease
	r.setMAC("192.168.1.50", "aa:bb:cc:dd:ee:ff")
	r.setHostname("192.168.1.50", "phone-of-dan", SourceDHCP)
	r.Touch("192.168.1.77", false, now) // newer lease → primary
	r.setMAC("192.168.1.77", "aa:bb:cc:dd:ee:ff")
	r.setHostname("192.168.1.77", "77.1.168.192.in-addr.arpa-ish", SourcePTR)

	devs := r.Devices(cfg)
	if len(devs) != 1 {
		t.Fatalf("want 1 merged device, got %d", len(devs))
	}
	if devs[0].IP != "192.168.1.77" {
		t.Errorf("primary = %q, want the newer lease", devs[0].IP)
	}
	if devs[0].Hostname != "phone-of-dan" || devs[0].NameSource != SourceDHCP {
		t.Errorf("merged name = %q/%q, want the DHCP name from the older IP",
			devs[0].Hostname, devs[0].NameSource)
	}
}
