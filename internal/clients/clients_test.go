package clients

import (
	"fmt"
	"testing"
	"time"

	"minos/internal/config"
)

func testConfig() *config.Config {
	c := config.Default()
	c.Groups = []config.Group{
		{Name: "kids", Mode: "filter", Denylist: []string{"tiktok.com"}, Allowlist: []string{"school.example.com"}},
		{Name: "trusted", Mode: "bypass"},
		{Name: "iot", Mode: "block"},
	}
	c.Clients = []config.Client{
		{IP: "10.0.0.10", Name: "tablet", Group: "kids"},
		{IP: "10.0.0.20", Name: "laptop", Group: "trusted"},
		{IP: "10.0.0.30", Name: "camera", Group: "iot"},
		{IP: "10.0.0.40", Name: "phone", Blocked: true},
		{IP: "10.0.0.50", Name: "labelled only"},
	}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

func TestPolicyResolution(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(testConfig())

	if p := r.PolicyFor("10.0.0.99"); p != nil {
		t.Errorf("unknown client policy = %+v, want nil (default)", p)
	}
	// A labelled client with no group and not blocked needs no policy entry.
	if p := r.PolicyFor("10.0.0.50"); p != nil {
		t.Errorf("label-only client policy = %+v, want nil (default)", p)
	}

	kids := r.PolicyFor("10.0.0.10")
	if kids == nil || kids.Mode != ModeFilter || kids.Group != "kids" || kids.Overlay == nil {
		t.Fatalf("kids policy = %+v, want filter mode with overlay", kids)
	}
	if res := kids.Overlay.Match("video.tiktok.com"); !res.Blocked || res.Rule != "tiktok.com" {
		t.Errorf("overlay deny miss: %+v", res)
	}
	if res := kids.Overlay.Match("school.example.com"); res.Blocked || res.Rule != "school.example.com" {
		t.Errorf("overlay allow miss: %+v", res)
	}
	if kids.Refuses() || kids.Bypasses() {
		t.Error("filter-mode policy must neither refuse nor bypass")
	}

	if p := r.PolicyFor("10.0.0.20"); !p.Bypasses() || p.Refuses() {
		t.Errorf("trusted policy = %+v, want bypass", p)
	}
	if p := r.PolicyFor("10.0.0.30"); !p.Refuses() {
		t.Errorf("iot policy = %+v, want refuse (block group)", p)
	}
	if p := r.PolicyFor("10.0.0.40"); !p.Refuses() {
		t.Errorf("blocked client policy = %+v, want refuse", p)
	}
	// A blocked device never bypasses, even in a bypass group.
	cfg := testConfig()
	cfg.Clients[1].Blocked = true
	r.ApplyConfig(cfg)
	if p := r.PolicyFor("10.0.0.20"); !p.Refuses() || p.Bypasses() {
		t.Errorf("blocked-in-bypass-group policy = %+v, want refuse", p)
	}
}

func TestTouchAndDevices(t *testing.T) {
	r := NewRegistry()
	cfg := testConfig()
	r.ApplyConfig(cfg)
	now := time.Now()

	r.Touch("10.0.0.10", true, now.Add(-time.Minute))
	r.Touch("10.0.0.10", false, now)
	r.Touch("10.0.0.99", false, now.Add(-time.Hour))

	devs := r.Devices(cfg)
	byIP := map[string]Device{}
	for _, d := range devs {
		byIP[d.IP] = d
	}
	// 2 seen + 5 configured with 1 overlap = 6.
	if len(devs) != 6 {
		t.Fatalf("got %d devices, want 6: %+v", len(devs), devs)
	}
	tab := byIP["10.0.0.10"]
	if !tab.Seen || tab.Queries != 2 || tab.QBlocked != 1 || tab.Name != "tablet" || tab.Group != "kids" {
		t.Errorf("tablet = %+v", tab)
	}
	stranger := byIP["10.0.0.99"]
	if !stranger.Seen || stranger.Group != "default" || stranger.Name != "" {
		t.Errorf("stranger = %+v", stranger)
	}
	cam := byIP["10.0.0.30"]
	if cam.Seen || cam.Queries != 0 || cam.Name != "camera" {
		t.Errorf("configured-unseen = %+v", cam)
	}
	// Seen devices sort before unseen, newest first.
	if devs[0].IP != "10.0.0.10" || !devs[0].Seen {
		t.Errorf("first device = %+v, want most recently seen", devs[0])
	}
}

func TestSeedDoesNotClobberLiveState(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.Touch("10.0.0.1", false, now)
	r.Seed("10.0.0.1", 500, 100, now.Add(-24*time.Hour), now.Add(-time.Hour))
	r.Seed("10.0.0.2", 7, 3, now.Add(-24*time.Hour), now.Add(-time.Hour))

	devs := r.Devices(config.Default())
	byIP := map[string]Device{}
	for _, d := range devs {
		byIP[d.IP] = d
	}
	if byIP["10.0.0.1"].Queries != 1 {
		t.Errorf("live device overwritten by seed: %+v", byIP["10.0.0.1"])
	}
	if byIP["10.0.0.2"].Queries != 7 || byIP["10.0.0.2"].QBlocked != 3 {
		t.Errorf("seeded device = %+v", byIP["10.0.0.2"])
	}
}

func BenchmarkTouch(b *testing.B) {
	r := NewRegistry()
	now := time.Now()
	r.Touch("10.0.0.1", false, now)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Touch("10.0.0.1", i%4 == 0, now)
	}
}

func BenchmarkPolicyFor(b *testing.B) {
	r := NewRegistry()
	cfg := testConfig()
	for i := 0; i < 250; i++ {
		cfg.Clients = append(cfg.Clients, config.Client{
			IP: fmt.Sprintf("10.1.%d.%d", i/250, i%250), Group: "kids",
		})
	}
	r.ApplyConfig(cfg)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.PolicyFor("10.0.0.10")
	}
}
