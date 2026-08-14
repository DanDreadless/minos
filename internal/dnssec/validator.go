// Package dnssec validates forwarded DNS answers against the DNSSEC chain
// of trust (RFC 4033–4035). The validator is transport-agnostic: chain
// records (DNSKEY, DS) are fetched through a caller-supplied Resolver, so
// in production they ride the forwarder's dedup table and response cache.
// The Resolver must return DNSSEC records (query upstream with DO=1).
//
// Scope (first cut): positive answers only. Denial-of-existence
// (NSEC/NSEC3) and wildcard proofs are not implemented yet — negative and
// wildcard-expanded answers return Indeterminate, and an unsigned answer
// is taken at face value as Insecure rather than proven. NOTHING may wire
// this package into the query path until those proofs land: an enforcing
// validator without them is unsound.
package dnssec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// Status is the RFC 4033 validation state of one response. Order matters:
// a higher value is a worse outcome, and a response takes the worst state
// of its RRsets.
type Status int

const (
	// Secure: every answer RRset verified up to a trust anchor.
	Secure Status = iota
	// Insecure: no chain of trust covers the answer (unsigned zone).
	Insecure
	// Indeterminate: validation could not complete — chain fetch failed,
	// budget exhausted, or the needed proof type is not implemented yet.
	// Kept distinct from Bogus so enforcement policy can treat
	// infrastructure trouble differently from a failed proof.
	Indeterminate
	// Bogus: a chain of trust exists but verification failed. The answer
	// must not be trusted.
	Bogus
)

func (s Status) String() string {
	switch s {
	case Secure:
		return "secure"
	case Insecure:
		return "insecure"
	case Indeterminate:
		return "indeterminate"
	default:
		return "bogus"
	}
}

// Result is the validation outcome for one response. Reason is plain
// language for logs and docket attribution; empty when Secure.
type Result struct {
	Status Status
	Reason string
}

// Resolver fetches one chain record set (DNSKEY or DS) on behalf of the
// validator. Implementations must request DNSSEC records from upstream
// (DO bit set) or every chain fetch will come back unsigned.
type Resolver func(ctx context.Context, qname string, qtype uint16) (*dns.Msg, error)

// Anti-DoS bounds. KeyTrap (CVE-2023-50387) showed that colliding key tags
// and oversized key/signature sets turn a validator into a CPU amplifier;
// every loop below is capped and every signature attempt spends budget.
const (
	defaultFetchBudget  = 24 // chain fetches per Validate call
	defaultVerifyBudget = 32 // signature verifications per Validate call
	maxSigsPerRRset     = 8
	maxKeysPerZone      = 32
	keyCacheMax         = 2048
)

// supportedAlgorithm reports whether we can verify signatures made with
// alg. Unsupported algorithms make a zone Insecure, never Bogus (RFC 4035
// §5.2): refusing to resolve a zone because it uses newer crypto than we
// know would be self-inflicted breakage.
func supportedAlgorithm(alg uint8) bool {
	switch alg {
	case dns.RSASHA1, dns.RSASHA1NSEC3SHA1, dns.RSASHA256, dns.RSASHA512,
		dns.ECDSAP256SHA256, dns.ECDSAP384SHA384, dns.ED25519:
		return true
	}
	return false
}

func supportedDigest(dt uint8) bool {
	switch dt {
	case dns.SHA1, dns.SHA256, dns.SHA384:
		return true
	}
	return false
}

// Validator checks answers against the chain of trust. Safe for concurrent
// use; all mutable state lives in the lock-free key cache.
type Validator struct {
	resolve      Resolver
	anchors      []*dns.DS
	keys         *keyCache
	now          func() time.Time
	fetchBudget  int
	verifyBudget int
}

// Option customizes a Validator (tests and lab setups).
type Option func(*Validator)

// WithAnchors replaces the built-in root trust anchors.
func WithAnchors(ds []*dns.DS) Option { return func(v *Validator) { v.anchors = ds } }

// WithClock stubs the validation clock.
func WithClock(now func() time.Time) Option { return func(v *Validator) { v.now = now } }

