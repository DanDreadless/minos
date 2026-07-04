// Package dnsproxy is the judgment seat: it receives DNS queries, has the
// filter engine judge them, and either synthesizes a blocked response or
// forwards to an upstream resolver. This file is the query hot path — keep
// allocations down and never touch disk or take locks here.
package dnsproxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/querylog"
)

// blockingPolicy is the per-swap snapshot of how to answer condemned
// queries; it changes atomically with config updates.
type blockingPolicy struct {
	nxdomain bool
	blockTTL uint32
}

// Server listens on UDP+TCP and serves judged queries.
type Server struct {
	engine  *filter.Engine
	qlog    *querylog.Log
	clients *clients.Registry

	listen    string
	udp       *dns.Server
	tcp       *dns.Server
	udpAddr   net.Addr
	policy    atomic.Pointer[blockingPolicy]
	upstreams atomic.Pointer[[]Upstream]

	// cache is nil when disabled. Hit/miss counters live on the Server so
	// they survive the cache flush that every config change performs.
	cache       atomic.Pointer[dnsCache]
	cacheHits   atomic.Uint64
	cacheMisses atomic.Uint64
}

func New(cfg *config.Config, engine *filter.Engine, qlog *querylog.Log, reg *clients.Registry) (*Server, error) {
	s := &Server{
		engine:  engine,
		qlog:    qlog,
		clients: reg,
		listen:  cfg.DNS.Listen,
	}
	if err := s.ApplyConfig(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// ApplyConfig atomically swaps blocking policy and upstream set. Safe while
// serving; wired to config.Store.OnChange so settings never need a restart.
// (The listen address is the one exception: changing it requires a restart,
// and the API rejects that edit.)
func (s *Server) ApplyConfig(cfg *config.Config) error {
	ups := make([]Upstream, 0, len(cfg.DNS.Upstreams))
	for _, u := range cfg.DNS.Upstreams {
		built, err := NewUpstream(u)
		if err != nil {
			return fmt.Errorf("build upstream: %w", err)
		}
		ups = append(ups, built)
	}
	s.upstreams.Store(&ups)
	s.policy.Store(&blockingPolicy{
		nxdomain: cfg.Blocking.Mode == "nxdomain",
		blockTTL: cfg.DNS.BlockTTL,
	})
	// A fresh cache on every config change doubles as the flush that keeps
	// cached answers consistent with new upstreams or blocking settings.
	if cfg.DNS.Cache.Enabled {
		s.cache.Store(newCache(cfg.DNS.Cache))
	} else {
		s.cache.Store(nil)
	}
	return nil
}

// CacheStats reports response-cache counters for the status API.
func (s *Server) CacheStats() (hits, misses uint64, entries int64, enabled bool) {
	c := s.cache.Load()
	if c == nil {
		return s.cacheHits.Load(), s.cacheMisses.Load(), 0, false
	}
	return s.cacheHits.Load(), s.cacheMisses.Load(), c.size.Load(), true
}

// Start binds UDP and TCP listeners and begins serving. It returns once
// both listeners are active.
func (s *Server) Start() error {
	pc, err := net.ListenPacket("udp", s.listen)
	if err != nil {
		return fmt.Errorf("bind udp %s: %w", s.listen, err)
	}
	// Bind TCP to the port UDP actually got, so listening on ":0" (tests)
	// still yields one address serving both transports.
	tcpAddr := s.listen
	if udpAddr, ok := pc.LocalAddr().(*net.UDPAddr); ok {
		tcpAddr = udpAddr.String()
	}
	ln, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		pc.Close()
		return fmt.Errorf("bind tcp %s: %w", tcpAddr, err)
	}
	s.udpAddr = pc.LocalAddr()
	handler := dns.HandlerFunc(s.handle)
	s.udp = &dns.Server{PacketConn: pc, Handler: handler}
	s.tcp = &dns.Server{Listener: ln, Handler: handler}
	go func() {
		if err := s.udp.ActivateAndServe(); err != nil {
			slog.Error("udp dns server stopped", "err", err)
		}
	}()
	go func() {
		if err := s.tcp.ActivateAndServe(); err != nil {
			slog.Error("tcp dns server stopped", "err", err)
		}
	}()
	slog.Info("dns server listening", "addr", s.udpAddr.String())
	return nil
}

// UDPAddr returns the bound UDP address (useful when listening on port 0).
func (s *Server) UDPAddr() net.Addr { return s.udpAddr }

func (s *Server) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, srv := range []*dns.Server{s.udp, s.tcp} {
		if srv == nil {
			continue
		}
		if err := srv.ShutdownContext(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// handle is the hot path: judge, then answer or forward.
func (s *Server) handle(w dns.ResponseWriter, req *dns.Msg) {
	start := time.Now()
	// Refuse anything malformed or multi-question outright; validation is
	// not optional in a resolver exposed to a LAN.
	if len(req.Question) != 1 || req.Response {
		reply := new(dns.Msg)
		reply.SetRcode(req, dns.RcodeFormatError)
		_ = w.WriteMsg(reply)
		return
	}
	q := req.Question[0]
	qname := filter.NormalizeDomain(q.Name)
	if qname == "" {
		reply := new(dns.Msg)
		reply.SetRcode(req, dns.RcodeFormatError)
		_ = w.WriteMsg(reply)
		return
	}

	entry := querylog.Entry{
		Time:   start,
		Client: clientIP(w.RemoteAddr()),
		QName:  qname,
		QType:  dns.TypeToString[q.Qtype],
	}

	// Per-device policy: one atomic map read; nil means the default rules.
	pol := s.clients.PolicyFor(entry.Client)
	if pol.Refuses() {
		// Device-level DNS block. Deliberately unaffected by recess: it is
		// access control, not content filtering.
		reply := new(dns.Msg)
		reply.SetRcode(req, dns.RcodeRefused)
		_ = w.WriteMsg(reply)
		entry.Verdict = querylog.VerdictBlocked
		entry.List = "clients"
		entry.Rule = "dns access blocked"
		s.record(entry, start)
		return
	}

	var verdict filter.Result
	if pol.Bypasses() {
		entry.List = "group:" + pol.Group
		entry.Rule = "bypass"
	} else {
		// A filter-mode group's overlay is judged first (its pardons beat
		// global denies, its sentences add blocks); recess silences both.
		if pol != nil && pol.Overlay != nil {
			if paused, _ := s.engine.Paused(); !paused {
				if res := pol.Overlay.Match(qname); res.Rule != "" {
					verdict = res
				}
			}
		}
		if verdict.Rule == "" {
			verdict = s.engine.Match(qname)
		}
	}

	if verdict.Blocked {
		reply := s.blockedResponse(req, q)
		_ = w.WriteMsg(reply)
		entry.Verdict = querylog.VerdictBlocked
		entry.List = verdict.List
		entry.Rule = verdict.Rule
		s.record(entry, start)
		return
	}

	// Cache lookup happens after judgment so verdicts always reflect the
	// live rules; only forwarded answers are cached. Load once so the get
	// and the later put use the same instance across a concurrent flush.
	var (
		cache *dnsCache
		key   string
	)
	if q.Qclass == dns.ClassINET {
		cache = s.cache.Load()
	}
	if cache != nil {
		key = cacheKey(qname, q.Qtype, req)
		if resp := cache.get(key, req); resp != nil {
			s.cacheHits.Add(1)
			_ = w.WriteMsg(resp)
			entry.Verdict = querylog.VerdictAllowed
			entry.Upstream = "cache"
			s.record(entry, start)
			return
		}
		s.cacheMisses.Add(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*upstreamTimeout)
	defer cancel()
	resp, upstreamName, err := s.forward(ctx, req)
	if err != nil {
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("forward failed", "qname", qname, "err", err)
		}
		reply := new(dns.Msg)
		reply.SetRcode(req, dns.RcodeServerFailure)
		_ = w.WriteMsg(reply)
		entry.Verdict = querylog.VerdictAllowed
		entry.Upstream = upstreamName
		s.record(entry, start)
		return
	}
	resp.Id = req.Id
	if cache != nil {
		cache.put(key, resp)
	}
	_ = w.WriteMsg(resp)
	entry.Verdict = querylog.VerdictAllowed
	entry.Upstream = upstreamName
	s.record(entry, start)
}

// record finalizes a query log entry and updates the device registry.
func (s *Server) record(e querylog.Entry, start time.Time) {
	e.DurationMs = msSince(start)
	s.qlog.Record(e)
	s.clients.Touch(e.Client, e.Verdict == querylog.VerdictBlocked, e.Time)
}

// forward tries each upstream in order until one answers.
func (s *Server) forward(ctx context.Context, req *dns.Msg) (*dns.Msg, string, error) {
	ups := *s.upstreams.Load()
	var lastErr error
	var lastName string
	for _, up := range ups {
		resp, err := up.Exchange(ctx, req)
		if err == nil {
			return resp, up.Name(), nil
		}
		lastErr = err
		lastName = up.Name()
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstreams configured")
	}
	return nil, lastName, fmt.Errorf("all upstreams failed: %w", lastErr)
}

// blockedResponse synthesizes the condemned answer per policy: NXDOMAIN,
// or 0.0.0.0/:: for A/AAAA and an empty NOERROR for other types.
func (s *Server) blockedResponse(req *dns.Msg, q dns.Question) *dns.Msg {
	pol := s.policy.Load()
	reply := new(dns.Msg)
	if pol.nxdomain {
		reply.SetRcode(req, dns.RcodeNameError)
		return reply
	}
	reply.SetReply(req)
	hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: pol.blockTTL}
	switch q.Qtype {
	case dns.TypeA:
		hdr.Rrtype = dns.TypeA
		reply.Answer = []dns.RR{&dns.A{Hdr: hdr, A: net.IPv4zero}}
	case dns.TypeAAAA:
		hdr.Rrtype = dns.TypeAAAA
		reply.Answer = []dns.RR{&dns.AAAA{Hdr: hdr, AAAA: net.IPv6zero}}
	}
	return reply
}

func clientIP(addr net.Addr) string {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP.String()
	case *net.TCPAddr:
		return a.IP.String()
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return addr.String()
		}
		return host
	}
}

func msSince(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
