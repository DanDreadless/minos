package dnssec

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// testZone is one signed zone in the synthetic hierarchy.
type testZone struct {
	name    string
	ksk     *dns.DNSKEY
	zsk     *dns.DNSKEY
	kskPriv crypto.Signer
	zskPriv crypto.Signer
}

func newTestZone(t testing.TB, name string) *testZone {
	t.Helper()
	z := &testZone{name: dns.CanonicalName(name)}
	z.ksk, z.kskPriv = genKey(t, z.name, 257)
	z.zsk, z.zskPriv = genKey(t, z.name, 256)
	return z
}

func genKey(t testing.TB, zone string, flags uint16) (*dns.DNSKEY, crypto.Signer) {
	t.Helper()
	key := &dns.DNSKEY{
		Hdr:       dns.RR_Header{Name: zone, Rrtype: dns.TypeDNSKEY, Class: dns.ClassINET, Ttl: 3600},
		Flags:     flags,
		Protocol:  3,
		Algorithm: dns.ECDSAP256SHA256,
	}
	priv, err := key.Generate(256)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key, priv.(crypto.Signer)
}

// sign produces an RRSIG over rrset. A zero expire means "a day from now".
// (RRSIG.Sign derives Labels from the owner itself, decrementing for a
// literal "*." prefix - wildcard tests sign the wildcard owner and rename.)
func sign(t testing.TB, key *dns.DNSKEY, priv crypto.Signer, rrset []dns.RR, expire time.Time) *dns.RRSIG {
	t.Helper()
	if expire.IsZero() {
		expire = time.Now().Add(24 * time.Hour)
	}
	sig := &dns.RRSIG{
		Hdr: dns.RR_Header{
			Name: rrset[0].Header().Name, Rrtype: dns.TypeRRSIG,
			Class: dns.ClassINET, Ttl: rrset[0].Header().Ttl,
		},
		TypeCovered: rrset[0].Header().Rrtype,
		Algorithm:   key.Algorithm,
		OrigTtl:     rrset[0].Header().Ttl,
		Expiration:  uint32(expire.Unix()),
		Inception:   uint32(expire.Add(-48 * time.Hour).Unix()),
		KeyTag:      key.KeyTag(),
		SignerName:  key.Hdr.Name,
	}
	if err := sig.Sign(priv, rrset); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

func aRecord(owner string, ip net.IP) *dns.A {
	return &dns.A{
		Hdr: dns.RR_Header{Name: owner, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   ip,
	}
}

// fakeResolver serves chain records from a map and counts fetches.
type fakeResolver struct {
	answers map[string]*dns.Msg
	errs    map[string]error
	calls   int
}

func (f *fakeResolver) resolve(_ context.Context, qname string, qtype uint16) (*dns.Msg, error) {
	f.calls++
	key := dns.CanonicalName(qname) + "|" + dns.TypeToString[qtype]
	if err, ok := f.errs[key]; ok {
		return nil, err
	}
	if msg, ok := f.answers[key]; ok {
		return msg, nil
	}
	// Anything not staged answers NOERROR/empty, like a real parent asked
	// for a DS that does not exist.
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), qtype)
	m.Response = true
	return m, nil
}

func (f *fakeResolver) stage(qname string, qtype uint16, answer ...dns.RR) {
	f.stageFull(qname, qtype, answer, nil)
}

// stageFull stages a response with both sections (empty answers with
// authority proofs model real DS/negative responses).
func (f *fakeResolver) stageFull(qname string, qtype uint16, answer, ns []dns.RR) {
	key := dns.CanonicalName(qname) + "|" + dns.TypeToString[qtype]
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), qtype)
	m.Response = true
	m.Answer = answer
	m.Ns = ns
	f.answers[key] = m
}

