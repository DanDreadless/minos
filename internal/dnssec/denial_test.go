package dnssec

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func nsecRR(owner, next string, types ...uint16) *dns.NSEC {
	return &dns.NSEC{
		Hdr:        dns.RR_Header{Name: owner, Rrtype: dns.TypeNSEC, Class: dns.ClassINET, Ttl: 300},
		NextDomain: next,
		TypeBitMap: types,
	}
}

// negMsg builds a negative response under test (rcode + authority proofs).
func negMsg(qname string, qtype uint16, rcode int, ns ...dns.RR) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(qname, qtype)
	m.Response = true
	m.Rcode = rcode
	m.Ns = ns
	return m
}

func TestCanonicalOrdering(t *testing.T) {
	// The RFC 4034 §6.1 example order (printable subset).
	order := []string{
		"example.",
		"a.example.",
		"yljkjljk.a.example.",
		"Z.a.example.",
		"zABC.a.EXAMPLE.",
		"z.example.",
		"*.z.example.",
	}
	for i := range order[:len(order)-1] {
		if canonicalCmp(order[i], order[i+1]) >= 0 {
			t.Fatalf("want %q < %q", order[i], order[i+1])
		}
		if canonicalCmp(order[i+1], order[i]) <= 0 {
			t.Fatalf("want %q > %q", order[i+1], order[i])
		}
	}
	if canonicalCmp("Www.Example.COM.", "www.example.com.") != 0 {
		t.Fatal("comparison must be case-insensitive")
	}
}

func TestNSECCoverWrap(t *testing.T) {
	// The zone's last NSEC wraps to the apex: it covers everything after
	// its owner, and nothing inside the ordinary range before it.
	if !nsecCovers("z.example.com.", "example.com.", "zz.example.com.") {
		t.Fatal("wrap range must cover names after the owner")
	}
	if nsecCovers("z.example.com.", "example.com.", "a.example.com.") {
		t.Fatal("wrap range must not cover names before the owner")
	}
	if nsecCovers("a.example.com.", "c.example.com.", "c.example.com.") {
		t.Fatal("covering is strict: the next name is not covered")
	}
}

func TestNXDomainNSECSecure(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	span := nsecRR("a.example.com.", "c.example.com.", dns.TypeA)
	apex := nsecRR("example.com.", "a.example.com.", dns.TypeSOA, dns.TypeNS, dns.TypeDNSKEY)
	m := negMsg("b.example.com.", dns.TypeA, dns.RcodeNameError,
		span, sign(t, z.zsk, z.zskPriv, []dns.RR{span}, time.Time{}),
		apex, sign(t, z.zsk, z.zskPriv, []dns.RR{apex}, time.Time{}))

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNXDomainMissingWildcardProofIsBogus(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	span := nsecRR("a.example.com.", "c.example.com.", dns.TypeA)
	m := negMsg("b.example.com.", dns.TypeA, dns.RcodeNameError,
		span, sign(t, z.zsk, z.zskPriv, []dns.RR{span}, time.Time{}))

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "wildcard") {
		t.Fatalf("reason should name the wildcard gap, got %q", res.Reason)
	}
}

