package mqtt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// broker is a tiny in-process MQTT broker: enough of the protocol to test
// the client's side of it. The Mosquitto end-to-end test (mqtt_e2e build
// tag) checks the client against a real one.
type broker struct {
	t  *testing.T
	ln net.Listener

	mu        sync.Mutex
	connects  []connectInfo
	published []Message
	subs      []string
	conns     []net.Conn
	refuse    byte // CONNACK return code to answer with
	silent    bool // stop answering pings
	got       chan Message
}

type connectInfo struct {
	clientID, user, pass string
	will                 *Message
	keepAlive            uint16
}

func newBroker(t *testing.T) *broker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &broker{t: t, ln: ln, got: make(chan Message, 100)}
	t.Cleanup(func() {
		ln.Close()
		b.dropAll()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b.mu.Lock()
			b.conns = append(b.conns, c)
			b.mu.Unlock()
			go b.serve(c)
		}
	}()
	return b
}

func (b *broker) url() string { return "mqtt://" + b.ln.Addr().String() }

func (b *broker) dropAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		_ = c.Close()
	}
	b.conns = nil
}

func (b *broker) send(c net.Conn, kind, flags byte, body []byte) {
	pkt, _ := encode(kind, flags, body)
	_, _ = c.Write(pkt)
}

func (b *broker) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	for {
		p, err := readPacket(r, 1<<20)
		if err != nil {
			return
		}
		switch p.kind {
		case typeConnect:
			info := parseConnect(b.t, p.body)
			b.mu.Lock()
			b.connects = append(b.connects, info)
			code := b.refuse
			b.mu.Unlock()
			b.send(c, typeConnack, 0, []byte{0, code})
			if code != 0 {
				return
			}
		case typeSubscribe:
			id := p.body[:2]
			rest := p.body[2:]
			var codes []byte
			for len(rest) > 0 {
				f, r2, err := readString(rest)
				if err != nil {
					return
				}
				rest = r2[1:]
				b.mu.Lock()
				b.subs = append(b.subs, f)
				b.mu.Unlock()
				code := byte(0)
				if strings.HasPrefix(f, "forbidden/") {
					code = 0x80
				}
				codes = append(codes, code)
			}
			b.send(c, typeSuback, 0, append(append([]byte{}, id...), codes...))
		case typePublish:
			m, _, _, err := parsePublish(p)
			if err != nil {
				return
			}
			m.Payload = append([]byte(nil), m.Payload...)
			b.mu.Lock()
			b.published = append(b.published, m)
			b.mu.Unlock()
			b.got <- m
		case typePingreq:
			b.mu.Lock()
			silent := b.silent
			b.mu.Unlock()
			if !silent {
				b.send(c, typePingresp, 0, nil)
			}
		case typePuback:
			b.got <- Message{Topic: "$puback"}
		case typeDisconnect:
			b.got <- Message{Topic: "$disconnect"}
			return
		}
	}
}

func parseConnect(t *testing.T, body []byte) connectInfo {
	name, rest, err := readString(body)
	if err != nil || name != "MQTT" || rest[0] != protocolLevel {
		t.Errorf("bad CONNECT header %q", name)
	}
	flags := rest[1]
	info := connectInfo{keepAlive: binary.BigEndian.Uint16(rest[2:])}
	rest = rest[4:]
	info.clientID, rest, _ = readString(rest)
	if flags&0x04 != 0 {
		w := &Message{Retain: flags&0x20 != 0}
		w.Topic, rest, _ = readString(rest)
		var payload string
		payload, rest, _ = readString(rest)
		w.Payload = []byte(payload)
		info.will = w
	}
	if flags&0x80 != 0 {
		info.user, rest, _ = readString(rest)
	}
	if flags&0x40 != 0 {
		info.pass, _, _ = readString(rest)
	}
	return info
}

func (b *broker) wait(t *testing.T, topic string) Message {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-b.got:
			if m.Topic == topic {
				return m
			}
		case <-timeout:
			t.Fatalf("broker never received %q", topic)
		}
	}
}

// pushTo sends a message to every connected client, as a broker delivers to
// subscribers.
func (b *broker) pushTo(m Message, qos byte, id uint16) {
	body := appendString(nil, m.Topic)
	if qos == 1 {
		body = binary.BigEndian.AppendUint16(body, id)
	}
	body = append(body, m.Payload...)
	flags := qos << 1
	if m.Retain {
		flags |= 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		b.send(c, typePublish, flags, body)
	}
}

type recorder struct {
	mu       sync.Mutex
	states   []error
	connects chan struct{}
	msgCh    chan Message
}

func newRecorder() *recorder {
	return &recorder{connects: make(chan struct{}, 10), msgCh: make(chan Message, 10)}
}

