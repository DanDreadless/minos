package lists

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetries shrinks every retry delay for the duration of a test.
func fastRetries(t *testing.T) {
	t.Helper()
	ab, rs := attemptBackoff, retrySchedule
	attemptBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	retrySchedule = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}
	t.Cleanup(func() { attemptBackoff, retrySchedule = ab, rs })
}

func domains(n int, prefix string) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%s%d.example\n", prefix, i)
	}
	return sb.String()
}

func status(t *testing.T, m *Manager) SourceStatus {
	t.Helper()
	st := m.Status()
	if len(st) != 1 {
		t.Fatalf("status has %d lists, want 1", len(st))
	}
	return st[0]
}

// A blip — two 503s, then the list — is absorbed inside one refresh.
func TestTransientBlipIsRetriedWithinARefresh(t *testing.T) {
	fastRetries(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ads.example\n"))
	}))
	defer srv.Close()
	m, engine := testManager(t, srv.URL, "")
	m.Refresh(context.Background())
	if st := status(t, m); st.State != ListOK || st.ConsecutiveFailures != 0 {
		t.Errorf("after a blip: %+v, want ok", st)
	}
	if !engine.Match("ads.example").Blocked {
		t.Error("list not compiled after recovering from the blip")
	}
}

// A retired list (404/410) is reported as retired, never hammered, and the
// last good copy stays in force — the URLhaus failure, handled.
func TestRetiredListKeepsLastCopyAndIsNotRetriedEarly(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\n")
	m, engine := testManager(t, srv.URL, "")
	m.Refresh(context.Background())

	srv.set("", http.StatusGone)
	before := srv.requests.Load()
	m.Refresh(context.Background())
	if got := srv.requests.Load() - before; got != 1 {
		t.Errorf("a 410 was requested %d times in one refresh, want 1 (no retries)", got)
	}
	st := status(t, m)
	if st.State != ListRetired || !strings.Contains(st.LastError, "410") {
		t.Errorf("state = %s, err = %q, want retired with the status", st.State, st.LastError)
	}
	if !engine.Match("ads.example").Blocked {
		t.Error("retired list stopped blocking; the last good copy should stay in force")
	}
	if d := m.retryDelay(24 * time.Hour); d != 0 {
		t.Errorf("retired list scheduled an early retry in %s, want none", d)
	}
}

func TestUnauthorizedFeedIsReportedAsSuch(t *testing.T) {
	srv := newListServer(t, "ads.example\n")
	srv.set("", http.StatusUnauthorized)
	m, _ := testManager(t, srv.URL, "")
	m.Refresh(context.Background())
	if st := status(t, m); st.State != ListUnauthorized {
		t.Errorf("state = %s, want unauthorized", st.State)
	}
}

// A captive portal or CDN error page served with a 200 must not replace the
// list — before vetting every line parsed as skipped and the list silently
// protected nothing.
func TestHTMLPageDoesNotReplaceTheList(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\n")
	m, engine := testManager(t, srv.URL, "")
	m.Refresh(context.Background())

	srv.set("<!DOCTYPE html><html><body>Sign in to the hotel Wi-Fi</body></html>", 0)
	m.Refresh(context.Background())
	m.rebuild(context.Background(), false)
	st := status(t, m)
	if st.State != ListFailing || !strings.Contains(st.LastError, "web page") {
		t.Errorf("state = %s, err = %q, want failing with the web-page reason", st.State, st.LastError)
	}
	if !engine.Match("ads.example").Blocked {
		t.Error("an HTML page replaced the list")
	}
}

// A sudden collapse is refused and the old copy kept; the same smaller body
// served shrinkAcceptAfter refreshes running is accepted as the new size.
func TestShrunkListIsHeldThenAcceptedWhenItStaysSmall(t *testing.T) {
	big := domains(400, "big")
	srv := newListServer(t, big)
	m, engine := testManager(t, srv.URL, "")
	m.Refresh(context.Background())
	if st := status(t, m); st.Rules != 400 {
		t.Fatalf("rules = %d, want 400", st.Rules)
	}

	small := domains(40, "small")
	srv.set(small, 0)
	for i := 1; i < shrinkAcceptAfter; i++ {
		m.Refresh(context.Background())
		st := status(t, m)
		if st.State != ListSuspicious || st.Rules != 400 || !strings.Contains(st.LastError, "40 rules against 400") {
			t.Fatalf("refresh %d: %+v, want suspicious with the 400-rule copy kept", i, st)
		}
		if !engine.Match("big7.example").Blocked || engine.Match("small7.example").Blocked {
			t.Fatalf("refresh %d: the shrunken body was compiled", i)
		}
	}
	m.Refresh(context.Background()) // the same small body, once more
	if st := status(t, m); st.State != ListOK || st.Rules != 40 {
		t.Errorf("after %d identical small downloads: %+v, want it accepted", shrinkAcceptAfter, st)
	}
	if !engine.Match("small7.example").Blocked {
		t.Error("accepted body not compiled")
	}
}