// hierarchy builds root -> com -> example.com with the chain records staged,
// returning the resolver, the zones, and the root anchor option.
func hierarchy(t testing.TB) (*fakeResolver, map[string]*testZone, Option) {
	t.Helper()
	root := newTestZone(t, ".")
	com := newTestZone(t, "com.")
	example := newTestZone(t, "example.com.")
	zones := map[string]*testZone{".": root, "com.": com, "example.com.": example}

	f := &fakeResolver{answers: map[string]*dns.Msg{}, errs: map[string]error{}}
	stageZone := func(z *testZone, parent *testZone) {
		keyset := []dns.RR{z.ksk, z.zsk}
		f.stage(z.name, dns.TypeDNSKEY, append(keyset, sign(t, z.ksk, z.kskPriv, keyset, time.Time{}))...)
		if parent != nil {
			ds := z.ksk.ToDS(dns.SHA256)
			ds.Hdr.Ttl = 3600
			dsSet := []dns.RR{ds}
			f.stage(z.name, dns.TypeDS, append(dsSet, sign(t, parent.zsk, parent.zskPriv, dsSet, time.Time{}))...)
		}
	}
	stageZone(root, nil)
	stageZone(com, root)
	stageZone(example, com)

	anchor := root.ksk.ToDS(dns.SHA256)
	return f, zones, WithAnchors([]*dns.DS{anchor})
}

// signedAnswer builds a response for owner/A signed by example.com's ZSK.
func signedAnswer(t testing.TB, z *testZone, owner string) *dns.Msg {
	t.Helper()
	a := aRecord(owner, net.IPv4(203, 0, 113, 7))
	m := new(dns.Msg)
	m.SetQuestion(owner, dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a, sign(t, z.zsk, z.zskPriv, []dns.RR{a}, time.Time{})}
	return m
}

func TestSecureChain(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	v := New(f.resolve, anchors)
	msg := signedAnswer(t, zones["example.com."], "www.example.com.")

	res := v.Validate(context.Background(), msg)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
	// Full cold chain: DS+DNSKEY for example.com and com, DNSKEY for root.
	if f.calls != 5 {
		t.Fatalf("cold chain took %d fetches, want 5", f.calls)
	}

	// Second validation rides the key cache: zero new fetches.
	f.calls = 0
	if res := v.Validate(context.Background(), msg); res.Status != Secure {
		t.Fatalf("warm: want Secure, got %v (%s)", res.Status, res.Reason)
	}
	if f.calls != 0 {
		t.Fatalf("warm chain took %d fetches, want 0", f.calls)
	}
}