func (r *recorder) opts(broker string) Options {
	return Options{
		Broker:    broker,
		ClientID:  "minos-test",
		Username:  "ha",
		Password:  "s3cret",
		KeepAlive: time.Second,
		Will:      &Message{Topic: "minos/availability", Payload: []byte("offline"), Retain: true},
		Subscribe: []string{"minos/+/set", "homeassistant/status"},
		OnConnect: func() { r.connects <- struct{}{} },
		OnMessage: func(m Message) { r.msgCh <- m },
		OnState: func(up bool, err error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !up {
				r.states = append(r.states, err)
			}
		},
	}
}

func (r *recorder) waitConnect(t *testing.T) {
	t.Helper()
	select {
	case <-r.connects:
	case <-time.After(5 * time.Second):
		t.Fatal("never connected")
	}
}

func start(t *testing.T, o Options) (*Client, context.CancelFunc, chan struct{}) {
	t.Helper()
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return c, cancel, done
}

func TestConnectPublishSubscribe(t *testing.T) {
	b := newBroker(t)
	rec := newRecorder()
	c, _, _ := start(t, rec.opts(b.url()))
	rec.waitConnect(t)

	b.mu.Lock()
	info := b.connects[0]
	subs := append([]string{}, b.subs...)
	b.mu.Unlock()
	if info.clientID != "minos-test" || info.user != "ha" || info.pass != "s3cret" || info.keepAlive != 1 {
		t.Errorf("connect %+v", info)
	}
	if info.will == nil || info.will.Topic != "minos/availability" || string(info.will.Payload) != "offline" || !info.will.Retain {
		t.Errorf("will %+v", info.will)
	}
	if strings.Join(subs, ",") != "minos/+/set,homeassistant/status" {
		t.Errorf("subscriptions %v", subs)
	}

	if err := c.Publish(Message{Topic: "minos/state", Payload: []byte(`{"paused":false}`), Retain: true}); err != nil {
		t.Fatal(err)
	}
	if m := b.wait(t, "minos/state"); string(m.Payload) != `{"paused":false}` || !m.Retain {
		t.Errorf("published %+v", m)
	}

	// An incoming QoS 1 message is delivered and acknowledged.
	b.pushTo(Message{Topic: "minos/blocking/set", Payload: []byte("OFF")}, 1, 7)
	select {
	case m := <-rec.msgCh:
		if m.Topic != "minos/blocking/set" || string(m.Payload) != "OFF" || m.Retain {
			t.Errorf("received %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("message not delivered")
	}
	b.wait(t, "$puback")

	// A retained replay is marked as such.
	b.pushTo(Message{Topic: "homeassistant/status", Payload: []byte("online"), Retain: true}, 0, 0)
	if m := <-rec.msgCh; !m.Retain {
		t.Error("retained flag lost")
	}
}

func TestReconnectsAfterDrop(t *testing.T) {
	b := newBroker(t)
	rec := newRecorder()
	c, _, _ := start(t, rec.opts(b.url()))
	rec.waitConnect(t)
	b.dropAll()
	rec.waitConnect(t) // OnConnect runs again, so state is republished
	if err := c.Publish(Message{Topic: "minos/x", Payload: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	b.wait(t, "minos/x")
}

func TestDeadConnectionIsNoticed(t *testing.T) {
	b := newBroker(t)
	rec := newRecorder()
	start(t, rec.opts(b.url()))
	rec.waitConnect(t)
	// A broker that stops answering (half-open connection) is given up on
	// after two keep-alive intervals and redialled.
	b.mu.Lock()
	b.silent = true
	b.mu.Unlock()
	time.Sleep(2500 * time.Millisecond)
	b.mu.Lock()
	b.silent = false
	b.mu.Unlock()
	rec.waitConnect(t)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.states) == 0 || !strings.Contains(rec.states[0].Error(), "lost the connection") {
		t.Errorf("states %v", rec.states)
	}
}

func TestWrongPasswordIsReported(t *testing.T) {
	b := newBroker(t)
	b.refuse = 4
	rec := newRecorder()
	start(t, rec.opts(b.url()))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		n := len(rec.states)
		rec.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.states) == 0 || !strings.Contains(rec.states[0].Error(), "wrong username or password") {
		t.Errorf("states %v", rec.states)
	}
}

func TestRefusedSubscriptionIsReported(t *testing.T) {
	b := newBroker(t)
	rec := newRecorder()
	o := rec.opts(b.url())
	o.Subscribe = []string{"forbidden/cmd"}
	start(t, o)
	time.Sleep(500 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.states) == 0 || !strings.Contains(rec.states[0].Error(), "refused the subscription") {
		t.Errorf("states %v", rec.states)
	}
}

func TestCleanStopPublishesWill(t *testing.T) {
	b := newBroker(t)
	rec := newRecorder()
	_, cancel, done := start(t, rec.opts(b.url()))
	rec.waitConnect(t)
	cancel()
	<-done
	if m := b.wait(t, "minos/availability"); string(m.Payload) != "offline" || !m.Retain {
		t.Errorf("will %+v", m)
	}
	b.wait(t, "$disconnect")
}

func TestPublishWhileDisconnected(t *testing.T) {
	c, err := New(Options{Broker: "mqtt://127.0.0.1:1", ClientID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(Message{Topic: "a", Payload: nil}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("err %v", err)
	}
	if err := c.Publish(Message{Topic: "a/#"}); err == nil {
		t.Error("wildcard topic accepted")
	}
}

func TestParseBroker(t *testing.T) {
	for _, tc := range []struct {
		in, addr string
		tls, ok  bool
	}{
		{"mqtt://192.168.1.10", "192.168.1.10:1883", false, true},
		{"mqtt://ha.lan:1884", "ha.lan:1884", false, true},
		{"mqtts://ha.lan", "ha.lan:8883", true, true},
		{"mqtt://[fd00::1]:1883", "[fd00::1]:1883", false, true},
		{"tcp://ha.lan", "ha.lan:1883", false, true},
		{"http://ha.lan", "", false, false},
		{"ha.lan:1883", "", false, false},
		{"mqtt://user:pw@ha.lan", "", false, false},
		{"mqtt://ha.lan/topic", "", false, false},
		{"", "", false, false},
	} {
		addr, _, useTLS, err := ParseBroker(tc.in)
		if (err == nil) != tc.ok || addr != tc.addr || useTLS != tc.tls {
			t.Errorf("%q: %q %v %v", tc.in, addr, useTLS, err)
		}
	}
}

func TestPacketCodec(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 16383, 16384, 2097151, 2097152} {
		pkt, err := encode(typePublish, 0, make([]byte, n))
		if err != nil {
			t.Fatal(err)
		}
		p, err := readPacket(bufio.NewReader(bytes.NewReader(pkt)), 4<<20)
		if err != nil || len(p.body) != n || p.kind != typePublish {
			t.Fatalf("length %d: %v %d", n, err, len(p.body))
		}
	}
	// Five length bytes is malformed.
	if _, err := readPacket(bufio.NewReader(bytes.NewReader([]byte{0x30, 0xff, 0xff, 0xff, 0xff, 0x01})), MaxPacket); !errors.Is(err, errMalformed) {
		t.Errorf("long length: %v", err)
	}
	// An oversized packet is skipped and the next one still reads.
	big, _ := encode(typePublish, 0, make([]byte, MaxPacket+1))
	ping, _ := encode(typePingresp, 0, nil)
	r := bufio.NewReader(bytes.NewReader(append(big, ping...)))
	if _, err := readPacket(r, MaxPacket); !errors.Is(err, errSkipped) {
		t.Fatalf("oversized: %v", err)
	}
	if p, err := readPacket(r, MaxPacket); err != nil || p.kind != typePingresp {
		t.Fatalf("after skip: %v %+v", err, p)
	}
	// Truncated strings and QoS 2 are refused.
	if _, _, _, err := parsePublish(packet{kind: typePublish, body: []byte{0, 9, 'a'}}); err == nil {
		t.Error("truncated topic accepted")
	}
	if _, _, _, err := parsePublish(packet{kind: typePublish, flags: 4, body: appendString(nil, "a")}); err == nil {
		t.Error("QoS 2 accepted")
	}
	if _, _, _, err := parsePublish(packet{kind: typePublish, flags: 2, body: appendString(nil, "a")}); err == nil {
		t.Error("QoS 1 without a packet id accepted")
	}
}

// FuzzReadPacket feeds arbitrary broker bytes through the reader and the
// publish parser: they must fail cleanly, never panic.
func FuzzReadPacket(f *testing.F) {
	pub, _ := encode(typePublish, 2, append(appendString(nil, "minos/x"), 0, 1, 'h', 'i'))
	f.Add(pub)
	f.Add([]byte{0x90, 0x03, 0x00, 0x01, 0x80})
	f.Add([]byte{0x30, 0xff, 0xff, 0xff, 0x7f})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bufio.NewReader(bytes.NewReader(data))
		for {
			p, err := readPacket(r, 1024)
			if errors.Is(err, errSkipped) {
				continue
			}
			if err != nil {
				return
			}
			if p.kind == typePublish {
				_, _, _, _ = parsePublish(p)
			}
			if p.kind == typeSuback {
				_ = checkSuback(p, 1, []string{"a"})
			}
		}
	})
}
