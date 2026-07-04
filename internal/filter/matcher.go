// Package filter is the blocklist engine: it compiles list entries into an
// immutable Matcher and judges query names against it.
//
// Matchers are never mutated after Build; reloads construct a fresh Matcher
// off the hot path and swap it into the Engine atomically.
package filter

import (
	"sort"
	"strings"
)

// Result is the verdict for a single query name.
type Result struct {
	Blocked bool
	// List and Rule identify exactly why a name was blocked (or pardoned) —
	// surfacing this in the UI is a core product feature.
	List string
	Rule string
}

// Builder accumulates list entries and compiles them into a Matcher.
type Builder struct {
	lists   []string
	listIdx map[string]int32
	deny    map[string]int32 // reversed-label domain → index into lists
	allow   map[string]int32
	skipped int
}

func NewBuilder() *Builder {
	return &Builder{
		listIdx: make(map[string]int32),
		deny:    make(map[string]int32),
		allow:   make(map[string]int32),
	}
}

func (b *Builder) listIndex(list string) int32 {
	if idx, ok := b.listIdx[list]; ok {
		return idx
	}
	idx := int32(len(b.lists))
	b.lists = append(b.lists, list)
	b.listIdx[list] = idx
	return idx
}

// AddDeny records domain (and its subdomains) as blocked by list.
// Invalid domains are counted as skipped. First list to claim a domain wins,
// so list order is priority order.
func (b *Builder) AddDeny(list, domain string) {
	norm := NormalizeDomain(domain)
	if norm == "" {
		b.skipped++
		return
	}
	key := reverseLabels(norm)
	if _, exists := b.deny[key]; !exists {
		b.deny[key] = b.listIndex(list)
	}
}

// AddAllow records domain (and its subdomains) as always allowed.
// Allow entries beat deny entries from any list.
func (b *Builder) AddAllow(list, domain string) {
	norm := NormalizeDomain(domain)
	if norm == "" {
		b.skipped++
		return
	}
	key := reverseLabels(norm)
	if _, exists := b.allow[key]; !exists {
		b.allow[key] = b.listIndex(list)
	}
}

// Skip counts an unsupported or unparseable rule. Skipped rules are a
// counted warning, never a failure.
func (b *Builder) Skip() { b.skipped++ }

// SkippedCount returns how many rules have been skipped so far.
func (b *Builder) SkippedCount() int { return b.skipped }

func (b *Builder) Build() *Matcher {
	return &Matcher{
		lists:   b.lists,
		deny:    compactSet(b.deny),
		allow:   compactSet(b.allow),
		skipped: b.skipped,
	}
}

// Matcher is an immutable compiled ruleset. All methods are safe for
// concurrent use because nothing mutates after Build.
type Matcher struct {
	lists   []string
	deny    domainSet
	allow   domainSet
	skipped int
}

// domainSet is a compact, immutable domain → list-index set: every
// reversed-label key concatenated in sorted order into one slab, with a
// packed index entry per key. Two maps at 2M entries cost ~164 MB
// (~82 B/entry, blowing the 150 MB RSS budget); this layout holds the
// same data in ~45 B/entry, and a binary-search lookup stays far under
// the 1 ms blocked-query budget.
type domainSet struct {
	slab []byte
	// idx entries are (offset << 24) | (keyLen << 16) | listIndex,
	// sorted by key bytes. keyLen fits 8 bits (DNS names cap at 254
	// with the trailing dot); listIndex fits 16 bits (a config carries
	// dozens of lists, not thousands); offset gets the remaining 40.
	idx []uint64
}