func TestSecureCNAMEChain(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	cname := &dns.CNAME{
		Hdr:    dns.RR_Header{Name: "www.example.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
		Target: "cdn.example.com.",
	}
	a := aRecord("cdn.example.com.", net.IPv4(203, 0, 113, 8))
	m := new(dns.Msg)
	m.SetQuestion("www.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{
		cname, sign(t, z.zsk, z.zskPriv, []dns.RR{cname}, time.Time{}),
		a, sign(t, z.zsk, z.zskPriv, []dns.RR{a}, time.Time{}),
	}
	if res := New(f.resolve, anchors).Validate(context.Background(), m); res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestTamperedAnswerIsBogus(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	msg := signedAnswer(t, zones["example.com."], "www.example.com.")
	msg.Answer[0].(*dns.A).A = net.IPv4(198, 51, 100, 66) // forged after signing

	res := New(f.resolve, anchors).Validate(context.Background(), msg)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func TestExpiredSignatureIsBogus(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	a := aRecord("www.example.com.", net.IPv4(203, 0, 113, 7))
	m := new(dns.Msg)
	m.SetQuestion("www.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a, sign(t, z.zsk, z.zskPriv, []dns.RR{a}, time.Now().Add(-time.Hour))}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "validity period") {
		t.Fatalf("reason should name the validity period, got %q", res.Reason)
	}
}

func TestUnknownKeyTagIsBogus(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	msg := signedAnswer(t, zones["example.com."], "www.example.com.")
	msg.Answer[1].(*dns.RRSIG).KeyTag ^= 0xbeef

	res := New(f.resolve, anchors).Validate(context.Background(), msg)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func TestUnsignedAnswerIsInsecure(t *testing.T) {
	// The delegation walk finds org. provably unsigned (an NS-without-DS
	// NSEC signed by the root), legitimizing the unsigned answer.
	f, zones, anchors := hierarchy(t)
	root := zones["."]
	proof := nsecRR("org.", "org0.", dns.TypeNS)
	f.stageFull("org.", dns.TypeDS, nil,
		[]dns.RR{proof, sign(t, root.zsk, root.zskPriv, []dns.RR{proof}, time.Time{})})
	a := aRecord("www.plain.org.", net.IPv4(203, 0, 113, 9))
	m := new(dns.Msg)
	m.SetQuestion("www.plain.org.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Insecure {
		t.Fatalf("want Insecure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestUnsignedAnswerUnprovableIsIndeterminate(t *testing.T) {
	// Same unsigned answer, but the resolver offers no proof about org.
	// either way: the walk must stay honest, never guess.
	f, _, anchors := hierarchy(t)
	a := aRecord("www.plain.org.", net.IPv4(203, 0, 113, 9))
	m := new(dns.Msg)
	m.SetQuestion("www.plain.org.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Indeterminate {
		t.Fatalf("want Indeterminate, got %v (%s)", res.Status, res.Reason)
	}
}

func TestMissingDSIsInsecure(t *testing.T) {
	// A signed answer whose zone is a proven insecure delegation: the DS
	// response carries com.'s NSEC proving NS-without-DS.
	f, zones, anchors := hierarchy(t)
	com := zones["com."]
	orphan := newTestZone(t, "orphan.com.")
	keyset := []dns.RR{orphan.ksk, orphan.zsk}
	f.stage(orphan.name, dns.TypeDNSKEY, append(keyset, sign(t, orphan.ksk, orphan.kskPriv, keyset, time.Time{}))...)
	proof := nsecRR("orphan.com.", "orphan0.com.", dns.TypeNS)
	f.stageFull(orphan.name, dns.TypeDS, nil,
		[]dns.RR{proof, sign(t, com.zsk, com.zskPriv, []dns.RR{proof}, time.Time{})})

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, orphan, "www.orphan.com."))
	if res.Status != Insecure {
		t.Fatalf("want Insecure, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "no DS record") {
		t.Fatalf("reason should name the missing DS, got %q", res.Reason)
	}
}

func TestUnprovenDSIsIndeterminate(t *testing.T) {
	// The same shape without any proof in the DS response: the chain
	// cannot tell an unsigned delegation from a stripped one.
	f, _, anchors := hierarchy(t)
	orphan := newTestZone(t, "unproven.com.")
	keyset := []dns.RR{orphan.ksk, orphan.zsk}
	f.stage(orphan.name, dns.TypeDNSKEY, append(keyset, sign(t, orphan.ksk, orphan.kskPriv, keyset, time.Time{}))...)

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, orphan, "www.unproven.com."))
	if res.Status != Indeterminate {
		t.Fatalf("want Indeterminate, got %v (%s)", res.Status, res.Reason)
	}
}

func TestDSDigestMismatchIsBogus(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	com := zones["com."]
	bad := newTestZone(t, "badds.com.")
	keyset := []dns.RR{bad.ksk, bad.zsk}
	f.stage(bad.name, dns.TypeDNSKEY, append(keyset, sign(t, bad.ksk, bad.kskPriv, keyset, time.Time{}))...)
	ds := bad.ksk.ToDS(dns.SHA256)
	ds.Hdr.Ttl = 3600
	ds.Digest = strings.Repeat("00", len(ds.Digest)/2) // wrong digest
	dsSet := []dns.RR{ds}
	f.stage(bad.name, dns.TypeDS, append(dsSet, sign(t, com.zsk, com.zskPriv, dsSet, time.Time{}))...)

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, bad, "www.badds.com."))
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "matches its DS") {
		t.Fatalf("reason should name the DS mismatch, got %q", res.Reason)
	}
}

