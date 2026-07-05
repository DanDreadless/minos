package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"minos/internal/config"
)

type captured struct {
	body  string
	title string
	tags  string
	auth  string
}

func sink(t *testing.T) (*httptest.Server, *atomic.Pointer[captured], *atomic.Int32) {
	t.Helper()
	var last atomic.Pointer[captured]
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		last.Store(&captured{
			body:  string(body),
			title: r.Header.Get("Title"),
			tags:  r.Header.Get("Tags"),
			auth:  r.Header.Get("Authorization"),
		})
	}))
	t.Cleanup(ts.Close)
	return ts, &last, &hits
}

func storeWith(t *testing.T, mutate func(*config.NotificationsConfig)) *config.Store {
	t.Helper()
	store, err := config.Open(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		mutate(&c.Notifications)
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

func TestWebhookDelivery(t *testing.T) {
	ts, last, hits := sink(t)
	store := storeWith(t, func(n *config.NotificationsConfig) { n.WebhookURL = ts.URL })
	n := New(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	n.Publish("device_new", "New device on your network", "192.168.1.50 (phone.lan)")
	waitDelivery(t, hits, 1)

	var e Event
	if err := json.Unmarshal([]byte(last.Load().body), &e); err != nil {
		t.Fatalf("webhook body is not JSON: %v", err)
	}
	if e.Type != "device_new" || e.Title != "New device on your network" ||
		e.Message != "192.168.1.50 (phone.lan)" || e.Time.IsZero() {
		t.Errorf("event = %+v", e)
	}
}

func TestNtfyDelivery(t *testing.T) {
	ts, last, hits := sink(t)
	store := storeWith(t, func(n *config.NotificationsConfig) {
		n.NtfyURL = ts.URL + "/minos"
		n.NtfyToken = "sekrit"
	})
	n := New(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	n.Publish("upstream_sick", "Upstream resolver failing", "1.1.1.1:853 stopped answering")
	waitDelivery(t, hits, 1)

	got := last.Load()
	if got.body != "1.1.1.1:853 stopped answering" {
		t.Errorf("ntfy body = %q", got.body)
	}
	if got.title != "Upstream resolver failing" || got.tags != "warning" {
		t.Errorf("ntfy headers = title %q tags %q", got.title, got.tags)
	}
	if got.auth != "Bearer sekrit" {
		t.Errorf("ntfy auth = %q, want bearer token", got.auth)
	}
}

func TestNoSinkNoTraffic(t *testing.T) {
	ts, _, hits := sink(t)
	// A sink exists, but the config points at nothing.
	store := storeWith(t, func(n *config.NotificationsConfig) {})
	n := New(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	n.Publish("update_available", "Minos update available", "v9.9.9")
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 0 {
		t.Errorf("deliveries = %d, want 0 with no configured sink", hits.Load())
	}
	_ = ts
}

func TestPublishNeverBlocks(t *testing.T) {
	// Configured sink, but no Run worker draining: fill the queue and keep
	// publishing — Publish must return, dropping the overflow.
	store := storeWith(t, func(n *config.NotificationsConfig) {
		n.WebhookURL = "http://127.0.0.1:1/never"
	})
	n := New(store)
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueSize*3; i++ {
			n.Publish("device_new", "t", "m")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full queue")
	}
}

func TestConfigValidationRejectsBadURLs(t *testing.T) {
	cfg := config.Default()
	cfg.Notifications.WebhookURL = "not a url"
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted a garbage webhook URL")
	}
	cfg = config.Default()
	cfg.Notifications.NtfyURL = "ftp://example.com/topic"
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted a non-http ntfy URL")
	}
}
