package notify

import (
	"context"
	"testing"
	"time"
)

type fakeStats struct {
	total, blocked int
	domains        []string
	counts         []int
	client         string
	clientQueries  int
	newClients     int
}

func (f fakeStats) Totals(context.Context, time.Time) (int, int, error) {
	return f.total, f.blocked, nil
}

func (f fakeStats) TopBlockedSummary(context.Context, time.Time, int) ([]string, []int, error) {
	return f.domains, f.counts, nil
}

func (f fakeStats) BusiestClient(context.Context, time.Time) (string, int, error) {
	return f.client, f.clientQueries, nil
}

func (f fakeStats) NewClientsSince(context.Context, time.Time) (int, error) {
	return f.newClients, nil
}

func TestDigestMessageGolden(t *testing.T) {
	stats := fakeStats{
		total: 48210, blocked: 9114,
		domains: []string{"doubleclick.net", "ads.example.com", "tracker.io"},
		counts:  []int{1893, 912, 455},
		client:  "192.168.1.50", clientQueries: 12041,
		newClients: 2,
	}
	got, err := digestMessage(t.Context(), stats, time.Now(), "7 days")
	if err != nil {
		t.Fatal(err)
	}
	want := "48,210 queries in the last 7 days — 9,114 blocked (18.9%).\n" +
		"Top blocked: doubleclick.net (1,893), ads.example.com (912), tracker.io (455).\n" +
		"Busiest client: 192.168.1.50 (12,041 queries).\n" +
		"2 new devices."
	if got != want {
		t.Errorf("digest message:\n%s\nwant:\n%s", got, want)
	}
}

func TestDigestMessageQuietNetwork(t *testing.T) {
	got, err := digestMessage(t.Context(), fakeStats{}, time.Now(), "24 hours")
	if err != nil {
		t.Fatal(err)
	}
	if got != "No queries in the last 24 hours." {
		t.Errorf("quiet digest = %q", got)
	}
}

func TestDigestMessageSingularDevice(t *testing.T) {
	got, err := digestMessage(t.Context(), fakeStats{total: 10, blocked: 1, newClients: 1},
		time.Now(), "24 hours")
	if err != nil {
		t.Fatal(err)
	}
	want := "10 queries in the last 24 hours — 1 blocked (10.0%).\n1 new device."
	if got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}
}

func TestNextDigestFire(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	at := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, loc)
	}
	cases := []struct {
		name    string
		cadence string
		after   time.Time
		want    time.Time
	}{
		// 2026-07-08 is a Wednesday.
		{"daily before 9", "daily", at(2026, 7, 8, 8, 30), at(2026, 7, 8, 9, 0)},
		{"daily at 9 exactly", "daily", at(2026, 7, 8, 9, 0), at(2026, 7, 9, 9, 0)},
		{"daily after 9", "daily", at(2026, 7, 8, 10, 0), at(2026, 7, 9, 9, 0)},
		// Next Monday from Wednesday is 2026-07-13.
		{"weekly midweek", "weekly", at(2026, 7, 8, 10, 0), at(2026, 7, 13, 9, 0)},
		// Monday before 9 fires the same day; at/after 9 waits a week.
		{"weekly monday early", "weekly", at(2026, 7, 13, 7, 0), at(2026, 7, 13, 9, 0)},
		{"weekly monday late", "weekly", at(2026, 7, 13, 9, 0), at(2026, 7, 20, 9, 0)},
	}
	for _, tc := range cases {
		if got := nextDigestFire(tc.cadence, tc.after); !got.Equal(tc.want) {
			t.Errorf("%s: nextDigestFire = %v, want %v", tc.name, got, tc.want)
		}
	}

	// DST safety: Europe/London springs forward 2026-03-29; the Monday after
	// (2026-03-30) must still fire at 09:00 local.
	if lon, err := time.LoadLocation("Europe/London"); err == nil {
		after := time.Date(2026, 3, 28, 12, 0, 0, 0, lon) // Saturday before
		got := nextDigestFire("weekly", after)
		want := time.Date(2026, 3, 30, 9, 0, 0, 0, lon)
		if !got.Equal(want) {
			t.Errorf("DST: nextDigestFire = %v, want %v", got, want)
		}
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{
		0: "0", 42: "42", 999: "999", 1000: "1,000",
		12041: "12,041", 1234567: "1,234,567",
	} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
