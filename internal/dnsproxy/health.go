package dnsproxy

import (
	"context"
	"fmt"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// Health probes. forward() asks upstreams in order and stops at the first
// answer, so while the primary answers, a backup is never asked anything —
// and a tripped breaker only clears on a successful exchange. A backup that
// failed once alongside the primary (a network blip takes out both) stayed
// "failing" for good, and the Needs attention line with it (field bug,
// October 2026). This loop probes, off the query path, every upstream whose
// cooldown has lapsed, and re-checks backups no query has used for a while,
// so a light reflects the resolver rather than the traffic pattern.

// probeTick is how often the loop looks for an upstream to probe; a variable
// so tests can shrink it.
var probeTick = 15 * time.Second

// idleRecheck: an upstream no query (or probe) has reached for this long is
// probed, so a dead backup is noticed before the day it is needed.
var idleRecheck = 30 * time.Minute

// probeQuestion is what a probe asks: the root's NS set, which every
// recursive resolver can answer and keeps cached.
func probeQuestion() *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(".", dns.TypeNS)
	m.RecursionDesired = true
	return m
}

// runHealthProbes probes due upstreams every tick until ctx ends, then
// closes done. The timing is passed in, read by Start: the loop never reads
// the package variables tests adjust.
func (s *Server) runHealthProbes(ctx context.Context, tick time.Duration, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.probeDue(ctx, time.Now())
	}
}

// probeDue probes each default upstream that needs it. Routes are left
// alone: they are authoritative and answered only for their own zones.
func (s *Server) probeDue(ctx context.Context, now time.Time) {
	for _, up := range s.fwd.Load().defaults {
		if ctx.Err() != nil {
			return
		}
		if s.probeWanted(up.Name(), now) {
			_, _, _ = s.probe(ctx, up, true)
		}
	}
}

// probeWanted reports whether an upstream is due a probe. A tripped one is
// claimed exactly as available() claims its half-open query, so a probe and
// a real query never both test it in one cooldown.
func (s *Server) probeWanted(name string, now time.Time) bool {
	c := s.counters(name)
	if until := c.sickUntil.Load(); until != 0 {
		if now.UnixNano() < until {
			return false
		}
		return c.sickUntil.CompareAndSwap(until, now.Add(failoverCooldown).UnixNano())
	}
	last := max(c.lastSuccess.Load(), c.lastFailure.Load())
	return last == 0 || now.UnixNano()-last >= s.probeIdle.Load()
}

// probe sends one test question and, when track is set, records the result
// like any exchange — a success clears a tripped breaker (and announces the
// recovery), a failure counts towards one.
func (s *Server) probe(ctx context.Context, up Upstream, track bool) (time.Duration, int, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamTimeout+time.Second)
	defer cancel()
	began := time.Now()
	resp, err := up.Exchange(ctx, probeQuestion())
	took := time.Since(began)
	if track {
		s.trackUpstream(up.Name(), took, err)
	}
	if err != nil {
		return took, 0, err
	}
	return took, resp.Rcode, nil
}

// UpstreamTest is the outcome of TestUpstream.
type UpstreamTest struct {
	OK    bool
	RTT   time.Duration
	Rcode string // the answer's response code, e.g. NOERROR
	Err   string
	// Configured: the upstream is one Minos uses, so the result also
	// updated its health (a success turns a failing light green).
	Configured bool
}

// TestUpstream asks u one test question now — the Settings "Test" button.
// A configured upstream is tested through the instance queries use, so the
// result counts towards its health; an unsaved one is built just for the
// test and changes nothing.
func (s *Server) TestUpstream(ctx context.Context, u config.Upstream) UpstreamTest {
	var up Upstream
	configured := false
	if ft := s.fwd.Load(); ft != nil {
		for _, d := range ft.defaults {
			if d.Name() == u.Address {
				up, configured = d, true
				break
			}
		}
	}
	if up == nil {
		built, err := NewUpstream(u)
		if err != nil {
			return UpstreamTest{Err: err.Error()}
		}
		up = built
		if c, ok := built.(interface{ Close() error }); ok {
			defer func() { _ = c.Close() }()
		}
	}
	rtt, rcode, err := s.probe(ctx, up, configured)
	if err != nil {
		return UpstreamTest{RTT: rtt, Err: err.Error(), Configured: configured}
	}
	name, ok := dns.RcodeToString[rcode]
	if !ok {
		name = fmt.Sprintf("RCODE%d", rcode)
	}
	return UpstreamTest{OK: true, RTT: rtt, Rcode: name, Configured: configured}
}
