package dnssec

// Denial of existence (RFC 4035 for NSEC, RFC 5155 for NSEC3, with the
// RFC 6840 clarifications): the proofs that make negative answers,
// wildcard expansions, and unsigned delegations trustworthy. Everything
// here consumes proof RRsets that have already been signature-validated
// by the caller — these functions reason about ranges and bitmaps only.

import (
	"context"
	"slices"
	"strings"

	"github.com/miekg/dns"
)

// maxNSEC3Iterations follows RFC 9276: zones should use 0 extra
// iterations, and validators treat zones above a small cap as insecure
// rather than burning CPU (each iteration is an extra SHA-1 per hashed
// name — an amplification lever, cousin to KeyTrap).
const maxNSEC3Iterations = 100

// maxWalkLabels bounds every suffix walk over a query name.
const maxWalkLabels = 16

// canonicalCmp orders names per RFC 4034 §6.1: labels compared right to
// left, each as a lowercase byte string; the name that runs out of labels
// first sorts first.
func canonicalCmp(a, b string) int {
	la := dns.SplitDomainName(dns.CanonicalName(a))
	lb := dns.SplitDomainName(dns.CanonicalName(b))
	for i := 1; ; i++ {
		switch {
		case i > len(la) && i > len(lb):
			return 0
		case i > len(la):
			return -1
		case i > len(lb):
			return 1
		}
		if c := strings.Compare(la[len(la)-i], lb[len(lb)-i]); c != 0 {
			return c
		}
	}
}

// nsecCovers reports whether name falls strictly between owner and next,
// honoring the last-NSEC wrap (next ≤ owner means the range runs off the
// end of the zone and back to the apex).
func nsecCovers(owner, next, name string) bool {
	if canonicalCmp(owner, next) < 0 {
		return canonicalCmp(owner, name) < 0 && canonicalCmp(name, next) < 0
	}
	return canonicalCmp(owner, name) < 0 || canonicalCmp(name, next) < 0
}

func hasType(bitmap []uint16, t uint16) bool {
	return slices.Contains(bitmap, t)
}

// ancestorDelegationNSEC reports the parent side of a zone cut (NS set,
// SOA clear). Such an NSEC proves nothing about names below the cut and
// must not satisfy a NODATA proof for anything but DS (RFC 6840 §4.1).
func ancestorDelegationNSEC(n *dns.NSEC) bool {
	return hasType(n.TypeBitMap, dns.TypeNS) && !hasType(n.TypeBitMap, dns.TypeSOA)
}

// suffixChain lists name and its ancestors, longest first, ending at the
// root: [www.example.com. example.com. com. .], capped at maxWalkLabels.
func suffixChain(name string) []string {
	labels := dns.SplitDomainName(dns.CanonicalName(name))
	if len(labels) > maxWalkLabels {
		labels = labels[len(labels)-maxWalkLabels:]
	}
	out := make([]string, 0, len(labels)+1)
	for i := range labels {
		out = append(out, strings.Join(labels[i:], ".")+".")
	}
	return append(out, ".")
}

// lastNLabels returns the suffix of name keeping n labels ("." for 0).
func lastNLabels(name string, n int) string {
	labels := dns.SplitDomainName(dns.CanonicalName(name))
	if n <= 0 || len(labels) < n {
		if n > len(labels) {
			return dns.CanonicalName(name)
		}
		return "."
	}
	return strings.Join(labels[len(labels)-n:], ".") + "."
}

// wildcardOf returns the source-of-synthesis wildcard for a closest
// encloser.
func wildcardOf(ce string) string {
	if ce == "." {
		return "*."
	}
	return "*." + ce
}

// proofs holds the signature-validated NSEC/NSEC3 records extracted from
// one response's authority section.
type proofs struct {
	nsec  []*dns.NSEC
	nsec3 []*dns.NSEC3
	// overIter notes an NSEC3 discarded for exceeding the iteration cap:
	// the zone is treated as insecure when no other proof lands (RFC 9276).
	overIter bool
	// bogus carries the reason of a proof RRset whose signature failed —
	// reported when the remaining proofs cannot carry the response.
	bogus string
}

func (p *proofs) empty() bool { return len(p.nsec) == 0 && len(p.nsec3) == 0 }

