package dnsproxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/miekg/dns"
)

// maxDoHRequest caps an incoming DoH body; a DNS message cannot legally
// exceed 64 KiB. Message validation and size limits are not optional in a
// resolver exposed beyond plain UDP.
const maxDoHRequest = 64 << 10

// SetCertSource plugs in a dynamic certificate callback (the ACME manager)
// instead of static cert/key files. Call before Start. Because every
// handshake consults the source, renewals apply live — no restart.
func (s *Server) SetCertSource(fn func(*tls.ClientHelloInfo) (*tls.Certificate, error)) {
	s.getCert = fn
}

// startEncrypted brings up the configured DoT/DoH listeners. Both feed the
// exact same handler as the plaintext listeners, so device policies, local
// records, Safe Search, the cache, and the query log all apply unchanged.
func (s *Server) startEncrypted(handler dns.Handler) error {
	t := s.tlsListeners
	if !t.Enabled() {
		return nil
	}
	getCert := s.getCert
	if getCert == nil {
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return fmt.Errorf("load dns.tls certificate: %w", err)
		}
		getCert = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return &cert, nil
		}
	}
	tcfg := &tls.Config{
		GetCertificate: getCert,
		MinVersion:     tls.VersionTLS12,
	}

	if t.DoTListen != "" {
		ln, err := tls.Listen("tcp", t.DoTListen, tcfg)
		if err != nil {
			return fmt.Errorf("bind dot %s: %w", t.DoTListen, err)
		}
		s.dotAddr = ln.Addr()
		s.dot = &dns.Server{Listener: ln, Handler: handler}
		go func() {
			if err := s.dot.ActivateAndServe(); err != nil {
				slog.Error("dot server stopped", "err", err)
			}
		}()
		slog.Info("dot server listening", "addr", s.dotAddr.String())
	}

	if t.DoHListen != "" {
		ln, err := net.Listen("tcp", t.DoHListen)
		if err != nil {
			return fmt.Errorf("bind doh %s: %w", t.DoHListen, err)
		}
		s.dohAddr = ln.Addr()
		mux := http.NewServeMux()
		mux.HandleFunc("/dns-query", s.serveDoH)
		s.doh = &http.Server{
			Handler:           mux,
			TLSConfig:         tcfg.Clone(), // ServeTLS adds h2 to NextProtos
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
		}
		go func() {
			if err := s.doh.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
				slog.Error("doh server stopped", "err", err)
			}
		}()
		slog.Info("doh server listening", "addr", s.dohAddr.String(), "path", "/dns-query")
	}
	return nil
}

// serveDoH implements RFC 8484: GET with ?dns=<base64url> and POST with an
// application/dns-message body, both answered through the normal pipeline.
func (s *Server) serveDoH(w http.ResponseWriter, r *http.Request) {
	var body []byte
	var err error
	switch r.Method {
	case http.MethodGet:
		b64 := r.URL.Query().Get("dns")
		if b64 == "" {
			http.Error(w, "missing dns query parameter", http.StatusBadRequest)
			return
		}
		body, err = base64.RawURLEncoding.DecodeString(b64)
		if err != nil {
			http.Error(w, "dns parameter is not base64url", http.StatusBadRequest)
			return
		}
	case http.MethodPost:
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "Content-Type must be application/dns-message", http.StatusUnsupportedMediaType)
			return
		}
		body, err = io.ReadAll(io.LimitReader(r.Body, maxDoHRequest+1))
		if err != nil {
			http.Error(w, "read request", http.StatusBadRequest)
			return
		}
		if len(body) > maxDoHRequest {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req := new(dns.Msg)
	if err := req.Unpack(body); err != nil {
		http.Error(w, "malformed DNS message", http.StatusBadRequest)
		return
	}

	dw := &dohResponseWriter{remote: httpRemoteAddr(r)}
	s.handle(dw, req)
	if dw.msg == nil {
		http.Error(w, "no response", http.StatusInternalServerError)
		return
	}
	packed, err := dw.msg.Pack()
	if err != nil {
		http.Error(w, "pack response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	// RFC 8484 §5.1: HTTP caching honors the answer's remaining TTL.
	if ttl, ok := minAnswerTTL(dw.msg); ok {
		w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", ttl))
	}
	_, _ = w.Write(packed)
}

func minAnswerTTL(m *dns.Msg) (uint32, bool) {
	if len(m.Answer) == 0 {
		return 0, false
	}
	ttl := m.Answer[0].Header().Ttl
	for _, rr := range m.Answer[1:] {
		if t := rr.Header().Ttl; t < ttl {
			ttl = t
		}
	}
	return ttl, true
}

// httpRemoteAddr converts an HTTP client address into a net.Addr so the
// device registry attributes DoH queries to the right IP.
func httpRemoteAddr(r *http.Request) net.Addr {
	host, port, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return &net.TCPAddr{}
	}
	ip := net.ParseIP(host)
	p, _ := net.LookupPort("tcp", port)
	return &net.TCPAddr{IP: ip, Port: p}
}

// dohResponseWriter adapts the DNS handler's dns.ResponseWriter interface
// onto a captured message, so DoH requests reuse handle() unchanged.
type dohResponseWriter struct {
	remote net.Addr
	msg    *dns.Msg
}

func (w *dohResponseWriter) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (w *dohResponseWriter) RemoteAddr() net.Addr { return w.remote }

func (w *dohResponseWriter) WriteMsg(m *dns.Msg) error {
	w.msg = m
	return nil
}

func (w *dohResponseWriter) Write(b []byte) (int, error) {
	m := new(dns.Msg)
	if err := m.Unpack(b); err != nil {
		return 0, err
	}
	w.msg = m
	return len(b), nil
}

func (w *dohResponseWriter) Close() error        { return nil }
func (w *dohResponseWriter) TsigStatus() error   { return nil }
func (w *dohResponseWriter) TsigTimersOnly(bool) {}
func (w *dohResponseWriter) Hijack()             {}

// DoTAddr and DoHAddr return the bound encrypted-listener addresses
// (nil when disabled); used by tests listening on ":0".
func (s *Server) DoTAddr() net.Addr { return s.dotAddr }
func (s *Server) DoHAddr() net.Addr { return s.dohAddr }

// shutdownEncrypted stops the TLS listeners as part of Server.Shutdown.
func (s *Server) shutdownEncrypted(ctx context.Context) error {
	var firstErr error
	if s.dot != nil {
		if err := s.dot.ShutdownContext(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.doh != nil {
		if err := s.doh.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
