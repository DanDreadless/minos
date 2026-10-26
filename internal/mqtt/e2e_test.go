//go:build mqtt_e2e

package mqtt

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestE2EMosquitto runs the client against a real Mosquitto broker
// (MQTT_BROKER, with user MQTT_USER / MQTT_PASS). CI starts one in Docker;
// locally:
//
//	docker run -d --name minos-mosquitto -p 18830:1883 -v <dir>:/mosquitto/config eclipse-mosquitto:2
//	MQTT_BROKER=mqtt://127.0.0.1:18830 MQTT_USER=minos MQTT_PASS=… go test -tags mqtt_e2e ./internal/mqtt
func TestE2EMosquitto(t *testing.T) {
	broker, user, pass := os.Getenv("MQTT_BROKER"), os.Getenv("MQTT_USER"), os.Getenv("MQTT_PASS")
	if broker == "" {
		t.Skip("MQTT_BROKER not set")
	}

	// A watcher sees the other client's messages and its will.
	watched := make(chan Message, 20)
	watcherUp := make(chan struct{}, 5)
	watcher, err := New(Options{
		Broker: broker, ClientID: "minos-e2e-watcher", Username: user, Password: pass,
		Subscribe: []string{"minos-e2e/#"},
		OnConnect: func() { watcherUp <- struct{}{} },
		OnMessage: func(m Message) { watched <- m },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)
	waitFor(t, watcherUp, "watcher connect")

	// The client under test: its raw connection is kept so the test can cut
	// it without a goodbye, as a crash or a pulled cable would.
	var mu sync.Mutex
	var raw net.Conn
	cmds := make(chan Message, 5)
	up := make(chan struct{}, 5)
	c, err := New(Options{
		Broker: broker, ClientID: "minos-e2e", Username: user, Password: pass,
		KeepAlive: 5 * time.Second,
		Will:      &Message{Topic: "minos-e2e/availability", Payload: []byte("offline"), Retain: true},
		Subscribe: []string{"minos-e2e-cmd/+/set"},
		OnConnect: func() { up <- struct{}{} },
		OnMessage: func(m Message) { cmds <- m },
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			mu.Lock()
			raw = conn
			mu.Unlock()
			return conn, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cctx, ccancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		c.Run(cctx)
		close(done)
	}()
	waitFor(t, up, "client connect")

	if err := c.Publish(Message{Topic: "minos-e2e/state", Payload: []byte("hello"), Retain: true}); err != nil {
		t.Fatal(err)
	}
	expect(t, watched, "minos-e2e/state", "hello")

	// A command reaches the client's subscription.
	if err := watcher.Publish(Message{Topic: "minos-e2e-cmd/blocking/set", Payload: []byte("OFF")}); err != nil {
		t.Fatal(err)
	}
	expect(t, cmds, "minos-e2e-cmd/blocking/set", "OFF")

	// Cutting the connection: the broker publishes the will, and the client
	// redials and runs OnConnect again.
	mu.Lock()
	raw.Close()
	mu.Unlock()
	expect(t, watched, "minos-e2e/availability", "offline")
	waitFor(t, up, "reconnect")

	// A clean stop publishes the will itself.
	if err := c.Publish(Message{Topic: "minos-e2e/availability", Payload: []byte("online"), Retain: true}); err != nil {
		t.Fatal(err)
	}
	expect(t, watched, "minos-e2e/availability", "online")
	ccancel()
	<-done
	expect(t, watched, "minos-e2e/availability", "offline")

	// Clear the retained messages this test left behind.
	for _, topic := range []string{"minos-e2e/state", "minos-e2e/availability"} {
		_ = watcher.Publish(Message{Topic: topic, Retain: true})
	}

	// The wrong password is refused, in words.
	errs := make(chan error, 5)
	bad, _ := New(Options{
		Broker: broker, ClientID: "minos-e2e-bad", Username: user, Password: pass + "x",
		OnState: func(up bool, err error) {
			if !up {
				errs <- err
			}
		},
	})
	bctx, bcancel := context.WithCancel(ctx)
	defer bcancel()
	go bad.Run(bctx)
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "wrong username or password") && !strings.Contains(err.Error(), "not authorized") {
			t.Errorf("bad password: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("bad password never reported")
	}
}

func waitFor(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func expect(t *testing.T, ch chan Message, topic, payload string) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case m := <-ch:
			if m.Topic == topic && string(m.Payload) == payload {
				return
			}
		case <-timeout:
			t.Fatalf("never saw %s = %q", topic, payload)
		}
	}
}
