package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"minos/internal/config"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.3.0", "0.2.0", true},
		{"v0.3.0", "0.2.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.1", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.1.9", "0.2.0", false},
		{"0.3.0", "0.2.0-dev", true}, // dev suffix compares on the base
		{"0.2.0", "0.2.0-dev", false},
		{"garbage", "0.2.0", false},
		{"0.3.0", "garbage", false},
		{"0.3", "0.2.0", false}, // not three parts
	}
	for _, tc := range cases {
		if got := IsNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func testStore(t *testing.T, enabled bool) *config.Store {
	t.Helper()
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.UpdateCheck = enabled
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCheckFetchesAndReports(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name": "v9.9.9"}`))
	}))
	defer ts.Close()

	c := NewChecker("0.2.0", testStore(t, true))
	c.url = ts.URL

	// Nothing known before the first check.
	if v, avail := c.Latest(); v != "" || avail {
		t.Fatalf("pre-check Latest() = (%q, %v), want empty", v, avail)
	}

	c.check(context.Background())
	v, avail := c.Latest()
	if v != "9.9.9" || !avail {
		t.Errorf("Latest() = (%q, %v), want (9.9.9, true)", v, avail)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}
}

func TestCheckSameVersionNotAvailable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v0.2.0"}`))
	}))
	defer ts.Close()
	c := NewChecker("0.2.0", testStore(t, true))
	c.url = ts.URL
	c.check(context.Background())
	if v, avail := c.Latest(); v != "0.2.0" || avail {
		t.Errorf("Latest() = (%q, %v), want (0.2.0, false)", v, avail)
	}
}

// TestDevBuildsNeverCheck: a -dev build makes no requests and reports no
// updates, even with update_check enabled — prompts are for releases.
func TestDevBuildsNeverCheck(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"tag_name": "v9.9.9"}`))
	}))
	defer ts.Close()

	c := NewChecker("0.1.0-dev", testStore(t, true))
	c.url = ts.URL
	c.check(context.Background())
	if hits.Load() != 0 {
		t.Errorf("dev build made %d requests, want 0", hits.Load())
	}
	if v, avail := c.Latest(); v != "" || avail {
		t.Errorf("dev build Latest() = (%q, %v), want empty", v, avail)
	}
}

func TestCheckSurvivesGarbage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer ts.Close()
	c := NewChecker("0.2.0", testStore(t, true))
	c.url = ts.URL
	c.check(context.Background())
	if v, avail := c.Latest(); v != "" || avail {
		t.Errorf("Latest() after garbage = (%q, %v), want empty", v, avail)
	}
}
