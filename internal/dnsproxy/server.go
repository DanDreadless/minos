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
	"net/http"
	"sort"
	"strings"
	"sync"
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

	listen  string
	udp     *dns.Server
	tcp     *dns.Server
	udpAddr net.Addr

	// Client-facing encrypted DNS; nil when dns.tls is not configured.
	// File-only settings like the plain listen address: restart to change.
	tlsListeners config.TLSListeners
	dot          *dns.Server
	doh          *http.Server
	dotAddr      net.Addr
	dohAddr      net.Addr
	policy       atomic.Pointer[blockingPolicy]
	fwd          atomic.Pointer[forwardTable]
	local        atomic.Pointer[localZone] // nil when no local records

	// cache is nil when disabled. Hit/miss counters live on the Server so
	// they survive the cache flush that every config change performs.
	cache       atomic.Pointer[dnsCache]
	cacheHits   atomic.Uint64
	cacheMisses atomic.Uint64

	// safeSearch is the global blocking.safe_search flag; per-group
	// enforcement rides the client policy.
	safeSearch atomic.Bool
	// forwardPrivateArpa opts out of answering RFC 6303 private reverse
	// zones locally (dns.forward_private_reverse).
	forwardPrivateArpa atomic.Bool

	// upstreamStats accumulates per-upstream counters for /metrics,
	// keyed by upstream name so they survive config swaps.
	upstreamStats sync.Map // string → *upstreamCounters

	// inflight collapses concurrent identical forwards (and stale
	// refreshes) into one upstream exchange, keyed like the cache.
	inflight sync.Map // string → *inflightCall
}

// inflightCall is one in-progress upstream exchange; followers wait on
// done and copy the leader's result.
type inflightCall struct {
	done     chan struct{}
	resp     *dns.Msg // a private copy; nil on error
	upstream string
	err      error
}

// forwardDedup forwards req, collapsing concurrent identical queries: the
// first caller (leader) exchanges upstream and caches the answer; everyone
// else waits and receives a copy. shared reports a follower result.
func (s *Server) forwardDedup(ctx context.Context, req *dns.Msg, qname, key string, cache *dnsCache) (resp *dns.Msg, upstream string, shared bool, err error) {
	call := &inflightCall{done: make(chan struct{})}
	if v, loaded := s.inflight.LoadOrStore(key, call); loaded {
		prior := v.(*inflightCall)
		select {
		case <-prior.done:
			if prior.err != nil {
				return nil, prior.upstream, true, prior.err
			}
			resp := prior.resp.Copy()
			// The leader's question may differ in case (0x20); answer
			// under the follower's own casing, as the cache does.
			resp.Question = req.Question
			return resp, prior.upstream, true, nil
		case <-ctx.Done():
			return nil, "", true, ctx.Err()
		}
	}
	defer s.inflight.Delete(key)
	r, name, routed, err := s.forward(ctx, req, qname)
	if err == nil {
		if cache != nil && !routed {
			cache.put(key, r)
		}
		// Followers copy from a private snapshot: the caller mutates r
		// (ID stamping) after this returns.
		call.resp = r.Copy()
	}
	call.upstream, call.err = name, err
	close(call.done)
	return r, name, false, err
}

// refreshStale re-resolves a stale cache key in the background, deduped
// through the same inflight table so a burst of stale hits costs one
// upstream exchange.
func (s *Server) refreshStale(key, qname string, qtype uint16) {
	if _, busy := s.inflight.Load(key); busy {
		return // a refresh or live query is already on it
	}
	go func() {
		req := new(dns.Msg)
		req.SetQuestion(dns.Fqdn(qname), qtype)
		if strings.HasSuffix(key, "|d") {
			req.SetEdns0(1232, true)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*upstreamTimeout)
		defer cancel()
		_, _, _, _ = s.forwardDedup(ctx, req, qname, key, s.cache.Load())
	}()
}

// Failover health: an upstream that fails failThreshold consecutive
// exchanges is sidestepped for failoverCooldown — healthy upstreams answer
// first and the sick one becomes a last resort. When the cooldown lapses,
// exactly one query (CAS-elected) probes it; success resets the breaker.
// Only transport errors count — SERVFAIL is an answer, not a failure.
const failThreshold = 3

// failoverCooldown is a variable so tests can shrink it.
var failoverCooldown = 30 * time.Second

type upstreamCounters struct {
	requests atomic.Uint64
	failures atomic.Uint64
	// durationNs sums exchange time (successes and failures alike) so a
	// scraper can derive average latency from sum/count.
	durationNs atomic.Int64
	// consecutiveFails trips the breaker at failThreshold; sickUntil is
	// unix nanos until which the upstream is sidestepped (0 = healthy).
	consecutiveFails atomic.Int32
	sickUntil        atomic.Int64
}