func TestNodataNSECSecure(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	match := nsecRR("www.example.com.", "zzz.example.com.", dns.TypeA, dns.TypeRRSIG, dns.TypeNSEC)
	m := negMsg("www.example.com.", dns.TypeAAAA, dns.RcodeSuccess,
		match, sign(t, z.zsk, z.zskPriv, []dns.RR{match}, time.Time{}))

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNodataForPresentTypeIsBogus(t *testing.T) {
	// The bitmap says A exists, yet the answer for A came back empty.
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	match := nsecRR("www.example.com.", "zzz.example.com.", dns.TypeA)
	m := negMsg("www.example.com.", dns.TypeA, dns.RcodeSuccess,
		match, sign(t, z.zsk, z.zskPriv, []dns.RR{match}, time.Time{}))

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func TestAncestorDelegationNSECCannotProveNodata(t *testing.T) {
	// RFC 6840 §4.1: a parent-side NSEC (NS without SOA) says nothing
	// about names inside the child zone.
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	parentSide := nsecRR("sub.example.com.", "zzz.example.com.", dns.TypeNS)
	m := negMsg("sub.example.com.", dns.TypeA, dns.RcodeSuccess,
		parentSide, sign(t, z.zsk, z.zskPriv, []dns.RR{parentSide}, time.Time{}))

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNXDomainAfterCNAMEChain(t *testing.T) {
	// The denial subject is the end of the CNAME chain, not the qname.
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	cname := &dns.CNAME{
		Hdr:    dns.RR_Header{Name: "www.example.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
		Target: "dead.example.com.",
	}
	span := nsecRR("c.example.com.", "e.example.com.", dns.TypeA)
	apex := nsecRR("example.com.", "a.example.com.", dns.TypeSOA, dns.TypeNS)
	m := negMsg("www.example.com.", dns.TypeA, dns.RcodeNameError,
		span, sign(t, z.zsk, z.zskPriv, []dns.RR{span}, time.Time{}),
		apex, sign(t, z.zsk, z.zskPriv, []dns.RR{apex}, time.Time{}))
	m.Answer = []dns.RR{cname, sign(t, z.zsk, z.zskPriv, []dns.RR{cname}, time.Time{})}

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestBogusProofSignatureSurfaces(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	z := zones["example.com."]
	span := nsecRR("a.example.com.", "c.example.com.", dns.TypeA)
	sig := sign(t, z.zsk, z.zskPriv, []dns.RR{span}, time.Time{})
	span.NextDomain = "d.example.com." // tampered after signing
	m := negMsg("b.example.com.", dns.TypeA, dns.RcodeNameError, span, sig)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

// --- NSEC3 ---

const base32hexAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUV"

// adjacentHash returns the base32hex string immediately before (delta -1)
// or after (delta +1) h in sort order, carrying across alphabet edges.
func adjacentHash(t *testing.T, h string, delta int) string {
	t.Helper()
	out := []byte(h)
	for i := len(out) - 1; i >= 0; i-- {
		p := strings.IndexByte(base32hexAlphabet, out[i]) + delta
		switch {
		case p < 0:
			out[i] = base32hexAlphabet[len(base32hexAlphabet)-1] // borrow left
		case p >= len(base32hexAlphabet):
			out[i] = base32hexAlphabet[0] // carry left
		default:
			out[i] = base32hexAlphabet[p]
			return string(out)
		}
	}
	t.Fatalf("hash %q wrapped entirely", h)
	return ""
}

func n3RR(zone, ownerHash, next string, iterations uint16, optout bool, types ...uint16) *dns.NSEC3 {
	var flags uint8
	if optout {
		flags = 1
	}
	return &dns.NSEC3{
		Hdr:        dns.RR_Header{Name: strings.ToLower(ownerHash) + "." + zone, Rrtype: dns.TypeNSEC3, Class: dns.ClassINET, Ttl: 300},
		Hash:       dns.SHA1,
		Flags:      flags,
		Iterations: iterations,
		SaltLength: 0,
		Salt:       "",
		HashLength: 20,
		NextDomain: next,
		TypeBitMap: types,
	}
}

// n3Zone stages a signed zone n3.com. and returns it with a signer helper.
func n3Zone(t *testing.T, f *fakeResolver, zones map[string]*testZone) (*testZone, func(rr dns.RR) []dns.RR) {
	t.Helper()
	com := zones["com."]
	z := newTestZone(t, "n3.com.")
	keyset := []dns.RR{z.ksk, z.zsk}
	f.stage(z.name, dns.TypeDNSKEY, append(keyset, sign(t, z.ksk, z.kskPriv, keyset, time.Time{}))...)
	ds := z.ksk.ToDS(dns.SHA256)
	ds.Hdr.Ttl = 3600
	dsSet := []dns.RR{ds}
	f.stage(z.name, dns.TypeDS, append(dsSet, sign(t, com.zsk, com.zskPriv, dsSet, time.Time{}))...)
	signed := func(rr dns.RR) []dns.RR {
		return []dns.RR{rr, sign(t, z.zsk, z.zskPriv, []dns.RR{rr}, time.Time{})}
	}
	return z, signed
}

func TestNXDomainNSEC3Secure(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	_, signed := n3Zone(t, f, zones)
	hCE := dns.HashName("n3.com.", dns.SHA1, 0, "")
	hNC := dns.HashName("gone.n3.com.", dns.SHA1, 0, "")
	hWC := dns.HashName("*.n3.com.", dns.SHA1, 0, "")
	var ns []dns.RR
	ns = append(ns, signed(n3RR("n3.com.", hCE, adjacentHash(t, hCE, 1), 0, false, dns.TypeSOA, dns.TypeNS))...)
	ns = append(ns, signed(n3RR("n3.com.", adjacentHash(t, hNC, -1), adjacentHash(t, hNC, 1), 0, false))...)
	ns = append(ns, signed(n3RR("n3.com.", adjacentHash(t, hWC, -1), adjacentHash(t, hWC, 1), 0, false))...)
	m := negMsg("gone.n3.com.", dns.TypeA, dns.RcodeNameError, ns...)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNXDomainNSEC3OptOutIsInsecure(t *testing.T) {
	// An opt-out span cannot disprove an insecure delegation, so the
	// NXDOMAIN degrades to insecure — never secure (RFC 5155 §9.2).
	f, zones, anchors := hierarchy(t)
	_, signed := n3Zone(t, f, zones)
	hCE := dns.HashName("n3.com.", dns.SHA1, 0, "")
	hNC := dns.HashName("gone.n3.com.", dns.SHA1, 0, "")
	var ns []dns.RR
	ns = append(ns, signed(n3RR("n3.com.", hCE, adjacentHash(t, hCE, 1), 0, false, dns.TypeSOA, dns.TypeNS))...)
	ns = append(ns, signed(n3RR("n3.com.", adjacentHash(t, hNC, -1), adjacentHash(t, hNC, 1), 0, true))...)
	m := negMsg("gone.n3.com.", dns.TypeA, dns.RcodeNameError, ns...)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Insecure {
		t.Fatalf("want Insecure, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "opt-out") {
		t.Fatalf("reason should name opt-out, got %q", res.Reason)
	}
}

func TestNSEC3IterationsOverCapIsInsecure(t *testing.T) {
	// RFC 9276: refuse to grind attacker-priced SHA-1; the zone is
	// treated as insecure, resolution never breaks.
	f, zones, anchors := hierarchy(t)
	_, signed := n3Zone(t, f, zones)
	hNC := dns.HashName("gone.n3.com.", dns.SHA1, 150, "")
	ns := signed(n3RR("n3.com.", adjacentHash(t, hNC, -1), adjacentHash(t, hNC, 1), 150, false))
	m := negMsg("gone.n3.com.", dns.TypeA, dns.RcodeNameError, ns...)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Insecure {
		t.Fatalf("want Insecure, got %v (%s)", res.Status, res.Reason)
	}
	if !strings.Contains(res.Reason, "iterations") {
		t.Fatalf("reason should name iterations, got %q", res.Reason)
	}
}

func TestNodataNSEC3Secure(t *testing.T) {
	f, zones, anchors := hierarchy(t)
	_, signed := n3Zone(t, f, zones)
	h := dns.HashName("host.n3.com.", dns.SHA1, 0, "")
	ns := signed(n3RR("n3.com.", h, adjacentHash(t, h, 1), 0, false, dns.TypeA))
	m := negMsg("host.n3.com.", dns.TypeAAAA, dns.RcodeSuccess, ns...)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Secure {
		t.Fatalf("want Secure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNegativeFromUnsignedZoneIsInsecure(t *testing.T) {
	// An NXDOMAIN with no proofs from a provably unsigned zone passes as
	// insecure via the delegation walk.
	f, zones, anchors := hierarchy(t)
	root := zones["."]
	proof := nsecRR("org.", "org0.", dns.TypeNS)
	f.stageFull("org.", dns.TypeDS, nil,
		[]dns.RR{proof, sign(t, root.zsk, root.zskPriv, []dns.RR{proof}, time.Time{})})
	m := negMsg("gone.plain.org.", dns.TypeA, dns.RcodeNameError)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Insecure {
		t.Fatalf("want Insecure, got %v (%s)", res.Status, res.Reason)
	}
}

func TestNegativeWithoutProofFromSignedZoneIsBogus(t *testing.T) {
	// example.com is provably signed down to the leaf: an NXDOMAIN with
	// no denial proof is withholding evidence.
	f, zones, anchors := hierarchy(t)
	example := zones["example.com."]
	notCut := nsecRR("gone.example.com.", "zzz.example.com.", dns.TypeA)
	f.stageFull("gone.example.com.", dns.TypeDS, nil,
		[]dns.RR{notCut, sign(t, example.zsk, example.zskPriv, []dns.RR{notCut}, time.Time{})})
	m := negMsg("gone.example.com.", dns.TypeA, dns.RcodeNameError)

	res := New(f.resolve, anchors).Validate(context.Background(), m)
	if res.Status != Bogus {
		t.Fatalf("want Bogus, got %v (%s)", res.Status, res.Reason)
	}
}

func BenchmarkValidateNXDomainNSEC(b *testing.B) {
	f, zones, anchors := hierarchy(b)
	z := zones["example.com."]
	span := nsecRR("a.example.com.", "c.example.com.", dns.TypeA)
	apex := nsecRR("example.com.", "a.example.com.", dns.TypeSOA, dns.TypeNS, dns.TypeDNSKEY)
	m := negMsg("b.example.com.", dns.TypeA, dns.RcodeNameError,
		span, sign(b, z.zsk, z.zskPriv, []dns.RR{span}, time.Time{}),
		apex, sign(b, z.zsk, z.zskPriv, []dns.RR{apex}, time.Time{}))
	v := New(f.resolve, anchors)
	if res := v.Validate(context.Background(), m); res.Status != Secure {
		b.Fatalf("prime failed: %v (%s)", res.Status, res.Reason)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if res := v.Validate(context.Background(), m); res.Status != Secure {
			b.Fatal(res.Reason)
		}
	}
}
