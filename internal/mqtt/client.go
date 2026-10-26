package mqtt

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Message is one MQTT message.
type Message struct {
	Topic   string
	Payload []byte
	// Retain asks the broker to keep the message for later subscribers. On
	// an incoming message it means the broker is replaying a stored one, not
	// that somebody just sent it.
	Retain bool
}

// Options configure a Client.
type Options struct {
	// Broker is mqtt://host[:1883] or mqtts://host[:8883].
	Broker   string
	ClientID string
	Username string
	Password string
	// KeepAlive is the ping interval (default 30 s). A connection that has
	// been silent for twice this long is treated as dead and redialled.
	KeepAlive time.Duration
	// Will is published by the broker if the connection drops without a
	// goodbye, and by the client itself when Run stops cleanly.
	Will *Message
	// Subscribe lists topic filters subscribed (at QoS 0) on every connect.
	Subscribe []string
	// OnConnect runs after each successful connect and subscribe, in its own
	// goroutine — the place to publish discovery and current state.
	OnConnect func()
	// OnMessage receives every incoming message, on the reading goroutine:
	// it must return quickly.
	OnMessage func(Message)
	// OnState reports connection changes: connected, or the error that ended
	// (or prevented) a connection.
	OnState func(connected bool, err error)
	// TLS overrides the TLS configuration for mqtts (tests, private CAs).
	TLS *tls.Config
	// Dial overrides the network dialer (tests).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// ErrNotConnected is returned by Publish while there is no connection.
// Callers publish their full state again from OnConnect, so a message lost
// this way is replaced as soon as the connection is back.
var ErrNotConnected = errors.New("not connected to the MQTT broker")

const (
	dialTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
	maxBackoff   = time.Minute
)

// Client keeps one connection to a broker open, redialling after failures.
type Client struct {
	o         Options
	addr      string
	host      string
	tls       bool
	keepAlive time.Duration

	mu     sync.Mutex // serialises writes to conn
	conn   net.Conn
	up     atomic.Bool
	nextID atomic.Uint32
}

// New checks the options; Run does the connecting.
func New(o Options) (*Client, error) {
	addr, host, useTLS, err := ParseBroker(o.Broker)
	if err != nil {
		return nil, err
	}
	if o.ClientID == "" || len(o.ClientID) > 65535 {
		return nil, errors.New("mqtt: client ID is required")
	}
	if o.Will != nil {
		if err := ValidTopic(o.Will.Topic); err != nil {
			return nil, err
		}
	}
	for _, f := range o.Subscribe {
		if f == "" || !utf8.ValidString(f) || strings.ContainsRune(f, 0) {
			return nil, fmt.Errorf("mqtt: invalid topic filter %q", f)
		}
	}
	ka := o.KeepAlive
	if ka <= 0 {
		ka = 30 * time.Second
	}
	if ka > 18*time.Hour {
		ka = 18 * time.Hour // The protocol's field is 16 bits of seconds
	}
	return &Client{o: o, addr: addr, host: host, tls: useTLS, keepAlive: ka}, nil
}

// ParseBroker validates a broker URL and returns the address to dial, the
// host name (for TLS) and whether to use TLS.
func ParseBroker(s string) (addr, host string, useTLS bool, err error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" {
		return "", "", false, fmt.Errorf("broker %q: use mqtt://host:1883 or mqtts://host:8883", s)
	}
	port := "1883"
	switch u.Scheme {
	case "mqtt", "tcp":
	case "mqtts", "ssl", "tls":
		useTLS, port = true, "8883"
	default:
		return "", "", false, fmt.Errorf("broker %q: use mqtt://host:1883 or mqtts://host:8883", s)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false, fmt.Errorf("broker %q: give only the scheme, host and port (the username and password have their own settings)", s)
	}
	host = u.Hostname()
	if host == "" {
		return "", "", false, fmt.Errorf("broker %q: missing host", s)
	}
	if p := u.Port(); p != "" {
		port = p
	}
	return net.JoinHostPort(host, port), host, useTLS, nil
}

// ValidTopic reports whether t can be published to: not empty, valid UTF-8,
// no NUL and no wildcard characters (§4.7).
func ValidTopic(t string) error {
	if t == "" || len(t) > 65535 || !utf8.ValidString(t) || strings.ContainsAny(t, "+#\x00") {
		return fmt.Errorf("mqtt: invalid topic %q", t)
	}
	return nil
}

// Connected reports whether a connection is currently up.
func (c *Client) Connected() bool { return c.up.Load() }

// Publish sends m at QoS 0. It fails with ErrNotConnected while the client
// is between connections.
func (c *Client) Publish(m Message) error {
	if err := ValidTopic(m.Topic); err != nil {
		return err
	}
	var flags byte
	if m.Retain {
		flags = 0x01
	}
	pkt, err := encode(typePublish, flags, publishBody(m))
	if err != nil {
		return err
	}
	return c.write(pkt)
}

