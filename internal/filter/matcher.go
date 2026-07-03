// Package filter is the blocklist engine: it compiles list entries into an
// immutable Matcher and judges query names against it.
//
// Matchers are never mutated after Build; reloads construct a fresh Matcher
// off the hot path and swap it into the Engine atomically.
package filter

import "strings"

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
		deny:    b.deny,
		allow:   b.allow,
		skipped: b.skipped,
	}
}

// Matcher is an immutable compiled ruleset. All methods are safe for
// concurrent use because nothing mutates after Build.
type Matcher struct {
	lists   []string
	deny    map[string]int32
	allow   map[string]int32
	skipped int
}

// EmptyMatcher blocks nothing; the Engine starts with it so queries flow
// before the first list refresh completes.
func EmptyMatcher() *Matcher { return NewBuilder().Build() }

// Rules returns the number of compiled deny entries.
func (m *Matcher) Rules() int { return len(m.deny) }

// AllowRules returns the number of compiled allow entries.
func (m *Matcher) AllowRules() int { return len(m.allow) }

// Skipped returns how many rules were dropped as invalid or unsupported.
func (m *Matcher) Skipped() int { return m.skipped }

// Match judges a query name. qname must already be normalized
// (lowercase, no trailing dot); use NormalizeDomain for untrusted input.
// It walks the name's parent domains from TLD to leaf via prefix slices of
// the reversed-label key, so a lookup does exactly one small allocation
// (the reversed key) regardless of match depth.
func (m *Matcher) Match(qname string) Result {
	if len(m.deny) == 0 && len(m.allow) == 0 {
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
		if idx, ok := m.allow[prefix]; ok {
			return Result{Blocked: false, List: m.lists[idx], Rule: unreverseLabels(prefix)}
		}
		if !denied.Blocked {
			if idx, ok := m.deny[prefix]; ok {
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
