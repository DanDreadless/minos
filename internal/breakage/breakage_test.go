package breakage

import (
	"context"
	"testing"
	"time"

	"minos/internal/querylog"
)

func blocked(client, name, list string, at time.Time) querylog.Entry {
	return querylog.Entry{Time: at, Client: client, QName: name, Verdict: querylog.VerdictBlocked, List: list, Rule: name}
}

// feed sends n blocked lookups a second apart and waits for any chronic
// check to settle.
func feed(d *Detector, client, name, list string, start time.Time, n int) {
	for i := range n {
		d.observe(context.Background(), blocked(client, name, list, start.Add(time.Duration(i)*time.Second)))
	}
	for range 100 {
		d.mu.Lock()
		busy := len(d.pending) > 0
		d.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// usedToWork is a history in which the name answered normally n times in
// the week before, last an hour before the burst, and was not blocked in
// the day before.
func usedToWork(n int) History {
	return func(_ context.Context, _, _ string, start time.Time) (Evidence, error) {
		return Evidence{Worked: n, WorkedLast: start.Add(-time.Hour)}, nil
	}
}

func TestBurstIsSuggested(t *testing.T) {
	var got []Suggestion
	d := New(usedToWork(212))
	done := make(chan Suggestion, 1)
	d.OnNew(func(s Suggestion) { done <- s })
	now := time.Now()

	feed(d, "192.168.1.20", "api.app.example", "StevenBlack", now, Threshold-1)
	if len(d.Suggestions()) != 0 {
		t.Fatal("suggested below the threshold")
	}
	feed(d, "192.168.1.20", "api.app.example", "StevenBlack", now.Add(20*time.Second), 1)
	got = d.Suggestions()
	if len(got) != 1 || got[0].Domain != "api.app.example" || got[0].List != "StevenBlack" || got[0].Count != Threshold ||
		got[0].Worked != 212 || got[0].WorkedLast.IsZero() {
		t.Fatalf("suggestions = %+v", got)
	}
	select {
	case s := <-done:
		if s.Client != "192.168.1.20" {
			t.Errorf("notified %+v", s)
		}
	case <-time.After(time.Second):
		t.Error("no notification for a new suggestion")
	}
	// Further lookups update the count, not a second suggestion.
	feed(d, "192.168.1.20", "api.app.example", "StevenBlack", now.Add(30*time.Second), 3)
	if got = d.Suggestions(); len(got) != 1 || got[0].Count != Threshold+3 {
		t.Fatalf("after more lookups: %+v", got)
	}
	// Once the name is allowed, the app is no longer broken by Minos.
	d.observe(context.Background(), querylog.Entry{Time: now.Add(time.Minute), Client: "192.168.1.20", QName: "api.app.example", Verdict: querylog.VerdictAllowed})
	if len(d.Suggestions()) != 0 {
		t.Error("suggestion kept after the name was allowed")
	}
}

// TestBlockedAllAlongIsNotSuggested is the field report that reshaped
// this package: Google Tag Manager, asked for on every page load, came in
// a burst after a quiet day and was announced as a broken app. It was
// blocked all along — it never worked — so it must never be suggested.
func TestBlockedAllAlongIsNotSuggested(t *testing.T) {
	d := New(usedToWork(0))
	now := time.Now()
	feed(d, "phone", "www.googletagmanager.com", "StevenBlack", now, Threshold*2)
	if len(d.Suggestions()) != 0 {
		t.Fatal("a tag blocked all along was suggested")
	}
	// Two answers in a week (a stray one, say) is still not "used to work".
	d = New(usedToWork(MinWorked - 1))
	feed(d, "phone", "www.googletagmanager.com", "StevenBlack", now, Threshold)
	if len(d.Suggestions()) != 0 {
		t.Fatal("suggested on too little evidence")
	}
}

func TestNoHistoryNoSuggestion(t *testing.T) {
	d := New(nil)
	feed(d, "phone", "api.app.example", "StevenBlack", time.Now(), Threshold*2)
	if len(d.Suggestions()) != 0 {
		t.Fatal("suggested with no evidence that the name ever worked")
	}
	d = New(func(context.Context, string, string, time.Time) (Evidence, error) {
		return Evidence{}, context.DeadlineExceeded
	})
	feed(d, "phone", "api.app.example", "StevenBlack", time.Now(), Threshold)
	if len(d.Suggestions()) != 0 {
		t.Fatal("suggested when the history could not be read")
	}
}

func TestSlowRetriesAreNotABurst(t *testing.T) {
	d := New(usedToWork(50))
	now := time.Now()
	for i := range Threshold * 2 {
		d.observe(context.Background(), blocked("c", "slow.example", "StevenBlack", now.Add(time.Duration(i)*20*time.Second)))
	}
	if len(d.Suggestions()) != 0 {
		t.Error("one lookup every 20 s was treated as a burst")
	}
}

func TestChronicChatterIsNotSuggested(t *testing.T) {
	d := New(func(context.Context, string, string, time.Time) (Evidence, error) {
		return Evidence{Worked: 50, Blocked: ChronicLimit + 1}, nil // Blocked and retried all day
	})
	now := time.Now()
	feed(d, "tv", "ads.tracker.example", "StevenBlack", now, Threshold)
	if len(d.Suggestions()) != 0 {
		t.Fatal("chronic chatter suggested")
	}
	// And it stays quiet for the rest of the day.
	feed(d, "tv", "ads.tracker.example", "StevenBlack", now.Add(time.Hour), Threshold)
	if len(d.Suggestions()) != 0 {
		t.Error("chronic chatter suggested an hour later")
	}
}

func TestDeliberateBlocksAreIgnored(t *testing.T) {
	d := New(usedToWork(50))
	now := time.Now()
	for _, list := range []string{"denylist", "service:tiktok", "group:Kids", "clients", "rate-limit", "firefox-doh-canary", "dnssec"} {
		feed(d, "kid", "x.example", list, now, Threshold*2)
	}
	if len(d.Suggestions()) != 0 {
		t.Errorf("a deliberate block was suggested: %+v", d.Suggestions())
	}
	feed(d, "kid", "cdn.example", "rebinding", now, Threshold) // Protection can break apps too
	if len(d.Suggestions()) != 1 {
		t.Error("a rebinding block burst was not suggested")
	}
}

func TestDismissAndResolve(t *testing.T) {
	d := New(usedToWork(50))
	now := time.Now()
	feed(d, "a", "one.example", "L", now, Threshold)
	feed(d, "b", "sub.two.example", "L", now, Threshold)
	if len(d.Suggestions()) != 2 {
		t.Fatalf("setup: %+v", d.Suggestions())
	}
	if !d.Dismiss("a", "one.example") {
		t.Error("dismiss reported nothing removed")
	}
	feed(d, "a", "one.example", "L", now.Add(time.Minute), Threshold) // Comes back, stays hidden
	d.Resolve("two.example")                                          // Allowed: covers subdomains
	if got := d.Suggestions(); len(got) != 0 {
		t.Errorf("left %+v", got)
	}
}

func TestSweepEndsOldSuggestions(t *testing.T) {
	d := New(usedToWork(50))
	now := time.Now()
	feed(d, "a", "one.example", "L", now, Threshold)
	d.sweep(now.Add(KeepFor + 2*time.Minute))
	if len(d.Suggestions()) != 0 {
		t.Error("a suggestion outlived KeepFor")
	}
}
