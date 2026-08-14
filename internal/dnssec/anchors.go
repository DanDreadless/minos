package dnssec

import "github.com/miekg/dns"

// RootAnchors returns the DNS root trust anchors in DS form, verified
// against https://data.iana.org/root-anchors/root-anchors.xml (2026-08-14):
// KSK-2017 (key tag 20326) and KSK-2024 (key tag 38696), both algorithm 8
// (RSASHA256) with SHA-256 digests. A root KSK roll ships as a Minos
// release; RFC 5011 rollover tracking is deliberately out of scope.
func RootAnchors() []*dns.DS {
	hdr := dns.RR_Header{Name: ".", Rrtype: dns.TypeDS, Class: dns.ClassINET}
	return []*dns.DS{
		{Hdr: hdr, KeyTag: 20326, Algorithm: dns.RSASHA256, DigestType: dns.SHA256,
			Digest: "e06d44b80b8f1d39a95c0b0d7c65d08458e880409bbc683457104237c7f8ec8d"},
		{Hdr: hdr, KeyTag: 38696, Algorithm: dns.RSASHA256, DigestType: dns.SHA256,
			Digest: "683d2d0acb8c9b712a1948b27f741219298d0a450d612c483af444a4c0fb2b16"},
	}
}
