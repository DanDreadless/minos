// Package filter is the blocklist engine: it compiles list entries into an
// immutable Matcher and judges query names against it.
//
// Matchers are never mutated after Build; reloads construct a fresh Matcher
// off the hot path and swap it into the Engine atomically.
package filter

import (
	"sort"
	"strings"
	"sync"
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
	// deny and allow map a reversed-label domain to its index into lists
	// (low 16 bits) and, for deny, a bit per tracked list that has the same
	// domain (high 16 bits — see Track).
	deny  map[string]uint32
	allow map[string]uint32
	// tracked lists get a bit each (trackBit), for the overlap report;
	// curList/curBit cache the last lookup, since lists arrive one at a time.
	tracked  []string
	trackBit map[string]uint32
	curList  string
	curBit   uint32
	// denyRe and allowRe hold /pattern/ rules (regex.go).
	denyRe  []regexRule
	allowRe []regexRule
	skipped int
	// countOnly classifies and counts entries without storing them, for
	// vetting a freshly downloaded list (did it shrink? is it empty?) without
	// allocating a second full set of maps for it.
	countOnly bool
	counted   int
}

func NewBuilder() *Builder {
	return &Builder{
		listIdx: make(map[string]int32),
		deny:    make(map[string]uint32),
		allow:   make(map[string]uint32),
	}
}

// NewCountingBuilder returns a Builder that applies the same validation and
// AdBlock classification as a real one but keeps nothing: Counted reports
// how many entries a list would contribute. Build on it is meaningless.
func NewCountingBuilder() *Builder {
	return &Builder{countOnly: true}
}

// Counted reports the valid entries a counting builder has seen.
func (b *Builder) Counted() int { return b.counted }

// MaxTracked is how many lists the overlap report can compare.
const MaxTracked = 16

// Track names the lists (at most MaxTracked; extras are ignored) whose
// overlap the built Matcher can report: every blocked domain then records
// which of them contain it, at 2 bytes per rule. Call before adding rules.
func (b *Builder) Track(lists []string) {
	if len(lists) > MaxTracked {
		lists = lists[:MaxTracked]
	}
	b.tracked = append([]string(nil), lists...)
	b.trackBit = make(map[string]uint32, len(lists))
	for i, l := range b.tracked {
		b.trackBit[l] = 1 << i
	}
	b.curList, b.curBit = "", 0
}

