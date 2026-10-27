package filter

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

func TestOverlap(t *testing.T) {
	b := NewBuilder()
	b.Track([]string{"A", "B", "C"})
	b.AddDeny("denylist", "only-a.com") // Not tracked: doesn't count as cover
	for _, d := range []string{"x.com", "ads.y.com", "only-a.com", "x-y.com"} {
		b.AddDeny("A", d)
	}
	for _, d := range []string{"y.com", "x.com"} {
		b.AddDeny("B", d)
	}
	b.AddDeny("C", "z.org")
	m := b.Build()

	got := map[string]ListOverlap{}
	for _, o := range m.Overlap() {
		got[o.List] = o
	}
	a := got["A"]
	if a.Rules != 4 || a.Unique != 2 || !reflect.DeepEqual(a.CoveredBy, map[string]int{"B": 2}) {
		t.Errorf("A: %+v", a)
	}
	// Freed: ads.y.com, only-a.com and x-y.com are A's alone (x.com is B's too).
	if want := int64(len("com.y.ads.")+len("com.only-a.")+len("com.x-y.")) + 30; a.FreedBytes != want {
		t.Errorf("A freed %d, want %d", a.FreedBytes, want)
	}
	if bb := got["B"]; bb.Rules != 2 || bb.Unique != 1 || !reflect.DeepEqual(bb.CoveredBy, map[string]int{"A": 1}) {
		t.Errorf("B: %+v", bb)
	}
	if c := got["C"]; c.Rules != 1 || c.Unique != 1 || len(c.CoveredBy) != 0 {
		t.Errorf("C: %+v", c)
	}

	for name, want := range map[string]uint16{
		"foo.ads.y.com": 0b011, // A directly, B through y.com
		"x.com":         0b011,
		"z.org":         0b100,
		"x-y.com":       0b001,
		"nothing.org":   0,
	} {
		if got := m.Covering(name); got != want {
			t.Errorf("Covering(%q) = %03b, want %03b", name, got, want)
		}
	}
	// Matching is unchanged by tracking.
	if r := m.Match("foo.ads.y.com"); !r.Blocked || r.List != "B" || r.Rule != "y.com" {
		t.Errorf("match %+v", r)
	}
}

func TestOverlapNeedsTwoLists(t *testing.T) {
	b := NewBuilder()
	b.Track([]string{"A"})
	b.AddDeny("A", "x.com")
	m := b.Build()
	if m.Overlap() != nil || m.Covering("x.com") != 0 || m.deny.masks != nil {
		t.Error("one tracked list still paid for the overlap bits")
	}
}

// TestOverlapMatchesBruteForce holds the single-pass stack algorithm to a
// plain answer on random names drawn from a tiny alphabet, so parents,
// near-misses ("a-b" against "a") and shared rules are everywhere.
func TestOverlapMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	labels := []string{"a", "b", "a-b", "ab", "c"}
	name := func() string {
		n := 1 + r.IntN(3)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = labels[r.IntN(len(labels))]
		}
		return strings.Join(parts, ".") + ".test"
	}
	for round := range 20 {
		lists := []string{"L0", "L1", "L2", "L3"}
		rules := map[string]map[string]bool{}
		b := NewBuilder()
		b.Track(lists)
		for _, l := range lists {
			rules[l] = map[string]bool{}
			for range 40 {
				d := name()
				rules[l][d] = true
				b.AddDeny(l, d)
			}
		}
		m := b.Build()

		covers := func(l, d string) bool {
			for p := d; ; {
				if rules[l][p] {
					return true
				}
				i := strings.IndexByte(p, '.')
				if i < 0 {
					return false
				}
				p = p[i+1:]
			}
		}
		for _, o := range m.Overlap() {
			want := ListOverlap{List: o.List, CoveredBy: map[string]int{}}
			for d := range rules[o.List] {
				want.Rules++
				alone := true
				for _, other := range lists {
					if other != o.List && covers(other, d) {
						want.CoveredBy[other]++
						alone = false
					}
				}
				if alone {
					want.Unique++
				}
			}
			o.FreedBytes = 0
			if !reflect.DeepEqual(o, want) {
				t.Fatalf("round %d %s:\n got %+v\nwant %+v", round, o.List, o, want)
			}
		}
		for range 50 {
			d := "x." + name()
			var want uint16
			for i, l := range lists {
				if covers(l, d) {
					want |= 1 << i
				}
			}
			if got := m.Covering(d); got != want {
				t.Fatalf("Covering(%q) = %04b, want %04b", d, got, want)
			}
		}
	}
}

func TestTrackCapsAtMax(t *testing.T) {
	b := NewBuilder()
	var lists []string
	for i := range MaxTracked + 3 {
		lists = append(lists, fmt.Sprintf("l%d", i))
	}
	b.Track(lists)
	for _, l := range lists {
		b.AddDeny(l, l+".com")
	}
	if got := len(b.Build().Overlap()); got != MaxTracked {
		t.Errorf("%d lists compared, want %d", got, MaxTracked)
	}
}

// BenchmarkBuild100kTracked is BenchmarkBuild100k with two tracked lists
// sharing half their rules: the cost of recording the overlap bits.
func BenchmarkBuild100kTracked(b *testing.B) {
	domains := make([]string, 100_000)
	for i := range domains {
		domains[i] = fmt.Sprintf("host%d.tracker%d.example.com", i, i%1000)
	}
	b.ReportAllocs()
	for b.Loop() {
		bl := NewBuilder()
		bl.Track([]string{"one", "two"})
		for _, d := range domains[:75_000] {
			bl.AddDeny("one", d)
		}
		for _, d := range domains[25_000:] {
			bl.AddDeny("two", d)
		}
		if bl.Build().Rules() != len(domains) {
			b.Fatal("bad build")
		}
	}
}

// BenchmarkOverlap1M is the report's one-off pass over 1M rules in three
// lists, parents included.
func BenchmarkOverlap1M(b *testing.B) {
	bl := NewBuilder()
	bl.Track([]string{"one", "two", "three"})
	for i := range 1_000_000 {
		d := fmt.Sprintf("host%d.tracker%d.example.com", i, i%1000)
		bl.AddDeny([]string{"one", "two", "three"}[i%3], d)
		if i%7 == 0 {
			bl.AddDeny("two", d)
		}
	}
	for i := range 1000 {
		bl.AddDeny("three", fmt.Sprintf("tracker%d.example.com", i))
	}
	m := bl.Build()
	b.ReportAllocs()
	for b.Loop() {
		if len(m.computeOverlap()) != 3 {
			b.Fatal("bad overlap")
		}
	}
}