func TestZSKOnlySignedDNSKEYIsBogus(t *testing.T) {
	// The DNSKEY RRset must be blessed by a DS-matched key; a ZSK signing
	// itself proves nothing.
	f, zones, anchors := hierarchy(t)
	com := zones["com."]
	z := newTestZone(t, "zskonly.com.")
	keyset := []dns.RR{z.ksk, z.zsk}
	f.stage(z.name, dns.TypeDNSKEY, append(keyset, sign(t, z.zsk, z.zskPriv, keyset, time.Time{}))...)
	ds := z.ksk.ToDS(dns.SHA256)
	ds.Hdr.Ttl = 3600
	dsSet := []dns.RR{ds}
	f.stage(z.name, dns.TypeDS, append(dsSet, sign(t, com.zsk, com.zskPriv, dsSet, time.Time{}))...)

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, z, "www.zskonly.com."))
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func TestUnsupportedDSAlgorithmIsInsecure(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	com := zones["com."]
	z := newTestZone(t, "future.com.")
	ds := z.ksk.ToDS(dns.SHA256)
	ds.Hdr.Ttl = 3600
	ds.Algorithm = 99 // an algorithm we cannot verify
	dsSet := []dns.RR{ds}
	f.stage(z.name, dns.TypeDS, append(dsSet, sign(t, com.zsk, com.zskPriv, dsSet, time.Time{}))...)

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, z, "www.future.com."))
	if res.Status != Insecure {
		t.Fatalf("want Insecure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestForeignSignerIsStrippingAttack(t *testing.T) {
	// A signature from a zone that cannot contain the owner is ignored,
	// leaving an unsigned answer — and the delegation walk proves
	// example.com. is signed all the way down, so this is condemned as
	// signature stripping, not passed as insecure.
	f, zones, anchors := hierarchy(t)
	example := zones["example.com."]
	evil := newTestZone(t, "evil.org.")
	notCut := nsecRR("www.example.com.", "zzz.example.com.", dns.TypeA, dns.TypeRRSIG, dns.TypeNSEC)
	f.stageFull("www.example.com.", dns.TypeDS, nil,
		[]dns.RR{notCut, sign(t, example.zsk, example.zskPriv, []dns.RR{notCut}, time.Time{})})
	a := aRecord("www.example.com.", net.IPv4(198, 51, 100, 66))
	m := new(dns.Msg)
	m.SetQuestion("www.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a, sign(t, evil.zsk, evil.zskPriv, []dns.RR{a}, time.Time{})}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus (stripping under signed chain), got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "signed zone") {
		t.Fatalf("reason should name the signed zone, got %q", res.Reason)
	}
}

func TestDecoySignerCannotDowngrade(t *testing.T) {
	// A hostile message prepends a signature naming a different plausible
	// signer (com. can contain www.example.com.); the real signature must
	// still win. RFC 4035 §5.3.3: any one verifying signature suffices.
	f, zones, anchors := hierarchy(t)
	msg := signedAnswer(t, zones["example.com."], "www.example.com.")
	real := msg.Answer[1].(*dns.RRSIG)
	decoy := *real
	decoy.SignerName = "com."
	decoy.KeyTag = zones["com."].zsk.KeyTag()
	msg.Answer = []dns.RR{msg.Answer[0], &decoy, real}

	res := New(f.resolve, anchors).Validate(context.Background(), msg)
	if res.Status != Secure {
		t.Fatalf("want Secure despite decoy, got %v (%s)", res.Status, res.Reason)
	}
}

func TestChainFetchErrorIsIndeterminate(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	f.errs["example.com.|DS"] = errors.New("upstream timeout")

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, zones["example.com."], "www.example.com."))
	if res.Status != Indeterminate {
		t.Fatalf("want Indeterminate, got %v (%s)", res.Status, res.Reason)
	}
}