// A small list may swing freely: below the floor nothing is vetted for size.
func TestSmallListsMaySwing(t *testing.T) {
	srv := newListServer(t, domains(50, "a"))
	m, _ := testManager(t, srv.URL, "")
	m.Refresh(context.Background())
	srv.set(domains(5, "b"), 0)
	m.Refresh(context.Background())
	if st := status(t, m); st.State != ListOK || st.Rules != 5 {
		t.Errorf("small list swing: %+v, want accepted", st)
	}
}

// The field bug: a source that failed was not tried again for a whole
// refresh interval (24 h) — a Pi that booted before its network had no
// lists for a day. Run now retries failing sources on a short backoff.
func TestRunRetriesAFailedSourceLongBeforeTheInterval(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\n")
	srv.set("", http.StatusServiceUnavailable) // "no network yet" at boot
	m, engine := testManager(t, srv.URL, "")   // refresh interval: 24 h default

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	waitFor(t, func() bool { return status(t, m).State == ListUnavailable })

	srv.set("ads.example\n", 0) // the network comes up
	waitFor(t, func() bool { return engine.Match("ads.example").Blocked })
	if st := status(t, m); st.State != ListOK {
		t.Errorf("state after recovery = %s, want ok", st.State)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRetryDelayBacksOffAndCaps(t *testing.T) {
	m, _ := testManager(t, "https://lists.example/a.txt", "")
	src := allSources(m.store.Get())[0]
	m.mu.Lock()
	st := m.ensureStatus(src)
	st.failKind = failTransient
	m.mu.Unlock()
	for _, tc := range []struct {
		fails    int
		interval time.Duration
		want     time.Duration
	}{
		{1, 24 * time.Hour, time.Minute},
		{3, 24 * time.Hour, 5 * time.Minute},
		{50, 24 * time.Hour, time.Hour},       // past the schedule: its last step
		{3, 2 * time.Minute, 2 * time.Minute}, // never later than a normal refresh
	} {
		m.mu.Lock()
		st.ConsecutiveFailures = tc.fails
		m.mu.Unlock()
		if got := m.retryDelay(tc.interval); got != tc.want {
			t.Errorf("%d failures, interval %s: retry in %s, want %s", tc.fails, tc.interval, got, tc.want)
		}
	}
}

func TestListStates(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	for _, tc := range []struct {
		name string
		s    SourceStatus
		want ListState
	}{
		{"never fetched", SourceStatus{Enabled: true}, ListPending},
		{"healthy", SourceStatus{Enabled: true, hasBody: true, LastRefresh: now, LastAttempt: now}, ListOK},
		{"loaded from disk", SourceStatus{Enabled: true, hasBody: true, LastRefresh: now.Add(-time.Hour)}, ListOK},
		{"failing, recent copy", SourceStatus{Enabled: true, hasBody: true, failKind: failTransient, LastRefresh: now.Add(-day)}, ListFailing},
		{"failing, old copy", SourceStatus{Enabled: true, hasBody: true, failKind: failTransient, LastRefresh: now.Add(-3 * day)}, ListStale},
		{"failing, no copy", SourceStatus{Enabled: true, failKind: failTransient}, ListUnavailable},
		{"disabled", SourceStatus{Enabled: false, failKind: failRetired}, ListDisabled},
	} {
		if got := tc.s.state(day, now); got != tc.want {
			t.Errorf("%s: state = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":      0,
		"7":     7 * time.Second,
		"86400": maxRetryAfter, // capped: a server can't stall a refresh for a day
		"-3":    0,
		"soon":  0,
	} {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("Retry-After %q = %s, want %s", in, got, want)
		}
	}
}