// WithBudgets overrides the per-call fetch and verification budgets.
func WithBudgets(fetches, verifies int) Option {
	return func(v *Validator) { v.fetchBudget, v.verifyBudget = fetches, verifies }
}

func New(resolve Resolver, opts ...Option) *Validator {
	v := &Validator{
		resolve:      resolve,
		anchors:      RootAnchors(),
		keys:         newKeyCache(keyCacheMax),
		now:          time.Now,
		fetchBudget:  defaultFetchBudget,
		verifyBudget: defaultVerifyBudget,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// budget is the per-Validate spend tracker shared down the chain walk.
type budget struct {
	fetches  int
	verifies int
}

// Validate judges one upstream response. It never mutates resp.
func (v *Validator) Validate(ctx context.Context, resp *dns.Msg) Result {
	if resp == nil || len(resp.Question) == 0 {
		return Result{Indeterminate, "malformed response"}
	}
	switch resp.Rcode {
	case dns.RcodeSuccess:
	case dns.RcodeNameError:
		return Result{Indeterminate, "negative answer (denial-of-existence proofs not implemented)"}
	default:
		return Result{Indeterminate, fmt.Sprintf("rcode %s is not validatable", dns.RcodeToString[resp.Rcode])}
	}
	sets := groupRRsets(resp.Answer)
	if len(sets) == 0 {
		return Result{Indeterminate, "empty answer (denial-of-existence proofs not implemented)"}
	}
	b := &budget{fetches: v.fetchBudget, verifies: v.verifyBudget}
	worst := Result{Status: Secure}
	for _, set := range sets {
		res := v.validateRRset(ctx, set, resp.Answer, b)
		if res.Status > worst.Status {
			worst = res
		}
		if worst.Status == Bogus {
			return worst
		}
	}
	return worst
}

// validateRRset verifies one answer RRset against the chain of trust.
func (v *Validator) validateRRset(ctx context.Context, rrset, section []dns.RR, b *budget) Result {
	owner := dns.CanonicalName(rrset[0].Header().Name)
	sigs := coveringSigs(section, owner, rrset[0].Header().Rrtype)
	if len(sigs) == 0 {
		// Face value: a stripped-signature attack lands here too. Closing
		// that hole needs the DS-walk proof of unsignedness (next stage).
		return Result{Insecure, "no covering signature for " + owner}
	}
	// All signatures over one RRset come from the zone containing it, so a
	// single chain walk serves every candidate signature.
	signer := dns.CanonicalName(sigs[0].SignerName)
	keys, chain := v.trustedKeys(ctx, signer, b)
	if chain.Status != Secure {
		return chain
	}
	verified, res := v.verifyWithKeys(sigs, rrset, keys, b)
	if res.Status != Secure {
		return res
	}
	if int(verified.Labels) < dns.CountLabel(owner) {
		// Wildcard expansion verifies, but soundness needs the NSEC proof
		// that no closer name exists — not implemented yet.
		return Result{Indeterminate, "wildcard-expanded answer (proof not implemented)"}
	}
	return Result{Status: Secure}
}

// trustedKeys returns the validated DNSKEY set for zone, walking DS/DNSKEY
// records up to a trust anchor and caching the result. The returned Result
// is Secure exactly when keys is usable.
func (v *Validator) trustedKeys(ctx context.Context, zone string, b *budget) ([]*dns.DNSKEY, Result) {
	now := v.now()
	if keys := v.keys.get(zone, now); keys != nil {
		return keys, Result{Status: Secure}
	}
	ds := v.anchors
	if zone != "." {
		set, res := v.fetchDS(ctx, zone, b)
		if res.Status != Secure {
			return nil, res
		}
		ds = set
	}
	if len(ds) == 0 {
		return nil, Result{Insecure, "no DS record for " + zone + " (unsigned delegation, unproven)"}
	}
	usable := false
	for _, d := range ds {
		if supportedAlgorithm(d.Algorithm) && supportedDigest(d.DigestType) {
			usable = true
			break
		}
	}
	if !usable {
		return nil, Result{Insecure, "no supported algorithm in DS set for " + zone}
	}
	keys, ttl, res := v.fetchDNSKEY(ctx, zone, ds, b)
	if res.Status != Secure {
		return nil, res
	}
	v.keys.put(zone, keys, ttl, now)
	return keys, Result{Status: Secure}
}

// fetchDS retrieves and validates the DS RRset delegating zone, recursing
// into the parent for its keys. An empty (Secure, nil) return means the
// parent answered NOERROR with no DS records.
func (v *Validator) fetchDS(ctx context.Context, zone string, b *budget) ([]*dns.DS, Result) {
	resp, res := v.query(ctx, zone, dns.TypeDS, b)
	if res.Status != Secure {
		return nil, res
	}
	var ds []*dns.DS
	var asRRs []dns.RR
	for _, rr := range resp.Answer {
		if d, ok := rr.(*dns.DS); ok && dns.CanonicalName(d.Hdr.Name) == zone {
			ds = append(ds, d)
			asRRs = append(asRRs, rr)
		}
	}
	if len(ds) == 0 {
		return nil, Result{Status: Secure}
	}
	sigs := coveringSigs(resp.Answer, zone, dns.TypeDS)
	if len(sigs) == 0 {
		return nil, Result{Bogus, "DS rrset for " + zone + " is unsigned"}
	}
	parent := dns.CanonicalName(sigs[0].SignerName)
	// The signer must be a proper ancestor: strictly fewer labels
	// guarantees the recursion terminates at the root.
	if parent == zone || !dns.IsSubDomain(parent, zone) {
		return nil, Result{Bogus, "DS rrset for " + zone + " signed by non-parent " + parent}
	}
	parentKeys, chain := v.trustedKeys(ctx, parent, b)
	if chain.Status != Secure {
		return nil, chain
	}
	if _, res := v.verifyWithKeys(sigs, asRRs, parentKeys, b); res.Status != Secure {
		res.Reason = "DS rrset for " + zone + ": " + res.Reason
		return nil, res
	}
	return ds, Result{Status: Secure}
}

// fetchDNSKEY retrieves zone's DNSKEY RRset and verifies it is self-signed
// by a key matching one of the DS records (or trust anchors) for the zone.
// A verified set is trusted in full: ZSKs inherit trust from the KSK's
// signature over the whole RRset.
func (v *Validator) fetchDNSKEY(ctx context.Context, zone string, ds []*dns.DS, b *budget) ([]*dns.DNSKEY, uint32, Result) {
	resp, res := v.query(ctx, zone, dns.TypeDNSKEY, b)
	if res.Status != Secure {
		return nil, 0, res
	}
	var keys []*dns.DNSKEY
	var asRRs []dns.RR
	for _, rr := range resp.Answer {
		k, ok := rr.(*dns.DNSKEY)
		if !ok || dns.CanonicalName(k.Hdr.Name) != zone {
			continue
		}
		// RFC 4034 §2.1: only zone keys with protocol 3 sign DNS data;
		// revoked keys (RFC 5011) must not be trusted.
		if k.Flags&dns.ZONE == 0 || k.Flags&dns.REVOKE != 0 || k.Protocol != 3 {
			continue
		}
		keys = append(keys, k)
		asRRs = append(asRRs, rr)
		if len(keys) > maxKeysPerZone {
			return nil, 0, Result{Bogus, "DNSKEY set for " + zone + " exceeds size limit"}
		}
	}
	if len(keys) == 0 {
		return nil, 0, Result{Bogus, "no DNSKEY rrset for signed zone " + zone}
	}
	// Entry points: keys whose digest matches a DS record. Only their
	// signatures can bless the set.
	var entryPoints []*dns.DNSKEY
	for _, k := range keys {
		for _, d := range ds {
			if k.KeyTag() != d.KeyTag || k.Algorithm != d.Algorithm {
				continue
			}
			computed := k.ToDS(d.DigestType)
			if computed != nil && strings.EqualFold(computed.Digest, d.Digest) {
				entryPoints = append(entryPoints, k)
				break
			}
		}
	}
	if len(entryPoints) == 0 {
		return nil, 0, Result{Bogus, "no DNSKEY for " + zone + " matches its DS records"}
	}
	sigs := coveringSigs(resp.Answer, zone, dns.TypeDNSKEY)
	if len(sigs) == 0 {
		return nil, 0, Result{Bogus, "DNSKEY rrset for " + zone + " is unsigned"}
	}
	if _, res := v.verifyWithKeys(sigs, asRRs, entryPoints, b); res.Status != Secure {
		res.Reason = "DNSKEY rrset for " + zone + ": " + res.Reason
		return nil, 0, res
	}
	return keys, asRRs[0].Header().Ttl, Result{Status: Secure}
}

// verifyWithKeys tries each signature against each tag-matching key until
// one verifies, spending verification budget per crypto attempt. Reaching
// here means a chain of trust exists, so total failure is Bogus.
func (v *Validator) verifyWithKeys(sigs []*dns.RRSIG, rrset []dns.RR, keys []*dns.DNSKEY, b *budget) (*dns.RRSIG, Result) {
	now := v.now()
	reason := "no signature matches a trusted key"
	for _, sig := range sigs {
		if !sig.ValidityPeriod(now) {
			reason = "signature outside its validity period"
			continue
		}
		for _, key := range keys {
			if key.KeyTag() != sig.KeyTag || key.Algorithm != sig.Algorithm {
				continue
			}
			if b.verifies <= 0 {
				return nil, Result{Bogus, "signature verification budget exhausted"}
			}
			b.verifies--
			if err := sig.Verify(key, rrset); err == nil {
				return sig, Result{Status: Secure}
			}
			reason = "signature verification failed"
		}
	}
	return nil, Result{Bogus, reason}
}

// query performs one budgeted chain fetch.
func (v *Validator) query(ctx context.Context, qname string, qtype uint16, b *budget) (*dns.Msg, Result) {
	if b.fetches <= 0 {
		return nil, Result{Indeterminate, "chain fetch budget exhausted"}
	}
	b.fetches--
	resp, err := v.resolve(ctx, qname, qtype)
	if err != nil {
		return nil, Result{Indeterminate, "chain fetch " + qname + "/" + dns.TypeToString[qtype] + " failed: " + err.Error()}
	}
	if resp == nil || (resp.Rcode != dns.RcodeSuccess && resp.Rcode != dns.RcodeNameError) {
		return nil, Result{Indeterminate, "chain fetch " + qname + "/" + dns.TypeToString[qtype] + " unanswerable"}
	}
	return resp, Result{Status: Secure}
}

// groupRRsets splits a message section into RRsets (same owner, type,
// class), preserving section order. RRSIG and OPT records are metadata,
// not data, and non-INET classes are not validatable.
func groupRRsets(section []dns.RR) [][]dns.RR {
	var order []string
	byKey := make(map[string][]dns.RR)
	for _, rr := range section {
		h := rr.Header()
		if h.Rrtype == dns.TypeRRSIG || h.Rrtype == dns.TypeOPT || h.Class != dns.ClassINET {
			continue
		}
		key := dns.CanonicalName(h.Name) + "|" + dns.TypeToString[h.Rrtype]
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], rr)
	}
	sets := make([][]dns.RR, 0, len(order))
	for _, key := range order {
		sets = append(sets, byKey[key])
	}
	return sets
}

// coveringSigs collects the RRSIGs in section covering (owner, rrtype)
// whose signer could plausibly own the name — a signature from a foreign
// zone can never chain and is ignored outright.
func coveringSigs(section []dns.RR, owner string, rrtype uint16) []*dns.RRSIG {
	var sigs []*dns.RRSIG
	for _, rr := range section {
		sig, ok := rr.(*dns.RRSIG)
		if !ok || sig.TypeCovered != rrtype || sig.Hdr.Class != dns.ClassINET {
			continue
		}
		if dns.CanonicalName(sig.Hdr.Name) != owner {
			continue
		}
		if !dns.IsSubDomain(dns.CanonicalName(sig.SignerName), owner) {
			continue
		}
		sigs = append(sigs, sig)
		if len(sigs) == maxSigsPerRRset {
			break
		}
	}
	return sigs
}
