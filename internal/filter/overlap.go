package filter

import "bytes"

// ListOverlap is how one tracked list's exact-domain rules overlap the
// other tracked lists (regex rules are not compared).
type ListOverlap struct {
	List string
	// Rules is the exact-domain rules the list contains.
	Rules int
	// Unique is how many of them no other tracked list covers — neither the
	// same domain nor a parent of it.
	Unique int
	// CoveredBy is, for each other tracked list, how many of this list's
	// rules it covers.
	CoveredBy map[string]int
	// FreedBytes is roughly the memory removing the list would free: the
	// rules no other list has exactly (slab bytes, index and bits).
	FreedBytes int64
}

// Tracked returns the lists the overlap report compares.
func (m *Matcher) Tracked() []string { return m.tracked }

// Overlap reports, for each tracked list, how its rules overlap the others.
// Computed once per matcher, on first use, in a single pass over the sorted
// rules: a domain's parents sort directly before it, so a stack of the
// ancestors seen so far gives each rule's coverage without a lookup. Nil
// when fewer than two lists were tracked.
func (m *Matcher) Overlap() []ListOverlap {
	m.overlapOnce.Do(func() { m.overlap = m.computeOverlap() })
	return m.overlap
}

func (m *Matcher) computeOverlap() []ListOverlap {
	n := len(m.tracked)
	s := &m.deny
	if n == 0 || s.masks == nil {
		return nil
	}
	out := make([]ListOverlap, n)
	covered := make([][]int, n)
	for l := range out {
		out[l].List = m.tracked[l]
		covered[l] = make([]int, n)
	}
	type ancestor struct {
		key []byte
		eff uint16 // Lists blocking this key, directly or through a parent
	}
	var stack []ancestor
	for i := range s.idx {
		key := s.key(i)
		for len(stack) > 0 && !bytes.HasPrefix(key, stack[len(stack)-1].key) {
			stack = stack[:len(stack)-1]
		}
		own := s.masks[i]
		eff := own
		if len(stack) > 0 {
			eff |= stack[len(stack)-1].eff
		}
		if eff != 0 {
			stack = append(stack, ancestor{key, eff})
		}
		if own == 0 {
			continue
		}
		for l := range n {
			bit := uint16(1) << l
			if own&bit == 0 {
				continue
			}
			out[l].Rules++
			others := eff &^ bit
			if others == 0 {
				out[l].Unique++
			}
			for o := range n {
				if others&(1<<o) != 0 {
					covered[l][o]++
				}
			}
			if own == bit {
				out[l].FreedBytes += int64(len(key)) + 10 // Key, 8-byte index entry, 2-byte bits
			}
		}
	}
	for l := range out {
		out[l].CoveredBy = map[string]int{}
		for o, c := range covered[l] {
			if c > 0 {
				out[l].CoveredBy[m.tracked[o]] = c
			}
		}
	}
	return out
}

// Covering reports which tracked lists block qname, through the name itself
// or a parent — the bits follow Tracked's order. Off the hot path: the
// overlap report asks it about names that were blocked.
func (m *Matcher) Covering(qname string) uint16 {
	s := &m.deny
	if s.masks == nil {
		return 0
	}
	rev := reverseLabels(qname)
	var mask uint16
	for i := 0; i < len(rev); i++ {
		if rev[i] != '.' {
			continue
		}
		if p, ok := s.find(rev[:i+1]); ok {
			mask |= s.masks[p]
		}
	}
	return mask
}
