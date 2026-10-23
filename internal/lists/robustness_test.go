package lists

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"minos/internal/config"
	"minos/internal/filter"
)

// listServer serves a plain list with an ETag, honouring If-None-Match, and
// can be switched to failing mid-test.
type listServer struct {
	*httptest.Server
	mu       sync.Mutex
	body     string
	status   int // 0 = serve normally
	bodies   atomic.Int32
	notMods  atomic.Int32
	requests atomic.Int32
}

func newListServer(t *testing.T, body string) *listServer {
	t.Helper()
	ls := &listServer{body: body}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.requests.Add(1)
		ls.mu.Lock()
		body, status := ls.body, ls.status
		ls.mu.Unlock()
		if status != 0 {
			http.Error(w, "down", status)
			return
		}
		etag := `"` + bodyHash([]byte(body))[:16] + `"`
		if r.Header.Get("If-None-Match") == etag {
			ls.notMods.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		ls.bodies.Add(1)
		w.Header().Set("ETag", etag)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *listServer) set(body string, status int) {
	ls.mu.Lock()
	ls.body, ls.status = body, status
	ls.mu.Unlock()
}

func testManager(t *testing.T, url, cacheDir string) (*Manager, *filter.Engine) {
	t.Helper()
	store, err := config.Open(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.Lists.Sources = []config.ListSource{{Name: "test", URL: url, Format: "plain", Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine := filter.NewEngine()
	m := NewManager(engine, store)
	if cacheDir != "" {
		m.SetCacheDir(cacheDir)
	}
	return m, engine
}

// startup runs what Run does before its refresh loop.
func startup(m *Manager) {
	ctx := context.Background()
	if m.loadFromDisk() {
		m.rebuild(ctx, false)
	}
	m.Refresh(ctx)
}

// TestListsSurviveRestartDuringOutage is the failure this cache exists for:
// before it, a restart while a list host was down brought that list back
// with zero rules.
func TestListsSurviveRestartDuringOutage(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\ntracker.example\n")
	dir := t.TempDir()

	first, _ := testManager(t, srv.URL, dir)
	startup(first)

	srv.set("", http.StatusServiceUnavailable)     // the host goes down...
	second, engine := testManager(t, srv.URL, dir) // ...and Minos restarts
	if !second.loadFromDisk() {
		t.Fatal("nothing loaded from the disk cache")
	}
	second.rebuild(context.Background(), false)
	if res := engine.Match("ads.example"); !res.Blocked {
		t.Fatal("before any fetch, the cached list is not enforcing")
	}
	second.Refresh(context.Background())
	if res := engine.Match("tracker.example"); !res.Blocked || res.List != "test" {
		t.Errorf("after a failed refresh: %+v, want the cached list still blocking", res)
	}
	st := second.Status()[0]
	if st.LastError == "" || st.Rules != 2 {
		t.Errorf("status = %+v, want the fetch error reported and 2 rules kept", st)
	}
}

// TestUnchangedListCostsA304: with validators held, a refresh of an
// unchanged list must not download it again.
func TestUnchangedListCostsA304(t *testing.T) {
	srv := newListServer(t, "ads.example\n")
	m, engine := testManager(t, srv.URL, t.TempDir())
	startup(m)
	m.Refresh(context.Background())
	m.Refresh(context.Background())
	if b, n := srv.bodies.Load(), srv.notMods.Load(); b != 1 || n != 2 {
		t.Errorf("bodies=%d notModified=%d, want 1 download then two 304s", b, n)
	}
	if res := engine.Match("ads.example"); !res.Blocked {
		t.Error("list stopped blocking after 304 refreshes")
	}

	srv.set("ads.example\nnew.example\n", 0) // content changes: downloaded again
	m.Refresh(context.Background())
	if res := engine.Match("new.example"); !res.Blocked {
		t.Error("changed list not re-downloaded")
	}
}

// TestRestartReuses304Validators: validators survive a restart via the disk
// cache, so the first refresh after a reboot is a 304 too.
func TestRestartReuses304Validators(t *testing.T) {
	srv := newListServer(t, "ads.example\n")
	dir := t.TempDir()
	first, _ := testManager(t, srv.URL, dir)
	startup(first)

	second, _ := testManager(t, srv.URL, dir)
	startup(second)
	if b := srv.bodies.Load(); b != 1 {
		t.Errorf("full downloads = %d across a restart, want 1", b)
	}
}

func TestDiskCacheIgnoresTornBody(t *testing.T) {
	dir := t.TempDir()
	c, err := newDiskCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://lists.example/a.txt"
	if err := c.store(url, []byte("ads.example\n"), cacheMeta{}); err != nil {
		t.Fatal(err)
	}
	bp, _ := c.paths(url)
	if err := os.WriteFile(bp, []byte("ads.exa"), 0o600); err != nil { // truncated
		t.Fatal(err)
	}
	if _, _, ok := c.load(url); ok {
		t.Error("a body that fails its checksum was loaded")
	}
}

func TestDiskCachePrunesRemovedLists(t *testing.T) {
	dir := t.TempDir()
	c, err := newDiskCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	keep, drop := "https://lists.example/keep.txt", "https://lists.example/drop.txt"
	for _, u := range []string{keep, drop} {
		if err := c.store(u, []byte("x.example\n"), cacheMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmp-123"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.prune(map[string]bool{keep: true})
	if _, _, ok := c.load(keep); !ok {
		t.Error("configured list was pruned")
	}
	if _, _, ok := c.load(drop); ok {
		t.Error("removed list survived the prune")
	}
	if fileExists(filepath.Join(dir, ".tmp-123")) {
		t.Error("interrupted temp file survived the prune")
	}
}

// TestSameContentDoesNotRewriteBody: SD-card discipline — an identical 200
// (a server without validators) rewrites metadata only.
func TestSameContentDoesNotRewriteBody(t *testing.T) {
	dir := t.TempDir()
	c, err := newDiskCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://lists.example/a.txt"
	if err := c.store(url, []byte("ads.example\n"), cacheMeta{}); err != nil {
		t.Fatal(err)
	}
	bp, _ := c.paths(url)
	before, err := os.Stat(bp)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store(url, []byte("ads.example\n"), cacheMeta{}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(bp)
	if err != nil {
		t.Fatal(err)
	}
	// A rewrite is temp file + rename, which replaces the file's identity.
	if !os.SameFile(before, after) {
		t.Error("identical content rewrote the body file")
	}
	if err := c.store(url, []byte("other.example\n"), cacheMeta{}); err != nil {
		t.Fatal(err)
	}
	changed, err := os.Stat(bp)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(after, changed) {
		t.Error("changed content did not rewrite the body file")
	}
}
