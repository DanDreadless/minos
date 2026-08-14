package dnsproxy

// DNSSEC wiring (dns.dnssec): validation runs on the forwardDedup leader
// path only — after the upstream answer, before the cache stores it — so
// followers and cache hits inherit the verdict for free and the blocked
// path never pays a nanosecond. Chain fetches (DNSKEY/DS) come back
// through forwardDedup with validation off: the validator verifies them
// cryptographically itself, and validating them again would recurse.

import (
	"context"
	"log/slog"

	"github.com/miekg/dns"

	"minos/internal/dnssec"
	"minos/internal/filter"
)

// dns.dnssec modes. Off must stay the zero value: an unwired Server (New
// before ApplyConfig) behaves exactly like today.
const (
	dnssecOff int32 = iota
	dnssecPermissive
	dnssecEnforce
)

func parseDNSSECMode(s string) int32 {
	switch s {
	case "permissive":
		return dnssecPermissive
	case "enforce":
		return dnssecEnforce
	default:
		return dnssecOff
	}
}

func dnssecModeName(m int32) string {
	switch m {
	case dnssecPermissive:
		return "permissive"
	case dnssecEnforce:
		return "enforce"
	default:
		return "off"
	}
}

// bogusAnswerError carries an enforce-mode DNSSEC failure to handle(),
// which answers SERVFAIL and attributes the refusal in the docket.
type bogusAnswerError struct{ reason string }

func (e *bogusAnswerError) Error() string { return "dnssec: bogus answer: " + e.reason }

// SetTrustAnchors overrides the built-in root trust anchors (tests and
// lab roots). Call before Start.
func (s *Server) SetTrustAnchors(ds []*dns.DS) {
	s.dnssecAnchors = ds
	if s.validator.Load() != nil {
		s.validator.Store(dnssec.New(s.resolveChain, dnssec.WithAnchors(ds)))
	}
}

// applyDNSSEC swaps the validation mode from a config change. The
// validator — and its validated-key cache — is built once and survives
// swaps: zone keys stay verified across settings edits.
func (s *Server) applyDNSSEC(mode string) {
	m := parseDNSSECMode(mode)
	if m != dnssecOff && s.validator.Load() == nil {
		var opts []dnssec.Option
		if s.dnssecAnchors != nil {
			opts = append(opts, dnssec.WithAnchors(s.dnssecAnchors))
		}
		s.validator.Store(dnssec.New(s.resolveChain, opts...))
	}
	s.dnssecMode.Store(m)
}

// resolveChain is the validator's fetch path for DNSKEY/DS records: DO
// set, deduped and cached like any forwarded query, never re-validated.
func (s *Server) resolveChain(ctx context.Context, qname string, qtype uint16) (*dns.Msg, error) {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(qname), qtype)
	req.SetEdns0(1232, true)
	norm := filter.NormalizeDomain(qname)
	resp, _, _, err := s.forwardDedup(ctx, req, norm, cacheKey(norm, qtype, req), s.cache.Load(), false)
	return resp, err
}

// ensureDO returns req, or a copy with the DO bit set so the upstream
// includes the DNSSEC records validation needs.
func ensureDO(req *dns.Msg) *dns.Msg {
	if opt := req.IsEdns0(); opt != nil && opt.Do() {
		return req
	}
	c := req.Copy()
	if opt := c.IsEdns0(); opt != nil {
		opt.SetDo()
	} else {
		c.SetEdns0(1232, true)
	}
	return c
}

// judgeDNSSEC validates one forwarded response. It returns an error only
// for an enforce-mode bogus answer; every other outcome stamps the AD bit
// and a counter. The response (and therefore the cache) carries the
// verdict, so cache hits never revalidate.
func (s *Server) judgeDNSSEC(ctx context.Context, resp *dns.Msg) error {
	mode := s.dnssecMode.Load()
	if mode == dnssecOff {
		return nil
	}
	v := s.validator.Load()
	if v == nil {
		return nil
	}
	res := v.Validate(ctx, resp)
	resp.AuthenticatedData = res.Status == dnssec.Secure
	switch res.Status {
	case dnssec.Secure:
		s.dnssecSecure.Add(1)
	case dnssec.Insecure:
		s.dnssecInsecure.Add(1)
	case dnssec.Indeterminate:
		// Deliberately passes even in enforce mode: indeterminate means
		// the chain could not be judged (upstream without DNSSEC support,
		// transient fetch failure) — refusing would turn a monitoring gap
		// into an outage. The counter is the signal to watch.
		s.dnssecIndeterminate.Add(1)
	case dnssec.Bogus:
		s.dnssecBogus.Add(1)
		if mode == dnssecEnforce {
			return &bogusAnswerError{reason: res.Reason}
		}
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("dnssec: bogus answer passed in permissive mode",
				"qname", resp.Question[0].Name, "reason", res.Reason)
		}
	}
	return nil
}

// finishDNSSEC adapts a served answer to what the client asked for: DO
// was added upstream by us, so clients that didn't request DNSSEC records
// don't receive them (unless they asked for that very type), and the AD
// bit reaches only clients that requested it via DO or AD (RFC 6840
// §5.7). Runs on both the cache-hit and forwarded paths.
func (s *Server) finishDNSSEC(req, resp *dns.Msg, qtype uint16) {
	if s.dnssecMode.Load() == dnssecOff {
		return
	}
	opt := req.IsEdns0()
	doBit := opt != nil && opt.Do()
	if !doBit {
		stripDNSSECRecords(resp, qtype)
		if !req.AuthenticatedData {
			resp.AuthenticatedData = false
		}
	}
}

// stripDNSSECRecords removes RRSIG/NSEC/NSEC3 from every section, keeping
// records of the type the client explicitly asked about.
func stripDNSSECRecords(m *dns.Msg, qtype uint16) {
	for _, sec := range []*[]dns.RR{&m.Answer, &m.Ns, &m.Extra} {
		kept := (*sec)[:0]
		for _, rr := range *sec {
			switch t := rr.Header().Rrtype; t {
			case dns.TypeRRSIG, dns.TypeNSEC, dns.TypeNSEC3:
				if t == qtype {
					kept = append(kept, rr)
				}
			default:
				kept = append(kept, rr)
			}
		}
		*sec = kept
	}
}

// DNSSECStat reports validation counters for the status and metrics APIs.
type DNSSECStat struct {
	Mode          string
	Secure        uint64
	Insecure      uint64
	Bogus         uint64
	Indeterminate uint64
}

func (s *Server) DNSSECStats() DNSSECStat {
	return DNSSECStat{
		Mode:          dnssecModeName(s.dnssecMode.Load()),
		Secure:        s.dnssecSecure.Load(),
		Insecure:      s.dnssecInsecure.Load(),
		Bogus:         s.dnssecBogus.Load(),
		Indeterminate: s.dnssecIndeterminate.Load(),
	}
}