func TestFetchBudgetExhaustionIsIndeterminate(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	v := New(f.resolve, anchors, WithBudgets(2, 32)) // chain needs 5

	res := v.Validate(context.Background(), signedAnswer(t, zones["example.com."], "www.example.com."))
	if res.Status != Indeterminate {
		t.Fatalf("want Indeterminate, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "budget") {
		t.Fatalf("reason should name the budget, got %q", res.Reason)
	}
}

func TestVerifyBudgetBoundsKeyTrap(t *testing.T) {
	// KeyTrap-style: many colliding signatures must cost bounded crypto,
	// then fail closed as Bogus.
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	a := aRecord("www.example.com.", net.IPv4(203, 0, 113, 7))
	rrset := []dns.RR{a}
	m := new(dns.Msg)
	m.SetQuestion("www.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = rrset
	for range maxSigsPerRRset {
		s := sign(t, z.zsk, z.zskPriv, rrset, time.Time{})
		s.Signature = s.Signature[:len(s.Signature)-4] + "AAAA" // corrupt
		m.Answer = append(m.Answer, s)
	}

	v := New(f.resolve, anchors, WithBudgets(24, 3))
	res := v.Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "budget") {
		t.Fatalf("reason should name the budget, got %q", res.Reason)
	}
}

// wildcardAnswer builds a real wildcard expansion: the zone signs
// *.wild.example.com. and the server serves it under the queried name
// with the wildcard signature.
func wildcardAnswer(t testing.TB, z *testZone) *dns.Msg {
	t.Helper()
	a := aRecord("*.wild.example.com.", net.IPv4(203, 0, 113, 7))
	sig := sign(t, z.zsk, z.zskPriv, []dns.RR{a}, time.Time{})
	a.Hdr.Name = "host.wild.example.com."
	sig.Hdr.Name = a.Hdr.Name
	m := new(dns.Msg)
	m.SetQuestion("host.wild.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a, sig}
	return m
}

func TestWildcardWithoutProofIsBogus(t *testing.T) {
	// RFC 4035 §5.3.4: a wildcard expansion must prove the queried name
	// itself does not exist; absent that proof it could be masking a real
	// record.
	f, zones, anchors := hierarchy(t)
	m := wildcardAnswer(t, zones["example.com."])

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "next-closer") {
		t.Fatalf("reason should name the next-closer proof, got %q", res.Reason)
	}
}

func TestWildcardWithProofIsSecure(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	m := wildcardAnswer(t, z)
	proof := nsecRR("wild.example.com.", "zz.wild.example.com.", dns.TypeA)
	m.Ns = []dns.RR{proof, sign(t, z.zsk, z.zskPriv, []dns.RR{proof}, time.Time{})}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestLiteralWildcardOwnerIsSecure(t *testing.T) {
	// An RRset owned by the wildcard name itself (a direct query for
	// *.wild.example.com.) is not an expansion and needs no proof.
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	a := aRecord("*.wild.example.com.", net.IPv4(203, 0, 113, 7))
	m := new(dns.Msg)
	m.SetQuestion("*.wild.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{a, sign(t, z.zsk, z.zskPriv, []dns.RR{a}, time.Time{})}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNegativeAnswersAreIndeterminate(t *testing.T) {
	f, _, anchors := hierarchy(t)
	v := New(f.resolve, anchors)

	nx := new(dns.Msg)
	nx.SetQuestion("gone.example.com.", dns.TypeA)
	nx.Response = true
	nx.Rcode = dns.RcodeNameError
	if res := v.Validate(context.Background(), nx); res.Status != Indeterminate {
		t.Fatalf("NXDOMAIN: want Indeterminate, got %v (%s)", res.Status, res.Reason)
	}

	nodata := new(dns.Msg)
	nodata.SetQuestion("www.example.com.", dns.TypeAAAA)
	nodata.Response = true
	if res := v.Validate(context.Background(), nodata); res.Status != Indeterminate {
		t.Fatalf("NODATA: want Indeterminate, got %v (%s)", res.Status, res.Reason)
	}
}

func TestRevokedAndNonZoneKeysAreExcluded(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	com := zones["com."]
	z := newTestZone(t, "revoked.com.")
	z.ksk.Flags |= dns.REVOKE // revoked entry point must not be trusted
	keyset := []dns.RR{z.ksk, z.zsk}
	f.stage(z.name, dns.TypeDNSKEY, append(keyset, sign(t, z.ksk, z.kskPriv, keyset, time.Time{}))...)
	ds := z.ksk.ToDS(dns.SHA256)
	ds.Hdr.Ttl = 3600
	dsSet := []dns.RR{ds}
	f.stage(z.name, dns.TypeDS, append(dsSet, sign(t, com.zsk, com.zskPriv, dsSet, time.Time{}))...)

	res := New(f.resolve, anchors).Validate(context.Background(), signedAnswer(t, z, "www.revoked.com."))
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func TestRealRootAnchorsParse(t *testing.T) {
	anchors := RootAnchors()
	if len(anchors) != 2 {
		t.Fatalf("want 2 anchors, got %d", len(anchors))
	}
	tags := map[uint16]bool{}
	for _, ds := range anchors {
		if ds.Hdr.Name != "." || ds.Algorithm != dns.RSASHA256 || ds.DigestType != dns.SHA256 {
			t.Fatalf("anchor %d has unexpected fields: %v", ds.KeyTag, ds)
		}
		if len(ds.Digest) != 64 {
			t.Fatalf("anchor %d digest is not SHA-256 length: %q", ds.KeyTag, ds.Digest)
		}
		tags[ds.KeyTag] = true
	}
	if !tags[20326] || !tags[38696] {
		t.Fatalf("want KSK-2017 (20326) and KSK-2024 (38696), got %v", tags)
	}
}

func TestKeyCacheEviction(t *testing.T) {
	c := newKeyCache(2)
	now := time.Now()
	key, _ := genKey(t, "a.", 257)
	for i := range 5 {
		c.put(fmt.Sprintf("z%d.", i), []*dns.DNSKEY{key}, 3600, now)
	}
	if size := c.size.Load(); size > 3 {
		t.Fatalf("cache exceeded its cap: %d entries", size)
	}
}
