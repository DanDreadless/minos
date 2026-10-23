package dnsproxy

import (
	"github.com/miekg/dns"

	"minos/internal/clients"
	"minos/internal/filter"
)

// CNAME cloaking: a tracker hides behind a first-party name —
// metrics.shop.example is a CNAME to shop.tracker-cdn.example — so a
// blocklist that names the tracker never sees it asked for. Minos judges
// every CNAME target in a forwarded answer the way it judged the name
// itself, and refuses the answer when one is blocked
// (blocking.cname_inspection, on by default).

// judge is the verdict for name under the device's policy: the group's own
// rules first (its allowed domains beat global blocks, its blocked domains
// add to them; pausing silences both), then the global rules. The caller
// handles bypass devices, which are never judged.
func (s *Server) judge(name string, pol *clients.Policy) filter.Result {
	if pol != nil && pol.Overlay != nil {
		if paused, _ := s.engine.Paused(); !paused {
			if res := pol.Overlay.Match(name); res.Rule != "" {
				return res
			}
		}
	}
	return s.engine.Match(name)
}

// cloakedTarget returns the first CNAME target in resp that is blocked,
// and the verdict that blocks it. Names an allow rule decided are not
// inspected: the user allowed the name, and its CNAMEs come with it.
func (s *Server) cloakedTarget(resp *dns.Msg, verdict filter.Result, pol *clients.Policy) (string, filter.Result) {
	if !s.cnameInspect.Load() || verdict.Rule != "" || pol.Bypasses() {
		return "", filter.Result{}
	}
	for _, rr := range resp.Answer {
		c, ok := rr.(*dns.CNAME)
		if !ok {
			continue
		}
		target := filter.NormalizeDomain(c.Target)
		if target == "" {
			continue
		}
		if res := s.judge(target, pol); res.Blocked {
			return target, res
		}
	}
	return "", filter.Result{}
}
