package lists

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"minos/internal/config"
	"minos/internal/filter"
)

// headerSpy records the Auth-Key each request carried.
type headerSpy struct {
	mu   sync.Mutex
	seen []string
}

func (h *headerSpy) handler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.seen = append(h.seen, r.Header.Get("Auth-Key"))
		h.mu.Unlock()
		_, _ = w.Write([]byte(body))
	}
}

func (h *headerSpy) keys() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func headerManager(t *testing.T, src config.ListSource) (*Manager, *filter.Engine) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Lists.Sources = []config.ListSource{src}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	return NewManager(engine, store), engine
}

// The credential reaches the primary — and never a mirror, which belongs to
// someone else.
func TestHeadersGoToThePrimaryOnly(t *testing.T) {
	fastRetries(t)
	var primarySpy, mirrorSpy headerSpy
	primary := httptest.NewServer(primarySpy.handler("ads.example\n"))
	defer primary.Close()
	mirror := httptest.NewServer(mirrorSpy.handler("ads.example\n"))
	defer mirror.Close()

	m, _ := headerManager(t, config.ListSource{
		Name: "keyed", URL: primary.URL, Format: "plain", Enabled: true,
		Mirrors: []string{mirror.URL}, Headers: map[string]string{"Auth-Key": "s3cret"},
	})
	m.Refresh(context.Background())
	if got := primarySpy.keys(); len(got) != 1 || got[0] != "s3cret" {
		t.Fatalf("primary saw Auth-Key %v, want [s3cret]", got)
	}

	primary.Close() // the primary goes away...
	m.mu.Lock()
	delete(m.cached, "keyed") // ...with no copy held, so the mirror rule allows a fallback
	m.mu.Unlock()
	m.Refresh(context.Background())
	for _, k := range mirrorSpy.keys() {
		if k != "" {
			t.Errorf("mirror received the primary's credential %q", k)
		}
	}
	if len(mirrorSpy.keys()) == 0 {
		t.Fatal("mirror was never asked; the test proves nothing")
	}
	if st := m.Status()[0]; !st.HasHeaders {
		t.Error("status does not flag that the list sends custom headers")
	}
}

// Go strips Authorization/Cookie on a cross-host redirect, but not custom
// headers: a publisher redirect (or a hijacked one) must not carry the key.
func TestHeadersStrippedOnCrossHostRedirect(t *testing.T) {
	var other headerSpy
	elsewhere := httptest.NewServer(other.handler("ads.example\n"))
	defer elsewhere.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/list.txt", http.StatusFound)
	}))
	defer origin.Close()

	m, engine := headerManager(t, config.ListSource{
		Name: "keyed", URL: origin.URL, Format: "plain", Enabled: true,
		Headers: map[string]string{"Auth-Key": "s3cret"},
	})
	m.Refresh(context.Background())
	m.rebuild(context.Background(), false)
	if got := other.keys(); len(got) != 1 || got[0] != "" {
		t.Errorf("redirect target saw Auth-Key %v, want it stripped", got)
	}
	if !engine.Match("ads.example").Blocked {
		t.Error("the redirected list itself should still load")
	}
}

func TestHeadersKeptOnSameHostRedirect(t *testing.T) {
	var spy headerSpy
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", spy.handler("ads.example\n"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	m, _ := headerManager(t, config.ListSource{
		Name: "keyed", URL: srv.URL + "/old", Format: "plain", Enabled: true,
		Headers: map[string]string{"Auth-Key": "s3cret"},
	})
	m.Refresh(context.Background())
	if got := spy.keys(); len(got) != 1 || got[0] != "s3cret" {
		t.Errorf("same-host redirect: Auth-Key %v, want kept", got)
	}
}
