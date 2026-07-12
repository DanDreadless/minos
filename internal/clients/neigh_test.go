package clients

import (
	"testing"
	"time"

	"minos/internal/config"
)

func TestParseNeighOutput(t *testing.T) {
	out := "fe80::1c2d:3e4f dev eth0 lladdr aa:bb:cc:dd:ee:ff router REACHABLE\n" +
		"2001:db8::5 dev eth0 lladdr AA-BB-CC-DD-EE-01 STALE\r\n" + // odd notation + CRLF
		"2001:db8::6 dev wlan0 lladdr aa:bb:cc:dd:ee:02 DELAY\n" +
		"2001:db8::7 dev wlan0 lladdr aa:bb:cc:dd:ee:03 PROBE\n" +
		"2001:db8::8 dev eth0 lladdr aa:bb:cc:dd:ee:04 PERMANENT\n" +
		"2001:db8::9 dev eth0 INCOMPLETE\n" + // no lladdr, dead state
		"2001:db8::a dev eth0 lladdr aa:bb:cc:dd:ee:05 FAILED\n" + // dead state
		"2001:db8::b dev eth0 lladdr not-a-mac REACHABLE\n" + // junk MAC
		"garbage line\n" +
		"\n"
	got := parseNeighOutput(out)
	want := map[string]string{
		"fe80::1c2d:3e4f": "aa:bb:cc:dd:ee:ff",
		"2001:db8::5":     "aa:bb:cc:dd:ee:01", // canonicalised
		"2001:db8::6":     "aa:bb:cc:dd:ee:02",
		"2001:db8::7":     "aa:bb:cc:dd:ee:03",
		"2001:db8::8":     "aa:bb:cc:dd:ee:04",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %#v", len(got), len(want), got)
	}
	for ip, mac := range want {
		if got[ip] != mac {
			t.Errorf("entry %s = %q, want %q", ip, got[ip], mac)
		}
	}
}

// An IPv6-only address whose neighbour entry carries a known MAC merges into
// the same physical-device row as the device's IPv4 traffic — and inherits
// its MAC-keyed policy after the rebuild setMAC triggers.
func TestIPv6NeighborMergesDevice(t *testing.T) {
	prev := ipv6Neighbors
	defer func() { ipv6Neighbors = prev }()
	ipv6Neighbors = func() map[string]string {
		return map[string]string{"fe80::dead:beef": "aa:bb:cc:00:11:22"}
	}

	cfg := config.Default()
	cfg.Clients = []config.Client{{
		IP: "192.168.1.55", MAC: "aa:bb:cc:00:11:22", Blocked: true,
	}}
	r := NewRegistry()
	r.ApplyConfig(cfg)

	now := time.Now()
	r.Touch("192.168.1.55", false, now.Add(-time.Minute))
	r.setMAC("192.168.1.55", "aa:bb:cc:00:11:22")

	// The device starts preferring IPv6: a new address appears, then the
	// neighbour sweep tags it.
	r.Touch("fe80::dead:beef", false, now)
	if p := r.PolicyFor("fe80::dead:beef"); p != nil {
		t.Fatalf("pre-sweep policy = %+v, want nil (MAC not yet learned)", p)
	}
	r.refreshMACs()

	devs := r.Devices(cfg)
	if len(devs) != 1 {
		t.Fatalf("got %d device rows, want 1 merged across families: %+v", len(devs), devs)
	}
	if len(devs[0].IPs) != 2 {
		t.Errorf("merged ips = %v, want both the IPv4 and IPv6 addresses", devs[0].IPs)
	}
	// The MAC-keyed device block now covers the IPv6 address too.
	if p := r.PolicyFor("fe80::dead:beef"); !p.Refuses() {
		t.Errorf("post-sweep policy = %+v, want refuse (inherited via MAC)", p)
	}
}