// UpstreamStat is one upstream's counters, for the metrics endpoint.
type UpstreamStat struct {
	Name            string
	Requests        uint64
	Failures        uint64
	DurationSeconds float64
	Sick            bool
}

func (s *Server) counters(name string) *upstreamCounters {
	v, ok := s.upstreamStats.Load(name)
	if !ok {
		v, _ = s.upstreamStats.LoadOrStore(name, &upstreamCounters{})
	}
	return v.(*upstreamCounters)
}

// trackUpstream records one exchange attempt against an upstream.
func (s *Server) trackUpstream(name string, took time.Duration, err error) {
	c := s.counters(name)
	c.requests.Add(1)
	c.durationNs.Add(took.Nanoseconds())
	if err != nil {
		c.failures.Add(1)
		if c.consecutiveFails.Add(1) >= failThreshold {
			c.sickUntil.Store(time.Now().Add(failoverCooldown).UnixNano())
		}
		return
	}
	c.consecutiveFails.Store(0)
	c.sickUntil.Store(0)
}

// available reports whether an upstream should be tried in the first pass.
// A sick upstream whose cooldown has lapsed admits exactly one caller (the
// CAS winner) as the half-open probe; everyone else keeps sidestepping it.
func (s *Server) available(name string, now time.Time) bool {
	c := s.counters(name)
	until := c.sickUntil.Load()
	if until == 0 {
		return true
	}
	if now.UnixNano() < until {
		return false
	}
	// Cooldown lapsed: winner probes, losers wait out a fresh cooldown.
	return c.sickUntil.CompareAndSwap(until, now.Add(failoverCooldown).UnixNano())
}

