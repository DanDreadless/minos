package threats

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"minos/internal/querylog"
)

func entry(client, name string, at time.Time) querylog.Entry {
	return querylog.Entry{Time: at, Client: client, QName: name, QType: "A", Verdict: querylog.VerdictAllowed}
}

type collector struct {
	mu  sync.Mutex
	got []Finding
}

func (c *collector) add(f Finding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, f)
}

func (c *collector) wait(t *testing.T, n int) []Finding {
	t.Helper()
	for range 200 {
		c.mu.Lock()
		if len(c.got) >= n {
			out := append([]Finding(nil), c.got...)
			c.mu.Unlock()
			return out
		}
		c.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited for %d findings", n)
	return nil
}

func TestDGA(t *testing.T) {
	var c collector
	d := New(Options{OnNew: c.add})
	now := time.Now()
	// A normal browsing mix — including odd-looking infrastructure — is not a finding.
	for i, n := range benign {
		d.Observe(entry("laptop", "www."+n, now.Add(time.Duration(i)*time.Second)))
	}
	if len(d.Findings()) != 0 {
		t.Fatalf("normal traffic produced findings: %+v", d.Findings())
	}
	// Generated names, one after another.
	gen := []string{"qnrbmqbvjfx", "kqlxtqptsmys", "pxhbfogkrmplf", "kvxhvklvrlwrbi", "tpgulxbofqfx",
		"ndqoxlfxugzk", "xjwqmtrvbnkl", "zkqwpxmvtrbl", "bqzxkvwmptrl", "mxqzvbkwtrpl", "wvqzxkbmptrl",
		"pqzxvbkmwtrl", "rqzxvbkmwtpl"}
	for i, l := range gen {
		d.Observe(entry("doorbell", l+".com", now.Add(time.Duration(i)*10*time.Second)))
	}
	got := c.wait(t, 1)
	if got[0].Kind != KindDGA || got[0].Client != "doorbell" || got[0].Count < DGAThreshold || len(got[0].Samples) != sampleLimit {
		t.Fatalf("finding %+v", got[0])
	}
	// The same names spread over hours are not a burst.
	d2 := New(Options{})
	for i, l := range gen {
		d2.Observe(entry("pc", l+".com", now.Add(time.Duration(i)*time.Hour)))
	}
	if len(d2.Findings()) != 0 {
		t.Error("generated names hours apart produced a finding")
	}
}

func TestTunnel(t *testing.T) {
	var c collector
	d := New(Options{OnNew: c.add})
	now := time.Now()
	for i := range TunnelThreshold {
		h := sha256.Sum256([]byte{byte(i)})
		name := fmt.Sprintf("%x.%x.exfil.example.net", h[:16], h[16:])
		d.Observe(entry("nas", name, now.Add(time.Duration(i)*time.Second)))
	}
	got := c.wait(t, 1)
	if got[0].Kind != KindTunnel || got[0].Domain != "example.net" {
		t.Fatalf("finding %+v", got[0])
	}
	// Antivirus hash lookups look the same and are expected.
	d2 := New(Options{})
	for i := range TunnelThreshold * 2 {
		h := sha256.Sum256([]byte{byte(i)})
		d2.Observe(entry("pc", fmt.Sprintf("%x.%x.sophosxl.net", h[:16], h[16:]), now.Add(time.Duration(i)*time.Second)))
	}
	if len(d2.Findings()) != 0 {
		t.Error("antivirus lookups flagged as tunnelling")
	}
	// And so is anything the user marks as fine.
	d3 := New(Options{Ignore: func() []string { return []string{"example.net"} }})
	for i := range TunnelThreshold {
		h := sha256.Sum256([]byte{byte(i)})
		d3.Observe(entry("nas", fmt.Sprintf("%x.%x.exfil.example.net", h[:16], h[16:]), now))
	}
	if len(d3.Findings()) != 0 {
		t.Error("an ignored domain was flagged")
	}
}

func TestKnownThreat(t *testing.T) {
	var c collector
	d := New(Options{OnNew: c.add, ThreatList: func(l string) bool { return ThreatFeed(l, "") }})
	now := time.Now()
	e := entry("tv", "evil.example.com", now)
	e.Verdict, e.List = querylog.VerdictBlocked, "URLhaus (malware-filter)"
	d.Observe(e)
	e.Time = now.Add(time.Second)
	d.Observe(e)
	ad := entry("tv", "ads.example.com", now)
	ad.Verdict, ad.List = querylog.VerdictBlocked, "StevenBlack"
	d.Observe(ad)
	other := entry("tv", "phish.example.org", now.Add(2*time.Second))
	other.Verdict, other.List = querylog.VerdictBlocked, "Hagezi TIF"
	d.Observe(other)
	got := c.wait(t, 1)
	time.Sleep(20 * time.Millisecond)
	fs := d.Findings()
	// One accumulating finding per device, one alert.
	if len(fs) != 1 || got[0].Kind != KindKnown || fs[0].Count != 3 || fs[0].Distinct != 2 ||
		fs[0].Domain != "evil.example.com" || fs[0].Samples[0] != "phish.example.org" {
		t.Fatalf("findings %+v", fs)
	}
	if len(c.got) != 1 {
		t.Errorf("%d alerts for one device's incident", len(c.got))
	}
	// Dismissed: quiet, even when it happens again.
	if !d.Dismiss(KindKnown, "tv", "") {
		t.Error("dismiss found nothing")
	}
	e.Time = now.Add(time.Minute)
	d.Observe(e)
	if len(d.Findings()) != 0 {
		t.Error("a dismissed finding came straight back")
	}
}

func TestThreatFeed(t *testing.T) {
	for name, want := range map[string]bool{
		"Hagezi TIF": true, "URLhaus (malware-filter)": true, "Phishing Army": true,
		"StevenBlack": false, "OISD Big": false, "Hagezi Pro": false,
	} {
		if got := ThreatFeed(name, ""); got != want {
			t.Errorf("ThreatFeed(%q) = %v", name, got)
		}
	}
	if !ThreatFeed("My list", "https://example.com/phishing.txt") {
		t.Error("URL not considered")
	}
	if strings.Contains(strings.Join(builtinIgnore, " "), "google") {
		t.Error("the built-in ignore list must stay security vendors only")
	}
}