func (c *Client) write(pkt []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || !c.up.Load() {
		return ErrNotConnected
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := c.conn.Write(pkt); err != nil {
		// The reader notices the broken connection and redials.
		_ = c.conn.Close()
		return fmt.Errorf("mqtt write: %w", err)
	}
	return nil
}

// Run connects and stays connected until ctx ends, redialling with a
// back-off (1 s doubling to a minute; reset once a connection has lasted a
// minute). On a clean stop it publishes the will itself, then says goodbye.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.state(false, err)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (c *Client) state(up bool, err error) {
	if c.o.OnState != nil {
		c.o.OnState(up, err)
	}
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	dial := c.o.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(dctx, "tcp", c.addr)
	if err != nil {
		return nil, err
	}
	if !c.tls {
		return conn, nil
	}
	cfg := &tls.Config{ServerName: c.host, MinVersion: tls.VersionTLS12}
	if c.o.TLS != nil {
		cfg = c.o.TLS.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = c.host
		}
	}
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(dctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tc, nil
}

// session runs one connection from dial to failure (or ctx's end).
func (c *Client) session(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("can't reach the broker at %s: %w", c.addr, err)
	}
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)

	// CONNECT and CONNACK, before anything else may use the connection.
	pkt, err := encode(typeConnect, 0, connectBody(&c.o, uint16(c.keepAlive/time.Second)))
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(dialTimeout))
	if _, err := conn.Write(pkt); err != nil {
		return fmt.Errorf("mqtt connect: %w", err)
	}
	ack, err := readPacket(r, MaxPacket)
	if err != nil {
		return fmt.Errorf("mqtt connect: %w", err)
	}
	if ack.kind != typeConnack || len(ack.body) != 2 {
		return fmt.Errorf("mqtt connect: %w: expected CONNACK", errMalformed)
	}
	if err := connackError(ack.body[1]); err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Time{})

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	c.up.Store(true)
	defer func() {
		c.mu.Lock()
		c.up.Store(false)
		c.conn = nil
		c.mu.Unlock()
	}()

	subID := uint16(0)
	if len(c.o.Subscribe) > 0 {
		subID = c.packetID()
		pkt, err := encode(typeSubscribe, 0x02, subscribeBody(subID, c.o.Subscribe))
		if err != nil {
			return err
		}
		if err := c.write(pkt); err != nil {
			return err
		}
	} else {
		c.connected()
	}

	// Pings: sent every keep-alive interval whatever else is happening, so
	// the broker's replies keep the read deadline below from expiring on a
	// healthy connection. Silence for two intervals means the connection is
	// dead even if TCP hasn't noticed (a broker restart behind NAT).
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(c.keepAlive)
		defer t.Stop()
		ping, _ := encode(typePingreq, 0, nil)
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				// Wake the reader, which says goodbye before Run returns.
				_ = conn.SetReadDeadline(time.Now())
				return
			case <-t.C:
				if err := c.write(ping); err != nil {
					return
				}
			}
		}
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * c.keepAlive))
		p, err := readPacket(r, MaxPacket)
		if errors.Is(err, errSkipped) {
			slog.Warn("mqtt: ignored a message larger than the limit", "limit_bytes", MaxPacket)
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				c.goodbye()
				return nil
			}
			return fmt.Errorf("lost the connection to the broker: %w", err)
		}
		switch p.kind {
		case typePublish:
			m, qos, id, err := parsePublish(p)
			if err != nil {
				return err
			}
			if qos == 1 {
				ack, _ := encode(typePuback, 0, binary.BigEndian.AppendUint16(nil, id))
				if err := c.write(ack); err != nil {
					return err
				}
			}
			if c.o.OnMessage != nil {
				c.o.OnMessage(m)
			}
		case typeSuback:
			if err := checkSuback(p, subID, c.o.Subscribe); err != nil {
				return err
			}
			c.connected()
		case typePingresp:
		default:
			return fmt.Errorf("%w: unexpected packet type %d", errMalformed, p.kind)
		}
	}
}

func (c *Client) connected() {
	c.state(true, nil)
	if c.o.OnConnect != nil {
		go c.o.OnConnect()
	}
}

// goodbye publishes the will (a clean DISCONNECT tells the broker not to)
// and disconnects.
func (c *Client) goodbye() {
	if c.o.Will != nil {
		_ = c.Publish(*c.o.Will)
	}
	pkt, _ := encode(typeDisconnect, 0, nil)
	_ = c.write(pkt)
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Unlock()
}

// packetID returns the next non-zero packet identifier.
func (c *Client) packetID() uint16 {
	for {
		if id := uint16(c.nextID.Add(1)); id != 0 {
			return id
		}
	}
}