// collectProofs signature-validates the NSEC/NSEC3 RRsets in the authority
// section. Unsigned or unverifiable proof records are dropped (they prove
// nothing); a Bogus one is remembered for the final verdict.
func (v *Validator) collectProofs(ctx context.Context, resp *dns.Msg, b *budget) *proofs {
	p := &proofs{}
	for _, set := range groupRRsets(resp.Ns) {
		t := set[0].Header().Rrtype
		if t != dns.TypeNSEC && t != dns.TypeNSEC3 {
			continue
		}
		res := v.validateRRset(ctx, set, resp.Ns, resp, true, b)
		if res.Status != Secure {
			if res.Status == Bogus && p.bogus == "" {
				p.bogus = res.Reason
			}
			continue
		}
		for _, rr := range set {
			switch n := rr.(type) {
			case *dns.NSEC:
				p.nsec = append(p.nsec, n)
			case *dns.NSEC3:
				if n.Hash != dns.SHA1 {
					continue // only SHA-1 is defined for NSEC3
				}
				if n.Iterations > maxNSEC3Iterations {
					p.overIter = true
					continue
				}
				p.nsec3 = append(p.nsec3, n)
			}
		}
	}
	return p
}

func (p *proofs) nsecMatching(name string) []*dns.NSEC {
	var out []*dns.NSEC
	for _, n := range p.nsec {
		if canonicalCmp(n.Hdr.Name, name) == 0 {
			out = append(out, n)
		}
	}
	return out
}

func (p *proofs) nsecCovering(name string) *dns.NSEC {
	for _, n := range p.nsec {
		if nsecCovers(n.Hdr.Name, n.NextDomain, name) {
			return n
		}
	}
	return nil
}

// nsec3Matching / nsec3Covering ride miekg's NSEC3 Match/Cover (hashing
// with the record's own parameters). Each call spends hash budget: NSEC3
// hashing is attacker-priced CPU.
func (p *proofs) nsec3Matching(name string, b *budget) *dns.NSEC3 {
	for _, n := range p.nsec3 {
		if b.hashes <= 0 {
			return nil
		}
		b.hashes--
		if n.Match(name) {
			return n
		}
	}
	return nil
}

func (p *proofs) nsec3Covering(name string, b *budget) *dns.NSEC3 {
	for _, n := range p.nsec3 {
		if b.hashes <= 0 {
			return nil
		}
		b.hashes--
		if n.Cover(name) {
			return n
		}
	}
	return nil
}

func optOut(n *dns.NSEC3) bool { return n != nil && n.Flags&1 != 0 }

// closestEncloserFromNSEC derives the closest encloser of qname implied by
// a covering NSEC: the longest ancestor of qname shared with the NSEC's
// owner or next name (both provably exist).
func closestEncloserFromNSEC(qname string, n *dns.NSEC) string {
	best := commonAncestorLabels(qname, n.Hdr.Name)
	if c := commonAncestorLabels(qname, n.NextDomain); c > best {
		best = c
	}
	return lastNLabels(qname, best)
}

func commonAncestorLabels(a, b string) int {
	la := dns.SplitDomainName(dns.CanonicalName(a))
	lb := dns.SplitDomainName(dns.CanonicalName(b))
	n := 0
	for n < len(la) && n < len(lb) &&
		la[len(la)-1-n] == lb[len(lb)-1-n] {
		n++
	}
	return n
}

// nsec3CE runs the RFC 5155 §8.3 closest-encloser walk: the longest
// suffix of qname matched by an NSEC3 is the closest encloser, and the
// suffix one label longer is the next closer. exists reports qname itself
// matching (the name exists — a NODATA shape, fatal for NXDOMAIN).
func (p *proofs) nsec3CE(qname string, b *budget) (ce, nc string, found, exists bool) {
	chain := suffixChain(qname)
	for i, cand := range chain {
		if p.nsec3Matching(cand, b) == nil {
			continue
		}
		if i == 0 {
			return cand, "", false, true
		}
		return cand, chain[i-1], true, false
	}
	return "", "", false, false
}

// proveNameError checks an NXDOMAIN: qname must be proven absent AND no
// wildcard may cover it (RFC 4035 §5.4, RFC 5155 §8.4).
func (p *proofs) proveNameError(qname string, b *budget) Result {
	if cov := p.nsecCovering(qname); cov != nil {
		wc := wildcardOf(closestEncloserFromNSEC(qname, cov))
		if p.nsecCovering(wc) != nil {
			return Result{Status: Secure}
		}
		return p.fail("NXDOMAIN proof does not disprove wildcard " + wc)
	}
	if len(p.nsecMatching(qname)) > 0 {
		return Result{Bogus, "NXDOMAIN for a name an NSEC proves to exist"}
	}
	if ce, nc, found, exists := p.nsec3CE(qname, b); exists {
		return Result{Bogus, "NXDOMAIN for a name an NSEC3 proves to exist"}
	} else if found {
		ncCov := p.nsec3Covering(nc, b)
		if ncCov == nil {
			return p.fail("NSEC3 proof does not cover the next closer " + nc)
		}
		if optOut(ncCov) {
			// An opt-out span cannot disprove an insecure delegation:
			// the strongest supportable state is insecure (RFC 5155 §9.2).
			return Result{Insecure, "NXDOMAIN inside an NSEC3 opt-out span"}
		}
		wc := wildcardOf(ce)
		if p.nsec3Matching(wc, b) != nil {
			return Result{Bogus, "NXDOMAIN while wildcard " + wc + " exists"}
		}
		if p.nsec3Covering(wc, b) != nil {
			return Result{Status: Secure}
		}
		return p.fail("NSEC3 proof does not disprove wildcard " + wc)
	}
	return p.fail("no denial proof covers " + qname)
}

