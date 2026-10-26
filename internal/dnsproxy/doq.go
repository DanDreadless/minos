package dnsproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// DNS over QUIC (RFC 9250) and DNS over HTTP/3.
//
// DoQ carries one query per QUIC stream: a 2-byte length, the message
// (ID 0), and the answer back the same way. QUIC gives TLS 1.3 encryption
// without TCP's head-of-line blocking, and survives a phone moving between
// networks. Like DoT and DoH, the listener reuses handle() unchanged —
// policies, local records, the cache and the log all apply.

const (
	doqALPN = "doq"
	// doqStreamTimeout bounds reading one query from, and writing its
	// answer to, a stream; a client that opens a stream and stalls is
	// dropped.
	doqStreamTimeout = 10 * time.Second
	// DoQ application error codes (RFC 9250 §4.3).
	doqNoError       quic.ApplicationErrorCode = 0x0
	doqInternalError quic.StreamErrorCode      = 0x1
)

func doqQUICConfig() *quic.Config {
	return &quic.Config{
		MaxIdleTimeout:     30 * time.Second,
		MaxIncomingStreams: 100,
		KeepAlivePeriod:    15 * time.Second,
	}
}

// startDoQ listens for DNS-over-QUIC on addr with the listeners' TLS
// configuration (certificate shared with DoT/DoH).
func (s *Server) startDoQ(addr string, base *tls.Config) error {
	tcfg := base.Clone()
	tcfg.NextProtos = []string{doqALPN}
	tcfg.MinVersion = tls.VersionTLS13 // QUIC requires 1.3
	ln, err := quic.ListenAddr(addr, tcfg, doqQUICConfig())
	if err != nil {
		return fmt.Errorf("bind doq %s: %w", addr, err)
	}
	s.doq = ln
	s.doqAddr = ln.Addr()
	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				if !errors.Is(err, quic.ErrServerClosed) {
					slog.Error("doq server stopped", "err", err)
				}
				return
			}
			go s.serveDoQConn(conn)
		}
	}()
	slog.Info("doq server listening", "addr", s.doqAddr.String())
	return nil
}

func (s *Server) serveDoQConn(conn *quic.Conn) {
	for {
		st, err := conn.AcceptStream(context.Background())
		if err != nil {
			return // the client closed the connection, or it idled out
		}
		go s.serveDoQStream(conn, st)
	}
}

func (s *Server) serveDoQStream(conn *quic.Conn, st *quic.Stream) {
	defer func() { _ = st.Close() }()
	_ = st.SetDeadline(time.Now().Add(doqStreamTimeout))
	req, err := readDoQMessage(st)
	if err != nil {
		st.CancelRead(doqInternalError)
		return
	}
	// RFC 9250 §4.2.1: the ID is 0 on the wire; answer with the same.
	dw := &dohResponseWriter{remote: conn.RemoteAddr()}
	s.handle(dw, req)
	if dw.msg == nil {
		st.CancelWrite(doqInternalError)
		return
	}
	dw.msg.Id = req.Id
	if err := writeDoQMessage(st, dw.msg); err != nil {
		st.CancelWrite(doqInternalError)
	}
}

func readDoQMessage(r io.Reader) (*dns.Msg, error) {
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(n[:]))
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	m := new(dns.Msg)
	if err := m.Unpack(buf); err != nil {
		return nil, err
	}
	return m, nil
}

func writeDoQMessage(w io.Writer, m *dns.Msg) error {
	packed, err := m.Pack()
	if err != nil {
		return err
	}
	if len(packed) > 0xffff {
		return errors.New("message too large")
	}
	out := make([]byte, 2+len(packed))
	binary.BigEndian.PutUint16(out, uint16(len(packed)))
	copy(out[2:], packed)
	_, err = w.Write(out)
	return err
}

// doqUpstream resolves over DNS-over-QUIC, keeping one connection open and
// opening a stream per query. The connection is redialled when it dies.
type doqUpstream struct {
	addr string
	tls  *tls.Config
	conn atomic.Pointer[quic.Conn]
	dial sync.Mutex // serialises redials only; queries never hold it
}

func newDoQUpstream(addr string, roots *x509.CertPool) (*doqUpstream, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("doq upstream %q: %w", addr, err)
	}
	return &doqUpstream{addr: addr, tls: &tls.Config{
		ServerName: host,
		NextProtos: []string{doqALPN},
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
	}}, nil
}

func (d *doqUpstream) Name() string { return d.addr }

// Close ends the open connection, if any (a temporary upstream built for a
// Settings test).
func (d *doqUpstream) Close() error {
	if c := d.conn.Swap(nil); c != nil {
		return c.CloseWithError(doqNoError, "")
	}
	return nil
}

// connection returns a live connection, dialling one if there is none or
// the last one died. stale is a connection the caller found broken.
func (d *doqUpstream) connection(ctx context.Context, stale *quic.Conn) (*quic.Conn, error) {
	if c := d.conn.Load(); c != nil && c != stale && c.Context().Err() == nil {
		return c, nil
	}
	d.dial.Lock()
	defer d.dial.Unlock()
	if c := d.conn.Load(); c != nil && c != stale && c.Context().Err() == nil {
		return c, nil // another query redialled while this one waited
	}
	c, err := quic.DialAddr(ctx, d.addr, d.tls, doqQUICConfig())
	if err != nil {
		return nil, err
	}
	if old := d.conn.Swap(c); old != nil {
		_ = old.CloseWithError(doqNoError, "")
	}
	return c, nil
}

func (d *doqUpstream) Exchange(ctx context.Context, msg *dns.Msg) (*dns.Msg, error) {
	wire := msg.Copy()
	wire.Id = 0
	var stale *quic.Conn
	for attempt := 0; attempt < 2; attempt++ {
		conn, err := d.connection(ctx, stale)
		if err != nil {
			return nil, fmt.Errorf("doq dial: %w", err)
		}
		resp, err := d.exchangeOn(ctx, conn, wire)
		if err == nil {
			resp.Id = msg.Id
			return resp, nil
		}
		// A connection the server closed or that idled out fails at the
		// first stream; one fresh connection is worth trying.
		if ctx.Err() != nil || conn.Context().Err() == nil {
			return nil, err
		}
		stale = conn
	}
	return nil, errors.New("doq: connection lost")
}

func (d *doqUpstream) exchangeOn(ctx context.Context, conn *quic.Conn, wire *dns.Msg) (*dns.Msg, error) {
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("doq stream: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = st.SetDeadline(dl)
	} else {
		_ = st.SetDeadline(time.Now().Add(upstreamTimeout))
	}
	if err := writeDoQMessage(st, wire); err != nil {
		st.CancelRead(doqInternalError)
		return nil, fmt.Errorf("doq write: %w", err)
	}
	_ = st.Close() // one query per stream: close our side
	resp, err := readDoQMessage(st)
	if err != nil {
		return nil, fmt.Errorf("doq read: %w", err)
	}
	return resp, nil
}

// newDoH3Upstream is DoH over HTTP/3: the same RFC 8484 exchange as
// dohUpstream, on a QUIC transport.
func newDoH3Upstream(url string, roots *x509.CertPool) *dohUpstream {
	return &dohUpstream{
		url: url,
		client: &http.Client{
			Timeout: upstreamTimeout,
			Transport: &http3.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots},
				QUICConfig:      doqQUICConfig(),
			},
		},
	}
}
