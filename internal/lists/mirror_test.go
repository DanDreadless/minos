package lists

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"minos/internal/config"
	"minos/internal/filter"
)

func mirroredManager(t *testing.T, primary string, mirrors ...string) (*Manager, *filter.Engine) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Lists.Sources = []config.ListSource{{
			Name: "test", URL: primary, Format: "plain", Enabled: true, Mirrors: mirrors,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	return NewManager(engine, store), engine
}

// A retired primary falls over to its mirror, and the status says so.
func TestRetiredPrimaryFallsBackToMirror(t *testing.T) {
	fastRetries(t)
	primary := newListServer(t, "ads.example\n")
	mirror := newListServer(t, "ads.example\nmirror-only.example\n")
	m, engine := mirroredManager(t, primary.URL, mirror.URL)
	m.Refresh(context.Background())

	primary.set("", http.StatusGone)
	m.Refresh(context.Background())
	m.rebuild(context.Background(), false)
	st := status(t, m)
	if st.State != ListOK || st.ServedBy != mirror.URL {
		t.Fatalf("status = %+v, want ok and served by the mirror", st)
	}
	if st.PrimaryError == "" {
		t.Error("primary_error empty; the reason the primary was skipped must be visible")
	}
	if !engine.Match("mirror-only.example").Blocked {
		t.Error("mirror's copy not compiled")
	}
}

// A passing blip with a fresh copy in hand must NOT reach for a mirror: the
// mirror may lag, and would replace a fresher copy with an older one.
func TestBlipWithFreshCopyKeepsItInsteadOfMirror(t *testing.T) {
	fastRetries(t)
	primary := newListServer(t, "ads.example\n")
	mirror := newListServer(t, "old.example\n")
	m, engine := mirroredManager(t, primary.URL, mirror.URL)
	m.Refresh(context.Background())

	primary.set("", http.StatusServiceUnavailable)
	m.Refresh(context.Background())
	if n := mirror.requests.Load(); n != 0 {
		t.Errorf("mirror asked %d times during a blip with a fresh copy, want 0", n)
	}
	if st := status(t, m); st.State != ListFailing {
		t.Errorf("state = %s, want failing (fresh copy kept)", st.State)
	}
	if !engine.Match("ads.example").Blocked {
		t.Error("fresh copy lost")
	}
}

// With nothing held yet (first boot, primary down), any mirror beats none.
func TestNoCopyYetUsesMirror(t *testing.T) {
	fastRetries(t)
	primary := newListServer(t, "")
	primary.set("", http.StatusServiceUnavailable)
	mirror := newListServer(t, "ads.example\n")
	m, engine := mirroredManager(t, primary.URL, mirror.URL)
	m.Refresh(context.Background())
	if !engine.Match("ads.example").Blocked {
		t.Error("first boot with the primary down: mirror not used")
	}
}

// A stale copy (failing for over two intervals) may be replaced by a mirror.
func TestStaleCopyUsesMirror(t *testing.T) {
	fastRetries(t)
	primary := newListServer(t, "ads.example\n")
	mirror := newListServer(t, "ads.example\nfresh.example\n")
	m, engine := mirroredManager(t, primary.URL, mirror.URL)
	m.Refresh(context.Background())

	m.mu.Lock()
	m.status["test"].LastRefresh = time.Now().Add(-72 * time.Hour) // default interval 24 h
	m.mu.Unlock()
	primary.set("", http.StatusServiceUnavailable)
	m.Refresh(context.Background())
	if !engine.Match("fresh.example").Blocked {
		t.Error("stale copy kept although a mirror could replace it")
	}
}

// Validators are only sent to the URL that issued them.
func TestValidatorsAreNotSentToAnotherHost(t *testing.T) {
	prev := cacheMeta{ETag: `"abc"`, LastModified: "Mon, 01 Jan 2026 00:00:00 GMT"}
	if v := validatorsFor("", prev); v.ETag == "" {
		t.Error("primary's own validators not reused for the primary")
	}
	if v := validatorsFor("https://mirror.example/l.txt", prev); v.ETag != "" || v.LastModified != "" {
		t.Error("primary's validators sent to a mirror")
	}
	prev.Source = "https://mirror.example/l.txt"
	if v := validatorsFor("", prev); v.ETag != "" {
		t.Error("a mirror's validators sent to the primary")
	}
}