// proveNoData checks a NOERROR/empty answer: qtype (and CNAME) must be
// proven absent at qname (RFC 4035 §5.4, RFC 5155 §8.5–8.7).
func (p *proofs) proveNoData(qname string, qtype uint16, b *budget) Result {
	for _, n := range p.nsecMatching(qname) {
		if qtype != dns.TypeDS && ancestorDelegationNSEC(n) {
			continue // parent-side NSEC cannot speak for the child zone
		}
		if qtype == dns.TypeDS && hasType(n.TypeBitMap, dns.TypeSOA) {
			continue // child apex NSEC cannot prove DS absence (parent holds DS)
		}
		if bitmapDenies(n.TypeBitMap, qtype) {
			return Result{Status: Secure}
		}
		return Result{Bogus, "NODATA for a type the NSEC bitmap proves present"}
	}
	// Wildcard NODATA over NSEC: qname is absent but a wildcard exists
	// without the type.
	if cov := p.nsecCovering(qname); cov != nil {
		wc := wildcardOf(closestEncloserFromNSEC(qname, cov))
		for _, n := range p.nsecMatching(wc) {
			if bitmapDenies(n.TypeBitMap, qtype) {
				return Result{Status: Secure}
			}
		}
	}
	if m := p.nsec3Matching(qname, b); m != nil {
		if qtype != dns.TypeDS && hasType(m.TypeBitMap, dns.TypeNS) && !hasType(m.TypeBitMap, dns.TypeSOA) {
			// parent-side NSEC3 of a delegation: unusable below the cut
		} else if bitmapDenies(m.TypeBitMap, qtype) {
			return Result{Status: Secure}
		} else {
			return Result{Bogus, "NODATA for a type the NSEC3 bitmap proves present"}
		}
	}
	if ce, nc, found, _ := p.nsec3CE(qname, b); found {
		if qtype == dns.TypeDS && optOut(p.nsec3Covering(nc, b)) {
			return Result{Insecure, "DS NODATA inside an NSEC3 opt-out span"}
		}
		// Wildcard NODATA over NSEC3.
		if m := p.nsec3Matching(wildcardOf(ce), b); m != nil &&
			bitmapDenies(m.TypeBitMap, qtype) && p.nsec3Covering(nc, b) != nil {
			return Result{Status: Secure}
		}
	}
	return p.fail("no denial proof for " + qname + "/" + dns.TypeToString[qtype])
}

// bitmapDenies reports the bitmap proving qtype absent — the type itself
// and, for non-CNAME queries, CNAME too (a present CNAME would have
// answered; RFC 6840 §4.3).
func bitmapDenies(bitmap []uint16, qtype uint16) bool {
	if hasType(bitmap, qtype) {
		return false
	}
	return qtype == dns.TypeCNAME || !hasType(bitmap, dns.TypeCNAME)
}

// fail converts "the proofs don't prove it" into the right terminal state:
// discarded over-limit NSEC3s make the zone insecure, a bogus proof RRset
// surfaces its own reason, anything else is a bogus response.
func (p *proofs) fail(reason string) Result {
	if p.overIter {
		return Result{Insecure, "NSEC3 iterations exceed the supported limit"}
	}
	if p.bogus != "" {
		return Result{Bogus, p.bogus}
	}
	return Result{Bogus, reason}
}

// dsAbsence classifies why a DS fetch came back empty.
type dsAbsence int

const (
	dsPresent dsAbsence = iota
	// dsInsecureDelegation: proven NS-without-DS (or an opt-out span) —
	// the child zone is legitimately unsigned.
	dsInsecureDelegation
	// dsNotDelegation: proven not a zone cut (or not to exist) — keep
	// descending, the covering zone continues.
	dsNotDelegation
	// dsUnproven: the response carried no usable proof either way.
	dsUnproven
)