// UpstreamStats snapshots the per-upstream counters, sorted by name.
func (s *Server) UpstreamStats() []UpstreamStat {
	now := time.Now().UnixNano()
	var out []UpstreamStat
	s.upstreamStats.Range(func(k, v any) bool {
		c := v.(*upstreamCounters)
		until := c.sickUntil.Load()
		out = append(out, UpstreamStat{
			Name:            k.(string),
			Requests:        c.requests.Load(),
			Failures:        c.failures.Load(),
			DurationSeconds: float64(c.durationNs.Load()) / 1e9,
			Sick:            until != 0 && now < until,
		})
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func New(cfg *config.Config, engine *filter.Engine, qlog *querylog.Log, reg *clients.Registry) (*Server, error) {
	s := &Server{
		engine:       engine,
		qlog:         qlog,
		clients:      reg,
		listen:       cfg.DNS.Listen,
		tlsListeners: cfg.DNS.TLS,
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
	ft := &forwardTable{}
	for _, u := range cfg.DNS.Upstreams {
		built, err := NewUpstream(u)
		if err != nil {
			return fmt.Errorf("build upstream: %w", err)
		}
		ft.defaults = append(ft.defaults, built)
	}
	if len(cfg.DNS.Routes) > 0 {
		ft.routes = make(map[string]Upstream)
		for _, r := range cfg.DNS.Routes {
			built, err := NewUpstream(r.Upstream)
			if err != nil {
				return fmt.Errorf("build route upstream: %w", err)
			}
			for _, d := range r.Domains {
				ft.routes[filter.NormalizeDomain(d)] = built
			}
		}
	}
	s.fwd.Store(ft)
	s.policy.Store(&blockingPolicy{
		nxdomain: cfg.Blocking.Mode == "nxdomain",
		blockTTL: cfg.DNS.BlockTTL,
	})
	s.local.Store(buildLocalZone(cfg))
	s.safeSearch.Store(cfg.Blocking.SafeSearch)
	s.forwardPrivateArpa.Store(cfg.DNS.ForwardPrivateReverse)
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
	if err := s.startEncrypted(handler); err != nil {
		pc.Close()
		ln.Close()
		return err
	}
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
	if err := s.shutdownEncrypted(ctx); err != nil && firstErr == nil {
		firstErr = err
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

	// Local records answer before any filtering: an explicit record beats
	// the blocklists, and local names never leak upstream. (A device-level
	// DNS block still wins — it returned above.)
	if z := s.local.Load(); z != nil {
		if reply, rule, ok := z.answer(req, q, qname); ok {
			_ = w.WriteMsg(reply)
			entry.Verdict = querylog.VerdictAllowed
			entry.List = "local"
			entry.Rule = rule
			entry.Upstream = "local"
			s.record(entry, start)
			return
		}
	}

	// Private reverse zones (RFC 6303) stay on the network: without a
	// covering local record (above) or conditional route, answer the
	// empty zone here. Applies to bypass devices too — this is resolver
	// correctness, not content filtering; upstreams can't know your LAN.
	if !s.forwardPrivateArpa.Load() {
		if zone := matchPrivateArpa(qname); zone != "" && s.fwd.Load().route(qname) == nil {
			_ = w.WriteMsg(privateArpaAnswer(req, q, qname, zone))
			entry.Verdict = querylog.VerdictAllowed
			entry.List = "local"
			entry.Rule = "private reverse zone"
			entry.Upstream = "local"
			s.record(entry, start)
			return
		}
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

	// Safe search rewrites matched search domains for enforced clients.
	// Blocklist verdicts already won above; bypass devices are exempt.
	if !pol.Bypasses() && (s.safeSearch.Load() || (pol != nil && pol.SafeSearch)) {
		if target, ok := safeSearchHosts[qname]; ok {
			if s.answerSafeSearch(w, req, q, target) {
				entry.Verdict = querylog.VerdictAllowed
				entry.List = "safesearch"
				entry.Rule = target
				entry.Upstream = "safesearch"
				s.record(entry, start)
				return
			}
		}
	}

	// Cache lookup happens after judgment so verdicts always reflect the
	// live rules; only forwarded answers are cached. Load once so the get
	// and the leader's put use the same instance across a concurrent flush.
	var cache *dnsCache
	if q.Qclass == dns.ClassINET {
		cache = s.cache.Load()
	}
	key := cacheKey(qname, q.Qtype, req) // also the dedup key when uncached
	if cache != nil {
		if resp, stale := cache.get(key, req); resp != nil {
			if stale {
				// RFC 8767: serve the expired answer now, refresh behind it.
				s.refreshStale(key, qname, q.Qtype)
				entry.Upstream = "stale"
			} else {
				entry.Upstream = "cache"
			}
			s.cacheHits.Add(1)
			_ = w.WriteMsg(resp)
			entry.Verdict = querylog.VerdictAllowed
			s.record(entry, start)
			return
		}
		s.cacheMisses.Add(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*upstreamTimeout)
	defer cancel()
	resp, upstreamName, _, err := s.forwardDedup(ctx, req, qname, key, cache)
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

// forwardTable is the per-swap snapshot of where queries go: the default
// upstream order plus conditional routes keyed by domain.
type forwardTable struct {
	defaults []Upstream
	routes   map[string]Upstream // nil when no routes configured
}

// route returns the upstream owning qname or a parent of it, walking label
// suffixes so "printer.lan" matches a route for "lan".
func (ft *forwardTable) route(qname string) Upstream {
	if ft.routes == nil {
		return nil
	}
	if up, ok := ft.routes[qname]; ok {
		return up
	}
	for i := 0; i < len(qname); i++ {
		if qname[i] == '.' {
			if up, ok := ft.routes[qname[i+1:]]; ok {
				return up
			}
		}
	}
	return nil
}

// forward resolves req: a matching route is authoritative for its domains
// (no fallback — a failing router answers SERVFAIL); everything else tries
// the default upstreams in order. routed answers are reported so the caller
// can skip caching them: LAN records are fast anyway and change often.
func (s *Server) forward(ctx context.Context, req *dns.Msg, qname string) (resp *dns.Msg, upstream string, routed bool, err error) {
	ft := s.fwd.Load()
	if up := ft.route(qname); up != nil {
		began := time.Now()
		resp, err := up.Exchange(ctx, req)
		s.trackUpstream(up.Name(), time.Since(began), err)
		if err != nil {
			return nil, up.Name(), true, fmt.Errorf("routed upstream failed: %w", err)
		}
		return resp, up.Name(), true, nil
	}
	// Two passes: available upstreams in config order first, then any
	// sidestepped (sick) ones as a last resort — a tripped breaker must
	// never turn "primary is down" into "refuse to try the secondary",
	// nor "everything is sick" into "refuse to try at all".
	now := time.Now()
	var lastErr error
	var lastName string
	var tried uint64 // bitmask over ft.defaults indices (pass 0 attempts)
	for pass := 0; pass < 2; pass++ {
		for i, up := range ft.defaults {
			if pass == 0 {
				if !s.available(up.Name(), now) {
					continue
				}
				if i < 64 {
					tried |= 1 << i
				}
			} else if i < 64 && tried&(1<<i) != 0 {
				continue // already attempted this query
			}
			began := time.Now()
			resp, err := up.Exchange(ctx, req)
			s.trackUpstream(up.Name(), time.Since(began), err)
			if err == nil {
				return resp, up.Name(), false, nil
			}
			lastErr = err
			lastName = up.Name()
			if ctx.Err() != nil {
				pass = 2 // context gone: no second pass
				break
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstreams configured")
	}
	return nil, lastName, false, fmt.Errorf("all upstreams failed: %w", lastErr)
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