// compactSet flattens a builder map into the slab + index form.
func compactSet(m map[string]int32) domainSet {
	if len(m) == 0 {
		return domainSet{}
	}
	keys := make([]string, 0, len(m))
	total := 0
	for k := range m {
		keys = append(keys, k)
		total += len(k)
	}
	sort.Strings(keys)
	slab := make([]byte, 0, total)
	idx := make([]uint64, len(keys))
	for i, k := range keys {
		off := uint64(len(slab))
		slab = append(slab, k...)
		idx[i] = off<<24 | uint64(len(k))<<16 | uint64(uint16(m[k]))
	}
	return domainSet{slab: slab, idx: idx}
}

// lookup binary-searches for key, returning its list index.
func (s *domainSet) lookup(key string) (int32, bool) {
	lo, hi := 0, len(s.idx)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		e := s.idx[mid]
		off := e >> 24
		k := s.slab[off : off+(e>>16)&0xff]
		switch c := compareStringBytes(key, k); {
		case c == 0:
			return int32(uint16(e)), true
		case c < 0:
			hi = mid
		default:
			lo = mid + 1
		}
	}
	return 0, false
}

// compareStringBytes is bytes.Compare across a string and a []byte,
// avoiding the allocation a string(b) conversion would cost per probe.
func compareStringBytes(a string, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// EmptyMatcher blocks nothing; the Engine starts with it so queries flow
// before the first list refresh completes.
func EmptyMatcher() *Matcher { return NewBuilder().Build() }

// Rules returns the number of compiled deny entries.
func (m *Matcher) Rules() int { return len(m.deny.idx) }

// AllowRules returns the number of compiled allow entries.
func (m *Matcher) AllowRules() int { return len(m.allow.idx) }

// Skipped returns how many rules were dropped as invalid or unsupported.
func (m *Matcher) Skipped() int { return m.skipped }

// Match judges a query name. qname must already be normalized
// (lowercase, no trailing dot); use NormalizeDomain for untrusted input.
// It walks the name's parent domains from TLD to leaf via prefix slices of
// the reversed-label key, so a lookup does exactly one small allocation
// (the reversed key) regardless of match depth.
func (m *Matcher) Match(qname string) Result {
	if len(m.deny.idx) == 0 && len(m.allow.idx) == 0 {
		return Result{}
	}
	rev := reverseLabels(qname)
	// Check every label boundary: for "com.doubleclick.ads." the prefixes
	// are "com.", "com.doubleclick.", "com.doubleclick.ads.".
	var denied Result
	for i := 0; i < len(rev); i++ {
		if rev[i] != '.' {
			continue
		}
		prefix := rev[:i+1]
		if idx, ok := m.allow.lookup(prefix); ok {
			return Result{Blocked: false, List: m.lists[idx], Rule: unreverseLabels(prefix)}
		}
		if !denied.Blocked {
			if idx, ok := m.deny.lookup(prefix); ok {
				denied = Result{Blocked: true, List: m.lists[idx], Rule: unreverseLabels(prefix)}
			}
		}
	}
	return denied
}

// ParseAdblockLine classifies one line of an AdBlock-syntax list and feeds
// it into the builder. Supported: comments, "||domain^" (block),
// "@@||domain^" (exception), and bare domains. Anything else — element
// hiding, regexes, paths, option suffixes — is counted and skipped.
func (b *Builder) ParseAdblockLine(list, line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") {
		return // comment / header, not a rule
	}
	allow := false
	if strings.HasPrefix(line, "@@") {
		allow = true
		line = line[2:]
	}
	if strings.HasPrefix(line, "||") {
		line = line[2:]
		// Require the plain "domain^" form (optionally bare "domain").
		if i := strings.IndexByte(line, '^'); i >= 0 {
			if i != len(line)-1 {
				b.Skip() // options or path after the separator
				return
			}
			line = line[:i]
		}
	}
	// Whatever remains must be a bare domain; ParseAdblockLine is not a
	// general AdBlock engine and that is deliberate.
	if strings.ContainsAny(line, "/*^$|#?&=~") {
		b.Skip()
		return
	}
	if allow {
		b.AddAllow(list, line)
	} else {
		b.AddDeny(list, line)
	}
}
