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

func TestScheduleActive(t *testing.T) {
	// 2026-07-04 is a Saturday.
	at := func(day, hour, min int) time.Time {
		return time.Date(2026, 7, day, hour, min, 0, 0, time.UTC)
	}
	day := &config.Schedule{Start: "09:00", End: "17:00"}
	wrap := &config.Schedule{Start: "21:00", End: "07:00"}
	satNight := &config.Schedule{Days: []string{"sat"}, Start: "21:00", End: "07:00"}

	cases := []struct {
		name string
		s    *config.Schedule
		now  time.Time
		want bool
	}{
		{"inside day window", day, at(4, 10, 0), true},
		{"start is inclusive", day, at(4, 9, 0), true},
		{"end is exclusive", day, at(4, 17, 0), false},
		{"before window", day, at(4, 8, 59), false},
		{"wrap: late evening", wrap, at(4, 23, 0), true},
		{"wrap: early morning (yesterday's anchor)", wrap, at(4, 3, 0), true},
		{"wrap: midday", wrap, at(4, 12, 0), false},
		{"sat-only: saturday night", satNight, at(4, 23, 0), true},
		{"sat-only: sunday 3am still saturday's window", satNight, at(5, 3, 0), true},
		{"sat-only: sunday night", satNight, at(5, 23, 0), false},
		{"sat-only: friday night", satNight, at(3, 23, 0), false},
	}
	for _, tc := range cases {
		if got := scheduleActive(tc.s, tc.now); got != tc.want {
			t.Errorf("%s: scheduleActive(%v) = %v, want %v", tc.name, tc.now, got, tc.want)
		}
	}
}

func TestScheduledGroupTogglesPolicy(t *testing.T) {
	cfg := testConfig()
	cfg.Groups[0].Schedule = &config.Schedule{Start: "21:00", End: "07:00"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.ApplyConfig(cfg)

	// Inside the window the kids policy applies...
	r.rebuildPolicies(time.Date(2026, 7, 4, 23, 0, 0, 0, time.UTC))
	if p := r.PolicyFor("10.0.0.10"); p == nil || p.Group != "kids" {
		t.Errorf("in-window policy = %+v, want kids group", p)
	}
	// ...outside it the member follows the default rules.
	r.rebuildPolicies(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC))
	if p := r.PolicyFor("10.0.0.10"); p != nil {
		t.Errorf("out-of-window policy = %+v, want nil (default)", p)
	}
	// A per-device block survives its group's window closing.
	cfg2 := testConfig()
	cfg2.Groups[0].Schedule = &config.Schedule{Start: "21:00", End: "07:00"}
	cfg2.Clients[0].Blocked = true
	r.ApplyConfig(cfg2)
	r.rebuildPolicies(time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC))
	if p := r.PolicyFor("10.0.0.10"); !p.Refuses() {
		t.Errorf("blocked device out-of-window = %+v, want refuse", p)
	}
}

func TestScheduleValidation(t *testing.T) {
	bad := []config.Schedule{
		{Days: []string{"monday"}, Start: "09:00", End: "17:00"},
		{Start: "9am", End: "17:00"},
		{Start: "09:00", End: "24:30"},
		{Start: "09:00", End: "09:00"},
	}
	for i, sch := range bad {
		cfg := testConfig()
		s := sch
		cfg.Groups[0].Schedule = &s
		if err := cfg.Validate(); err == nil {
			t.Errorf("case %d: Validate() accepted %+v", i, sch)
		}
	}
}

func TestNewDeviceCallback(t *testing.T) {
	r := NewRegistry()
	var got []string
	r.OnNewDevice(func(ip, mac, hostname string) { got = append(got, ip) })

	// Seeded (hydrated) devices never notify.
	r.Seed("10.0.0.1", 5, 1, time.Now().Add(-time.Hour), time.Now())
	r.emitNew("10.0.0.1")

	// Live-discovered devices notify exactly once.
	r.Touch("10.0.0.2", false, time.Now())
	r.emitNew("10.0.0.2")
	r.emitNew("10.0.0.2") // duplicate emit (e.g. re-enrichment) is a no-op
	r.Touch("10.0.0.2", false, time.Now())
	r.emitNew("10.0.0.2") // subsequent traffic is not "new"

	if len(got) != 1 || got[0] != "10.0.0.2" {
		t.Errorf("callbacks = %v, want exactly [10.0.0.2]", got)
	}
}

func TestGroupBlockedServices(t *testing.T) {
	cfg := testConfig()
	cfg.Groups[0].Services = []string{"youtube"}
	cfg.Groups[0].Allowlist = append(cfg.Groups[0].Allowlist, "youtubekids.com")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.ApplyConfig(cfg)

	kids := r.PolicyFor("10.0.0.10")
	if kids == nil || kids.Overlay == nil {
		t.Fatal("kids policy missing overlay")
	}
	if res := kids.Overlay.Match("www.youtube.com"); !res.Blocked || res.List != "service:youtube" {
		t.Errorf("www.youtube.com = %+v, want blocked by service:youtube", res)
	}
	// A group pardon beats the group's own service block.
	if res := kids.Overlay.Match("youtubekids.com"); res.Blocked {
		t.Errorf("youtubekids.com = %+v, want pardoned", res)
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
	// Sorted by numeric IP, seen and unseen interleaved by address.
	gotOrder := make([]string, len(devs))
	for i, d := range devs {
		gotOrder[i] = d.IP
	}
	wantOrder := []string{"10.0.0.10", "10.0.0.20", "10.0.0.30", "10.0.0.40", "10.0.0.50", "10.0.0.99"}
	if fmt.Sprint(gotOrder) != fmt.Sprint(wantOrder) {
		t.Errorf("device order = %v, want %v", gotOrder, wantOrder)
	}
}

func TestDevicesSortedByIP(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	// Insert in a deliberately scrambled order, including the classic
	// string-vs-numeric trap (.9 vs .10 vs .100) and an IPv6 address.
	for _, ip := range []string{"192.168.1.100", "192.168.1.9", "192.168.1.10", "fe80::1", "192.168.1.2"} {
		r.Touch(ip, false, now)
	}
	devs := r.Devices(config.Default())
	got := make([]string, len(devs))
	for i, d := range devs {
		got[i] = d.IP
	}
	// Numeric v4 order first (.2 < .9 < .10 < .100), then v6.
	want := []string{"192.168.1.2", "192.168.1.9", "192.168.1.10", "192.168.1.100", "fe80::1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("order = %v, want %v", got, want)
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