// proveDSStatus interprets an empty DS response's proofs (RFC 5155 §8.6,
// RFC 6840 §4.4).
func (p *proofs) proveDSStatus(zone string, b *budget) dsAbsence {
	for _, n := range p.nsecMatching(zone) {
		bm := n.TypeBitMap
		if hasType(bm, dns.TypeSOA) || hasType(bm, dns.TypeDS) {
			continue // child-side NSEC, or a contradiction: unusable
		}
		if hasType(bm, dns.TypeNS) {
			return dsInsecureDelegation
		}
		return dsNotDelegation
	}
	if p.nsecCovering(zone) != nil {
		return dsNotDelegation // the name does not exist at the parent
	}
	if m := p.nsec3Matching(zone, b); m != nil {
		bm := m.TypeBitMap
		if !hasType(bm, dns.TypeSOA) && !hasType(bm, dns.TypeDS) {
			if hasType(bm, dns.TypeNS) {
				return dsInsecureDelegation
			}
			return dsNotDelegation
		}
	}
	if cov := p.nsec3Covering(zone, b); cov != nil {
		if optOut(cov) {
			return dsInsecureDelegation
		}
		return dsNotDelegation
	}
	if p.overIter {
		// Can't afford to check the zone's proofs: treat as insecure
		// rather than refuse the whole subtree (RFC 9276).
		return dsInsecureDelegation
	}
	return dsUnproven
}

// proveUnsigned decides what an unsigned answer for name means by walking
// the delegation chain from the root: a proven insecure delegation
// legitimizes it, a fully signed chain condemns it (stripped signatures),
// and missing proof stays honestly indeterminate.
func (v *Validator) proveUnsigned(ctx context.Context, name string, b *budget) Result {
	chain := suffixChain(name)
	// Walk top-down (skip the root itself — it is signed and anchored).
	for i := len(chain) - 2; i >= 0; i-- {
		cut := chain[i]
		ds, absence, res := v.fetchDS(ctx, cut, b)
		if res.Status != Secure {
			return res
		}
		if len(ds) > 0 {
			continue // signed at this cut, descend
		}
		switch absence {
		case dsInsecureDelegation:
			return Result{Insecure, "unsigned answer under provably insecure delegation " + cut}
		case dsNotDelegation:
			continue // no cut here, the covering zone continues
		default:
			return Result{Indeterminate, "cannot prove whether " + cut + " is signed"}
		}
	}
	return Result{Bogus, "unsigned answer from a provably signed zone"}
}

// validateNegative judges an NXDOMAIN or NODATA response: any answer
// RRsets (CNAME chains) validate positively, then the denial subject —
// the end of the CNAME chain — must be proven absent.
func (v *Validator) validateNegative(ctx context.Context, resp *dns.Msg, b *budget) Result {
	qname := dns.CanonicalName(resp.Question[0].Name)
	qtype := resp.Question[0].Qtype
	worst := Result{Status: Secure}
	for _, set := range groupRRsets(resp.Answer) {
		res := v.validateRRset(ctx, set, resp.Answer, resp, false, b)
		if res.Status == Bogus {
			return res
		}
		if res.Status > worst.Status {
			worst = res
		}
	}
	subject := qname
	if qtype != dns.TypeCNAME {
		subject = followCNAMEs(qname, resp.Answer)
	}
	p := v.collectProofs(ctx, resp, b)
	var res Result
	switch {
	case p.empty():
		// No usable proof. Discarded proofs (over-iteration NSEC3s, bogus
		// signatures) decide the verdict themselves; a truly proof-free
		// response gets the delegation walk to tell "unsigned zone" from
		// "signed zone withholding proofs".
		if p.overIter || p.bogus != "" {
			res = p.fail("no usable denial proof")
			break
		}
		res = v.proveUnsigned(ctx, subject, b)
		if res.Status == Bogus {
			res.Reason = "negative answer without denial proof from a signed zone"
		}
	case resp.Rcode == dns.RcodeNameError:
		res = p.proveNameError(subject, b)
	default:
		res = p.proveNoData(subject, qtype, b)
	}
	if res.Status > worst.Status {
		worst = res
	}
	return worst
}

// followCNAMEs chases the CNAME chain in an answer section from qname to
// its final target (bounded — hostile chains can loop).
func followCNAMEs(qname string, answer []dns.RR) string {
	current := qname
	for range 8 {
		advanced := false
		for _, rr := range answer {
			c, ok := rr.(*dns.CNAME)
			if ok && canonicalCmp(c.Hdr.Name, current) == 0 {
				current = dns.CanonicalName(c.Target)
				advanced = true
				break
			}
		}
		if !advanced {
			break
		}
	}
	return current
}
