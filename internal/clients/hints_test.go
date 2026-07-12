package clients

import (
	"testing"
	"time"

	"minos/internal/config"
)

func TestHintFromQNames(t *testing.T) {
	cases := []struct {
		name   string
		qnames []string
		want   string
	}{
		{"windows ncsi", []string{"github.com", "www.msftconnecttest.com"}, "Windows PC"},
		{"exact suffix", []string{"msftconnecttest.com"}, "Windows PC"},
		{"apple push", []string{"1-courier.push.apple.com"}, "Apple device"},
		{"console", []string{"dp.dl.playstation.net"}, "PlayStation"},
		{"label boundary holds", []string{"notmsftconnecttest.com", "evilxboxlive.com"}, ""},
		{"case insensitive", []string{"Connectivitycheck.Gstatic.COM"}, "Android device"},
		{"nothing matches", []string{"example.com", "github.com"}, ""},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hintFromQNames(tc.qnames); got != tc.want {
				t.Errorf("hintFromQNames = %q, want %q", got, tc.want)
			}
		})
	}
}

// The sweep hints exactly the devices nothing else names, one per tick,
// and never touches a device with any real identity.
func TestSweepHints(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())
	queries := map[string][]string{
		"10.0.0.1": {"connectivitycheck.gstatic.com", "example.com"},
		"10.0.0.2": {"www.msftconnecttest.com"},
	}
	r.SetQNameSource(func(ip string, n int) []string { return queries[ip] })

	r.Touch("10.0.0.1", false, time.Now()) // anonymous → eligible
	r.Touch("10.0.0.2", false, time.Now()) // named → skipped
	r.setHostname("10.0.0.2", "desktop.lan", SourcePTR)

	r.sweepHints() // one per tick: only the anonymous device is eligible
	devs := r.Devices(config.Default())
	byIP := map[string]Device{}
	for _, d := range devs {
		byIP[d.IP] = d
	}
	if byIP["10.0.0.1"].Hint != "Android device" {
		t.Errorf("anonymous device hint = %q, want Android device", byIP["10.0.0.1"].Hint)
	}
	if byIP["10.0.0.2"].Hint != "" {
		t.Errorf("named device got a hint: %q", byIP["10.0.0.2"].Hint)
	}

	// Once hinted, the device is no longer eligible — the sweep moves on
	// (and with nothing left to hint, does nothing).
	r.sweepHints()
	if got := r.Devices(config.Default()); len(got) != 2 {
		t.Fatalf("device rows changed: %d", len(got))
	}

	// A device whose MAC the registry names is never hinted either.
	r.Touch("10.0.0.3", false, time.Now())
	r.setMAC("10.0.0.3", "28:cd:c1:00:00:01") // Raspberry Pi OUI
	queries["10.0.0.3"] = []string{"www.msftconnecttest.com"}
	r.sweepHints()
	for _, d := range r.Devices(config.Default()) {
		if d.IP == "10.0.0.3" && d.Hint != "" {
			t.Errorf("OUI-named device got a hint: %q", d.Hint)
		}
	}
}
