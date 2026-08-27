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
	"minos/internal/querylog"
)

// ListDNSSEC is the pseudo-list name validation attributes itself under in
// the query log: on `list` for an enforced refusal, on `audit_list` for a
// permissive would-block. Exported so the API queries the same name the
// proxy writes rather than repeating a literal.
const ListDNSSEC = "dnssec"

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
	resp, _, _, _, err := s.forwardDedup(ctx, req, norm, cacheKey(norm, qtype, req), s.cache.Load(), false)
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
//
// mark carries what the docket records: the outcome for every judged
// answer, plus the failure reason for a bogus one that permissive mode let
// through — the "enforce would have refused this" evidence. An enforce-mode
// refusal is a block instead, and err carries it.
func (s *Server) judgeDNSSEC(ctx context.Context, resp *dns.Msg) (mark dnssecMark, err error) {
	mode := s.dnssecMode.Load()
	if mode == dnssecOff {
		return dnssecMark{}, nil
	}
	v := s.validator.Load()
	if v == nil {
		return dnssecMark{}, nil
	}
	// Chain-record queries are the validator's own food: judging a
	// client's DNSKEY/DS answer would fetch that very record through the
	// same dedup key and wait on itself. Validating clients asking for
	// these types are running their own validation anyway.
	if len(resp.Question) == 1 {
		switch resp.Question[0].Qtype {
		case dns.TypeDNSKEY, dns.TypeDS, dns.TypeRRSIG:
			return dnssecMark{}, nil
		}
	}
	res := v.Validate(ctx, resp)
	resp.AuthenticatedData = res.Status == dnssec.Secure
	mark.status = statusName(res.Status)
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
			return mark, &bogusAnswerError{reason: res.Reason}
		}
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("dnssec: bogus answer passed in permissive mode",
				"qname", resp.Question[0].Name, "reason", res.Reason)
		}
		// Permissive: the answer is served, but the docket records what
		// enforce mode would have refused. A counter alone can't name the
		// domain, which is the one thing a permissive-mode operator needs
		// before flipping to enforce.
		//
		// The caller treats "" as "no mark", and every Bogus result
		// currently carries a reason — so fall back rather than let a
		// future empty reason silently erase the evidence.
		if res.Reason == "" {
			mark.audit = "bogus"
		} else {
			mark.audit = res.Reason
		}
		return mark, nil
	}
	return mark, nil
}

// dnssecMark is what validation concluded about one answer, on its way to
// the query log. Kept as a struct rather than more return values: the
// outcome and the bogus reason always travel together, and forwardDedup
// already carries enough.
type dnssecMark struct {
	// status is a querylog.DNSSEC* value, or "" when nothing was judged.
	status string
	// audit is the permissive-mode bogus reason; empty otherwise.
	audit string
}

// statusName maps a validator state to the querylog's stored text. The two
// vocabularies are kept apart deliberately: querylog must not import the
// validator to read its own column.
func statusName(st dnssec.Status) string {
	switch st {
	case dnssec.Secure:
		return querylog.DNSSECSecure
	case dnssec.Insecure:
		return querylog.DNSSECInsecure
	case dnssec.Bogus:
		return querylog.DNSSECBogus
	case dnssec.Indeterminate:
		return querylog.DNSSECIndeterminate
	}
	return ""
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
