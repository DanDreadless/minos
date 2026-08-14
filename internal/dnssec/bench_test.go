package dnssec

import (
	"context"
	"testing"
)

// BenchmarkValidateWarm is the steady-state cost a validating forwarder
// adds to a cache-missed answer: zone keys already validated and cached,
// one signature verification over the answer RRset.
func BenchmarkValidateWarm(b *testing.B) {
	f, zones, anchors := hierarchy(b)
	v := New(f.resolve, anchors)
	msg := signedAnswer(b, zones["example.com."], "www.example.com.")
	if res := v.Validate(context.Background(), msg); res.Status != Secure {
		b.Fatalf("prime failed: %v (%s)", res.Status, res.Reason)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if res := v.Validate(context.Background(), msg); res.Status != Secure {
			b.Fatal(res.Reason)
		}
	}
}

// BenchmarkValidateCold is the full chain walk with an empty key cache:
// five (free, in-memory) chain fetches plus every verification down from
// the root anchor. Real-world cold cost adds upstream RTTs on top.
func BenchmarkValidateCold(b *testing.B) {
	f, zones, anchors := hierarchy(b)
	msg := signedAnswer(b, zones["example.com."], "www.example.com.")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		v := New(f.resolve, anchors)
		if res := v.Validate(context.Background(), msg); res.Status != Secure {
			b.Fatal(res.Reason)
		}
	}
}
