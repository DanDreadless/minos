package clients

import (
	"testing"
	"time"

	"minos/internal/config"
)

// buildDHCPRequest assembles a minimal BOOTREQUEST with the given options.
func buildDHCPRequest(mac []byte, opts []byte) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6 // BOOTREQUEST, ethernet, hlen 6
	copy(b[28:34], mac)
	b[236], b[237], b[238], b[239] = 99, 130, 83, 99 // magic cookie
	return append(b, opts...)
}

var testMAC = []byte{0xaa, 0xbb, 0xcc, 0x00, 0x11, 0x22}

func TestParseDHCPRequest(t *testing.T) {
	cases := []struct {
		name     string
		packet   []byte
		wantOK   bool
		hostname string
		osHint   string
	}{
		{"hostname + msft vendor class",
			buildDHCPRequest(testMAC, []byte{
				12, 10, 'd', 'a', 'n', 's', '-', 'l', 'a', 'p', 't', 'o',
				60, 8, 'M', 'S', 'F', 'T', ' ', '5', '.', '0',
				255,
			}), true, "dans-lapto", "Windows PC"},
		{"android vendor class",
			buildDHCPRequest(testMAC, []byte{
				60, 15, 'a', 'n', 'd', 'r', 'o', 'i', 'd', '-', 'd', 'h', 'c', 'p', '-', '1', '4',
				255,
			}), true, "", "Android device"},
		{"apple option-55 fingerprint",
			buildDHCPRequest(testMAC, []byte{
				12, 6, 'i', 'P', 'h', 'o', 'n', 'e',
				55, 7, 1, 121, 3, 6, 15, 119, 252,
				255,
			}), true, "iPhone", "Apple device"},
		{"pad bytes tolerated",
			buildDHCPRequest(testMAC, []byte{
				0, 0, 12, 2, 't', 'v', 0, 255,
			}), true, "tv", ""},
		{"no identity options", buildDHCPRequest(testMAC, []byte{53, 1, 1, 255}), false, "", ""},
		{"option length past packet", buildDHCPRequest(testMAC, []byte{12, 200, 'x'}), false, "", ""},
		{"control bytes sanitised", buildDHCPRequest(testMAC, []byte{12, 4, 'a', 0x1b, 'b', 'c', 255}), false, "", ""},
		{"not a bootrequest", func() []byte { b := buildDHCPRequest(testMAC, []byte{12, 2, 'x', 'y'}); b[0] = 2; return b }(), false, "", ""},
		{"no magic cookie", func() []byte { b := buildDHCPRequest(testMAC, []byte{12, 2, 'x', 'y'}); b[236] = 0; return b }(), false, "", ""},
		{"truncated header", make([]byte, 100), false, "", ""},
		{"empty", nil, false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := parseDHCPRequest(tc.packet)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (id %+v)", ok, tc.wantOK, id)
			}
			if !ok {
				return
			}
			if id.hostname != tc.hostname || id.osHint != tc.osHint {
				t.Errorf("id = %q/%q, want %q/%q", id.hostname, id.osHint, tc.hostname, tc.osHint)
			}
			if id.mac != "aa:bb:cc:00:11:22" {
				t.Errorf("mac = %q", id.mac)
			}
		})
	}
}

// A DISCOVER precedes any address: the identity parks on the MAC and
// attaches the moment the neighbour table associates it with an IP.
func TestDHCPPendingAttachesOnMACLearn(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())

	id, ok := parseDHCPRequest(buildDHCPRequest(testMAC, []byte{
		12, 5, 'p', 'h', 'o', 'n', 'e',
		55, 7, 1, 121, 3, 6, 15, 119, 252,
		255,
	}))
	if !ok {
		t.Fatal("test packet did not parse")
	}
	r.applyDHCPIdentity(id) // no IP carries the MAC yet → parked

	r.Touch("192.168.1.60", false, time.Now())
	r.setMAC("192.168.1.60", "aa:bb:cc:00:11:22") // association learned → drains

	if n, s := hostnameOf(r, "192.168.1.60"); n != "phone" || s != SourceDHCP {
		t.Errorf("name = %q/%q, want phone/dhcp", n, s)
	}
	devs := r.Devices(config.Default())
	if len(devs) != 1 || devs[0].Hint != "Apple device" {
		t.Errorf("hint = %+v, want Apple device", devs)
	}
}

// A broadcast for an already-associated MAC applies immediately, and the
// DHCP name outranks a PTR one.
func TestDHCPAppliesToKnownMAC(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())
	r.Touch("192.168.1.61", false, time.Now())
	r.setMAC("192.168.1.61", "aa:bb:cc:00:11:22")
	r.setHostname("192.168.1.61", "61.reverse.lan", SourcePTR)

	id, _ := parseDHCPRequest(buildDHCPRequest(testMAC, []byte{12, 5, 'p', 'h', 'o', 'n', 'e', 255}))
	r.applyDHCPIdentity(id)
	if n, s := hostnameOf(r, "192.168.1.61"); n != "phone" || s != SourceDHCP {
		t.Errorf("name = %q/%q, want phone/dhcp (beats ptr)", n, s)
	}
}

// A stale parked introduction never attaches.
func TestDHCPPendingExpires(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())
	r.pendingDHCP.Store("aa:bb:cc:00:11:22", dhcpIdentity{
		mac: "aa:bb:cc:00:11:22", hostname: "old-name", at: time.Now().Add(-2 * time.Hour),
	})
	r.Touch("192.168.1.62", false, time.Now())
	r.setMAC("192.168.1.62", "aa:bb:cc:00:11:22")
	if n, _ := hostnameOf(r, "192.168.1.62"); n != "" {
		t.Errorf("expired pending identity attached: %q", n)
	}
	if _, still := r.pendingDHCP.Load("aa:bb:cc:00:11:22"); still {
		t.Error("expired entry not cleaned up")
	}
}
