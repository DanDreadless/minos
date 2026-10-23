package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"minos/internal/config"
)

type captured struct {
	path string
	body string
	auth string
}

func sink(t *testing.T) (*httptest.Server, *atomic.Pointer[captured], *atomic.Int32) {
	t.Helper()
	var last atomic.Pointer[captured]
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		last.Store(&captured{path: r.URL.Path, body: string(body), auth: r.Header.Get("Authorization")})
	}))
	t.Cleanup(ts.Close)
	return ts, &last, &hits
}

func storeWith(t *testing.T, mutate func(*config.Config)) *config.Store {
	t.Helper()
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		mutate(c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func waitDelivery(t *testing.T, hits *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("deliveries = %d, want %d", hits.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func run(t *testing.T, n *Notifier) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go n.Run(ctx)
}

func TestWebhookDelivery(t *testing.T) {
	ts, last, hits := sink(t)
	n := New(storeWith(t, func(c *config.Config) {
		c.Notifications.WebhookURL = ts.URL
		c.Notifications.UIURL = "http://minos.lan:8080"
	}))
	run(t, n)

	n.Publish(NewDevicesEvent([]DeviceInfo{{Key: "aa:bb:cc:dd:ee:ff", Name: "Pixel 7", IP: "192.168.1.50"}}))
	waitDelivery(t, hits, 1)

	var e Event
	if err := json.Unmarshal([]byte(last.Load().body), &e); err != nil {
		t.Fatalf("webhook body is not JSON: %v", err)
	}
	if e.Type != TypeDeviceNew || e.Title != "New device: Pixel 7" || e.Time.IsZero() || e.Priority != PriorityDefault {
		t.Errorf("event = %+v", e)
	}
	// The field bug: the only link in the old message was the device's IP,
	// which phones auto-linked to http://<ip>. The event now carries the
	// Minos page for the device.
	if want := "http://minos.lan:8080/#/device/aa:bb:cc:dd:ee:ff"; e.URL != want {
		t.Errorf("url = %q, want %q", e.URL, want)
	}
	if len(e.Actions) != 1 || e.Actions[0].URL != e.URL {
		t.Errorf("actions = %+v", e.Actions)
	}
}

func TestNtfyDeliveryIsJSON(t *testing.T) {
	ts, last, hits := sink(t)
	n := New(storeWith(t, func(c *config.Config) {
		c.Notifications.NtfyURL = ts.URL + "/minos-alerts"
		c.Notifications.NtfyToken = "sekrit"
		c.Notifications.UIURL = "http://minos.lan:8080/"
	}))
	run(t, n)

	n.Publish(UpstreamEvent("1.1.1.1:853", true, []string{"9.9.9.9:853"}))
	waitDelivery(t, hits, 1)

	got := last.Load()
	if got.path != "/" {
		t.Errorf("posted to %q, want the server root (JSON publishing)", got.path)
	}
	var m ntfyMessage
	if err := json.Unmarshal([]byte(got.body), &m); err != nil {
		t.Fatalf("ntfy body is not JSON: %v: %s", err, got.body)
	}
	if m.Topic != "minos-alerts" || m.Title != "Resolver not answering: 1.1.1.1:853" ||
		!slices.Equal(m.Tags, []string{"warning"}) || m.Priority != PriorityDefault {
		t.Errorf("ntfy message = %+v", m)
	}
	if m.Click != "http://minos.lan:8080/#/settings?tab=dns" || len(m.Actions) != 1 || m.Actions[0].Action != "view" {
		t.Errorf("click = %q, actions = %+v", m.Click, m.Actions)
	}
	if got.auth != "Bearer sekrit" {
		t.Errorf("ntfy auth = %q, want bearer token", got.auth)
	}
}

func TestSplitNtfyURL(t *testing.T) {
	for _, c := range []struct{ in, server, topic string }{
		{"https://ntfy.sh/my-topic", "https://ntfy.sh/", "my-topic"},
		{"https://example.com/ntfy/alerts/", "https://example.com/ntfy/", "alerts"},
		{"http://10.0.0.2:8090/t?x=1", "http://10.0.0.2:8090/", "t"},
	} {
		s, tp, err := splitNtfyURL(c.in)
		if err != nil || s != c.server || tp != c.topic {
			t.Errorf("%s → %q %q %v, want %q %q", c.in, s, tp, err, c.server, c.topic)
		}
	}
	if _, _, err := splitNtfyURL("https://ntfy.sh/"); err == nil {
		t.Error("a URL without a topic was accepted")
	}
}

func TestNoSinkNoTraffic(t *testing.T) {
	_, _, hits := sink(t)
	n := New(storeWith(t, func(*config.Config) {}))
	run(t, n)
	n.Publish(UpdateEvent("0.1.0", "9.9.9"))
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 with no configured sink", hits.Load())
	}
}

func TestMutedTypesAreNotSent(t *testing.T) {
	ts, last, hits := sink(t)
	n := New(storeWith(t, func(c *config.Config) {
		c.Notifications.WebhookURL = ts.URL
		c.Notifications.Mute = []string{TypeDeviceNew}
	}))
	run(t, n)
	n.Publish(NewDevicesEvent([]DeviceInfo{{Key: "10.0.0.9", IP: "10.0.0.9"}}))
	n.Publish(UpdateEvent("0.1.0", "9.9.9"))
	waitDelivery(t, hits, 1)
	time.Sleep(50 * time.Millisecond)
	if hits.Load() != 1 || !strings.Contains(last.Load().body, TypeUpdateAvailable) {
		t.Errorf("muted device_new was delivered (hits %d, last %s)", hits.Load(), last.Load().body)
	}
}

func TestPublishNeverBlocks(t *testing.T) {
	// Configured sink, but no Run worker draining: fill the queue and keep
	// publishing — Publish must return, dropping the overflow.
	n := New(storeWith(t, func(c *config.Config) { c.Notifications.WebhookURL = "http://127.0.0.1:1/never" }))
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueSize*3; i++ {
			n.Publish(Event{Type: TypeDeviceNew, Title: "t", Message: "m"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full queue")
	}
}

func TestSendTestReportsEachSink(t *testing.T) {
	ts, _, hits := sink(t)
	n := New(storeWith(t, func(c *config.Config) {
		c.Notifications.WebhookURL = ts.URL
		c.Notifications.NtfyURL = "http://127.0.0.1:1/topic" // nothing listens
	}))
	res, err := n.SendTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res["webhook"] != nil || res["ntfy"] == nil || hits.Load() != 1 {
		t.Errorf("results = %v (hits %d), want webhook ok and ntfy failed", res, hits.Load())
	}
	n = New(storeWith(t, func(*config.Config) {}))
	if _, err := n.SendTest(context.Background()); err == nil {
		t.Error("a test with nothing configured reported success")
	}
}

func TestQuietHoursSilenceButNeverDNSDown(t *testing.T) {
	n := New(storeWith(t, func(c *config.Config) { c.Notifications.QuietHours = "22:00-07:00" }))
	cfg := n.store.Get()
	night := time.Date(2026, 10, 23, 23, 30, 0, 0, time.Local)
	day := time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local)
	list := ListEvent("x", "retired", "")
	list.Time = night
	if got := n.finish(cfg, list).Priority; got != PriorityLow {
		t.Errorf("retired list at night: priority %d, want low", got)
	}
	list.Time = day
	if got := n.finish(cfg, list).Priority; got != PriorityHigh {
		t.Errorf("retired list by day: priority %d, want high", got)
	}
	down := DNSDownEvent("1.1.1.1:853")
	down.Time = night
	if got := n.finish(cfg, down).Priority; got != PriorityUrgent {
		t.Errorf("DNS down at night: priority %d, want urgent", got)
	}
	for _, c := range []struct {
		window string
		h, m   int
		quiet  bool
	}{
		{"22:00-07:00", 6, 59, true}, {"22:00-07:00", 7, 0, false}, {"22:00-07:00", 21, 59, false},
		{"13:00-14:00", 13, 30, true}, {"13:00-14:00", 14, 0, false}, {"", 3, 0, false}, {"junk", 3, 0, false},
	} {
		at := time.Date(2026, 1, 1, c.h, c.m, 0, 0, time.Local)
		if got := inQuietHours(c.window, at); got != c.quiet {
			t.Errorf("inQuietHours(%q, %02d:%02d) = %v", c.window, c.h, c.m, got)
		}
	}
}

func TestTransitionControlsFlapping(t *testing.T) {
	n := New(storeWith(t, func(*config.Config) {}))
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return now }
	step := func(d time.Duration, bad, want bool, what string) {
		t.Helper()
		now = now.Add(d)
		if got := n.Transition("u", bad, 30*time.Minute); got != want {
			t.Errorf("%s: announce = %v, want %v", what, got, want)
		}
	}
	step(0, false, false, "recovery with no failure announced")
	step(0, true, true, "first failure")
	step(time.Minute, true, false, "still failing")
	step(time.Minute, false, true, "recovery")
	step(time.Minute, true, false, "re-failure inside the cooldown")
	step(time.Minute, false, false, "recovery of an unannounced failure")
	step(40*time.Minute, true, true, "failure after the cooldown")
}

func TestUIBase(t *testing.T) {
	cfg := config.Default()
	cfg.Notifications.UIURL = "https://minos.example.com/"
	if got := UIBase(cfg); got != "https://minos.example.com" {
		t.Errorf("configured: %q", got)
	}
	if got := DefaultUIBase("192.168.1.2:8080"); got != "http://192.168.1.2:8080" {
		t.Errorf("specific listen: %q", got)
	}
	if got := DefaultUIBase("0.0.0.0:8080"); got != "" && !strings.HasSuffix(got, ":8080") {
		t.Errorf("wildcard listen: %q", got)
	}
	if got := absolute("", "#/devices"); got != "" {
		t.Errorf("a UI path without a base became %q", got)
	}
	if got := absolute("", "https://github.com/x"); got != "https://github.com/x" {
		t.Errorf("an absolute URL was changed: %q", got)
	}
}

// Every event type the notifier can send is one a user can mute, and the
// config's list has nothing the notifier doesn't send.
func TestMutableTypesMatchEvents(t *testing.T) {
	sent := []string{
		TypeDeviceNew, TypeUpstreamSick, TypeUpstreamRecovered, TypeDNSDown, TypeListFailing,
		TypeListRecovered, TypeUpdateAvailable, TypeACMEFailed, TypeDigest,
	}
	if !slices.Equal(sent, config.NotificationTypes) {
		t.Errorf("config.NotificationTypes = %v, want %v", config.NotificationTypes, sent)
	}
	for _, typ := range sent {
		if _, ok := ntfyTags[typ]; !ok {
			t.Errorf("%s has no ntfy tag", typ)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"garbage webhook":  func(c *config.Config) { c.Notifications.WebhookURL = "not a url" },
		"non-http ntfy":    func(c *config.Config) { c.Notifications.NtfyURL = "ftp://example.com/topic" },
		"ntfy w/o topic":   func(c *config.Config) { c.Notifications.NtfyURL = "https://ntfy.sh/" },
		"bad ui url":       func(c *config.Config) { c.Notifications.UIURL = "minos.lan" },
		"bad quiet hours":  func(c *config.Config) { c.Notifications.QuietHours = "late-early" },
		"unknown mute":     func(c *config.Config) { c.Notifications.Mute = []string{"device_gone"} },
		"half quiet hours": func(c *config.Config) { c.Notifications.QuietHours = "22:00" },
	} {
		cfg := config.Default()
		mutate(cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cfg := config.Default()
	cfg.Notifications.QuietHours = "22:00-07:00"
	cfg.Notifications.Mute = []string{TypeDigest}
	cfg.Notifications.UIURL = "http://192.168.1.2:8080"
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid settings refused: %v", err)
	}
}

func TestDeviceBatcherGroupsABurst(t *testing.T) {
	var got []Event
	done := make(chan struct{})
	b := NewDeviceBatcher(30*time.Millisecond, func(ips []string) []DeviceInfo {
		out := make([]DeviceInfo, len(ips))
		for i, ip := range ips {
			out[i] = DeviceInfo{Key: ip, IP: ip}
		}
		return out
	}, func(e Event) {
		got = append(got, e)
		close(done)
	})
	for _, ip := range []string{"10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		b.Add(ip)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the batch never went out")
	}
	if len(got) != 1 || got[0].Title != "3 new devices on your network" || got[0].Link != "#/devices" {
		t.Errorf("events = %+v", got)
	}
}

func TestEventWording(t *testing.T) {
	cases := []struct {
		name         string
		e            Event
		title, start string
	}{
		{"named device", NewDevicesEvent([]DeviceInfo{{Key: "k", Name: "Kitchen speaker", Vendor: "Sonos", IP: "192.168.1.9", MAC: "aa:bb:cc:dd:ee:ff"}}),
			"New device: Kitchen speaker",
			"Kitchen speaker made its first DNS query through Minos (made by Sonos, address 192.168.1.9, MAC aa:bb:cc:dd:ee:ff)."},
		{"vendor only", NewDevicesEvent([]DeviceInfo{{Key: "k", Vendor: "Apple", IP: "192.168.1.9"}}),
			"New device: Apple device", "Apple device made its first DNS query"},
		{"nothing known", NewDevicesEvent([]DeviceInfo{{Key: "k", IP: "192.168.1.9", PrivateMAC: true, MAC: "02:00:00:00:00:01"}}),
			"New device on your network",
			"A device with a private address made its first DNS query through Minos (address 192.168.1.9, private (randomized) MAC address)."},
		{"list retired", ListEvent("URLhaus", "retired", "HTTP 404"), "Blocklist retired: URLhaus",
			"URLhaus is no longer published (HTTP 404). Minos keeps blocking with the last copy"},
		{"list recovered", ListEvent("URLhaus", "ok", ""), "Blocklist updating again: URLhaus", "URLhaus downloaded"},
		{"only upstream down", UpstreamEvent("1.1.1.1:853", true, nil), "DNS is down: no resolver is answering",
			"Every upstream resolver has stopped answering (the last was 1.1.1.1:853)"},
		{"update", UpdateEvent("0.23.0", "0.24.0"), "Minos v0.24.0 is available", "This server runs v0.23.0."},
		{"acme", ACMEEvent("dns.example.com", errors.New("rate limited")), "Certificate renewal failing for dns.example.com",
			"Minos cannot get a certificate for dns.example.com and will retry hourly: rate limited."},
	}
	for _, c := range cases {
		if c.e.Title != c.title || !strings.HasPrefix(c.e.Message, c.start) {
			t.Errorf("%s:\n title %q\n  want %q\n body %q\n  want prefix %q", c.name, c.e.Title, c.title, c.e.Message, c.start)
		}
		if c.e.Link == "" {
			t.Errorf("%s: no link — every notification must say where to act on it", c.name)
		}
	}
	if e := UpstreamEvent("1.1.1.1:853", true, nil); e.Priority != PriorityUrgent || e.Type != TypeDNSDown {
		t.Errorf("all upstreams down is %s at priority %d", e.Type, e.Priority)
	}
}