// bitFor returns list's tracking bit (zero when it is not tracked).
func (b *Builder) bitFor(list string) uint32 {
	if b.trackBit == nil {
		return 0
	}
	if list != b.curList {
		b.curList, b.curBit = list, b.trackBit[list]
	}
	return b.curBit
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
//
// A /pattern/ entry is a regex rule (regex.go). It never normalizes as a
// domain, so it is recognised in that branch, keeping the per-entry path
// of a million-line list exactly as it was.
func (b *Builder) AddDeny(list, domain string) {
	norm := NormalizeDomain(domain)
	if norm == "" {
		if IsRegexRule(domain) {
			b.addRegex(&b.denyRe, list, domain)
			return
		}
		b.skipped++
		return
	}
	if b.countOnly {
		b.counted++
		return
	}
	key := reverseLabels(norm)
	bit := b.bitFor(list) << 16
	if v, exists := b.deny[key]; !exists {
		b.deny[key] = uint32(b.listIndex(list)) | bit
	} else if v&bit != bit {
		b.deny[key] = v | bit
	}
}

// AddAllow records domain (and its subdomains) as always allowed.
// Allow entries beat deny entries from any list.
func (b *Builder) AddAllow(list, domain string) {
	norm := NormalizeDomain(domain)
	if norm == "" {
		if IsRegexRule(domain) {
			b.addRegex(&b.allowRe, list, domain)
			return
		}
		b.skipped++
		return
	}
	if b.countOnly {
		b.counted++
		return
	}
	key := reverseLabels(norm)
	if _, exists := b.allow[key]; !exists {
		b.allow[key] = uint32(b.listIndex(list))
	}
}

// addRegex records a /pattern/ rule; one that does not compile, or beyond
// MaxRegexRules, is skipped like any other unsupported rule.
func (b *Builder) addRegex(set *[]regexRule, list, entry string) {
	re, err := CompileRegexRule(entry)
	if err != nil || len(*set) >= MaxRegexRules {
		b.skipped++
		return
	}
	if b.countOnly {
		b.counted++
		return
	}
	for _, r := range *set {
		if r.entry == entry {
			return // first list to claim a rule wins, as for domains
		}
	}
	*set = append(*set, regexRule{entry: entry, list: b.listIndex(list), re: re})
}

// Skip counts an unsupported or unparseable rule. Skipped rules are a
// counted warning, never a failure.
func (b *Builder) Skip() { b.skipped++ }

// SkippedCount returns how many rules have been skipped so far.
func (b *Builder) SkippedCount() int { return b.skipped }

func (b *Builder) Build() *Matcher {
	tracked := b.tracked
	if len(tracked) < 2 {
		tracked = nil // Nothing to compare: skip the per-rule bits
	}
	return &Matcher{
		lists:   b.lists,
		tracked: tracked,
		deny:    compactSet(b.deny, tracked != nil),
		allow:   compactSet(b.allow, false),
		denyRe:  buildRegexSet(b.denyRe),
		allowRe: buildRegexSet(b.allowRe),
		skipped: b.skipped,
	}
}

// Matcher is an immutable compiled ruleset. All methods are safe for
// concurrent use because nothing mutates after Build.
type Matcher struct {
	lists   []string
	deny    domainSet
	allow   domainSet
	denyRe  *regexSet // nil when there are none
	allowRe *regexSet
	skipped int

	// tracked are the lists the overlap report compares (Builder.Track);
	// deny.masks holds their bits. overlap is computed on first use.
	tracked     []string
	overlapOnce sync.Once
	overlap     []ListOverlap
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
	// masks, when the builder tracked lists, holds each entry's tracked-list
	// bits, parallel to idx.
	masks []uint16
}

// compactSet flattens a builder map into the slab + index form, keeping the
// tracked-list bits when withMasks is set.
func compactSet(m map[string]uint32, withMasks bool) domainSet {
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
	var masks []uint16
	if withMasks {
		masks = make([]uint16, len(keys))
	}
	for i, k := range keys {
		off := uint64(len(slab))
		slab = append(slab, k...)
		v := m[k]
		idx[i] = off<<24 | uint64(len(k))<<16 | uint64(uint16(v))
		if masks != nil {
			masks[i] = uint16(v >> 16)
		}
	}
	return domainSet{slab: slab, idx: idx, masks: masks}
}

// key returns entry i's reversed-label key.
func (s *domainSet) key(i int) []byte {
	e := s.idx[i]
	off := e >> 24
	return s.slab[off : off+(e>>16)&0xff]
}

// find binary-searches for key, returning its position in idx. The same
// search as lookup, kept separate so the hot path's code is untouched.
func (s *domainSet) find(key string) (int, bool) {
	lo, hi := 0, len(s.idx)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch c := compareStringBytes(key, s.key(mid)); {
		case c == 0:
			return mid, true
		case c < 0:
			hi = mid
		default:
			lo = mid + 1
		}
	}
	return 0, false
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
func (m *Matcher) Rules() int { return len(m.deny.idx) + m.denyRe.len() }

// AllowRules returns the number of compiled allow entries.
func (m *Matcher) AllowRules() int { return len(m.allow.idx) + m.allowRe.len() }

// Skipped returns how many rules were dropped as invalid or unsupported.
func (m *Matcher) Skipped() int { return m.skipped }

// Empty reports whether the matcher holds no rules at all — the hot path's
// cheap gate for skipping optional engines (the audit engine when no audit
// lists are configured). Zero allocations.
func (m *Matcher) Empty() bool {
	return len(m.deny.idx) == 0 && len(m.allow.idx) == 0 && m.denyRe == nil && m.allowRe == nil
}

// Match judges a query name. qname must already be normalized
// (lowercase, no trailing dot); use NormalizeDomain for untrusted input.
// It walks the name's parent domains from TLD to leaf via prefix slices of
// the reversed-label key, so a lookup does exactly one small allocation
// (the reversed key) regardless of match depth.
//
// Regex rules come after the exact ones: an allow regex beats any block
// (allowing always wins, as for domains); a block regex applies only when
// no exact rule decided. Each side is one combined pass over the name.
//
// Measured on the Pi (arm64, 1M entries, no regex rules): hit 4.03 µs and
// miss 1.14 µs, the same before and after regex support. On the x86 dev box
// the hit benchmark swings ~25% between builds from code alignment alone —
// compare on the Pi, not there.
func (m *Matcher) Match(qname string) Result {
	if len(m.deny.idx) == 0 && len(m.allow.idx) == 0 {
		if m.denyRe == nil && m.allowRe == nil {
			return Result{}
		}
		return m.matchRegex(qname, Result{})
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
	if m.denyRe == nil && m.allowRe == nil {
		return denied
	}
	return m.matchRegex(qname, denied)
}

// matchRegex applies the regex rules after the exact walk found no allow:
// an allow regex beats an exact block; a block regex applies only when
// nothing exact decided.
func (m *Matcher) matchRegex(qname string, denied Result) Result {
	if r := m.allowRe.match(qname); r != nil {
		return Result{Blocked: false, List: m.lists[r.list], Rule: r.entry}
	}
	if denied.Blocked {
		return denied
	}
	if r := m.denyRe.match(qname); r != nil {
		return Result{Blocked: true, List: m.lists[r.list], Rule: r.entry}
	}
	return denied
}

// ParseAdblockLine classifies one line of an AdBlock-syntax list and feeds
// it into the builder. Supported: comments, "||domain^" (block),
// "@@||domain^" (exception), /regex/ rules (and @@/regex/), and bare
// domains. Anything else — element hiding, paths, option suffixes — is
// counted and skipped.
// allowList marks a subscribed allowlist: membership makes every supported
// rule an allow, "@@" or not — that is how AdGuard whitelist filters and
// Pi-hole v6 "antigravity" lists are written (block-shaped rules whose
// list decides their meaning).
func (b *Builder) ParseAdblockLine(list, line string, allowList bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[") {
		return // comment / header, not a rule
	}
	allow := allowList
	if strings.HasPrefix(line, "@@") {
		allow = true
		line = line[2:]
	}
	if IsRegexRule(line) {
		if allow {
			b.AddAllow(list, line)
		} else {
			b.AddDeny(list, line)
		}
		return
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
