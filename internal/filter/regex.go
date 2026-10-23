package filter

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
)

// Regex rules: an entry written /pattern/ — in your allowed or blocked
// domains, in an AdBlock-syntax list, or imported from Pi-hole's regex
// lists — matches every name the pattern matches, anywhere in the name.
//
// Patterns compile with Go's regexp, which is RE2: matching is linear in
// the length of the name whatever the pattern, so no rule can hang the
// resolver (no backtracking, no catastrophic patterns). The price is that
// backreferences and lookaround are not supported; such rules are skipped
// and counted, like any other unsupported rule.
//
// Cost control: regexes are checked only after the exact-domain rules, the
// count per matcher is capped, and each rule carries a literal prefilter —
// text that must appear in any name it matches ("track" in
// \.track[0-9]+\.), found by parsing the pattern once at build time. A name
// that contains none of a rule's literals skips that rule without running
// it, so a miss — the common case — is a few substring checks per rule
// rather than a regex evaluation. (One combined alternation was tried
// first: Go's regexp ran it at ~96 µs per miss for 100 rules.)

const (
	// MaxRegexRules caps regex rules per matcher (allow and block each);
	// beyond it rules are skipped. Real lists carry a handful to a few
	// hundred.
	MaxRegexRules = 1000
	// maxRegexLen caps one pattern's length.
	maxRegexLen = 512
)

// IsRegexRule reports an entry in /pattern/ form.
func IsRegexRule(s string) bool {
	return len(s) > 2 && s[0] == '/' && s[len(s)-1] == '/'
}

// CompileRegexRule validates and compiles a /pattern/ entry, matched
// case-insensitively against the name without its trailing dot.
func CompileRegexRule(entry string) (*regexp.Regexp, error) {
	if !IsRegexRule(entry) {
		return nil, errors.New("a regex rule is written /pattern/")
	}
	pat := entry[1 : len(entry)-1]
	if len(pat) > maxRegexLen {
		return nil, fmt.Errorf("regex longer than %d characters", maxRegexLen)
	}
	// Pi-hole's ";querytype=…" and similar extensions have no meaning here.
	if strings.Contains(pat, ";") {
		return nil, errors.New("regex options (;querytype= and the like) are not supported")
	}
	re, err := regexp.Compile("(?i)" + pat)
	if err != nil {
		return nil, fmt.Errorf("not a supported regex: %w", err)
	}
	return re, nil
}

// regexRule is one compiled rule and the list it came from. needles, when
// set, is the prefilter: any name the rule matches contains at least one.
type regexRule struct {
	entry   string // as written, /pattern/
	list    int32
	re      *regexp.Regexp
	needles []string
}

// regexSet is one side (allow or block) of a matcher's regex rules.
type regexSet struct {
	rules []regexRule
}

func buildRegexSet(rules []regexRule) *regexSet {
	if len(rules) == 0 {
		return nil
	}
	for i := range rules {
		rules[i].needles = needlesFor(rules[i].entry[1 : len(rules[i].entry)-1])
	}
	return &regexSet{rules: rules}
}

// match returns the first rule matching name (lowercase, as Match takes
// it), or nil.
func (s *regexSet) match(name string) *regexRule {
	if s == nil {
		return nil
	}
	for i := range s.rules {
		r := &s.rules[i]
		if r.needles != nil && !containsAny(name, r.needles) {
			continue
		}
		if r.re.MatchString(name) {
			return r
		}
	}
	return nil
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// needlesFor returns literals at least one of which appears in every match
// of pattern (lowercased — rules match case-insensitively and names are
// lowercase), or nil when there is no such set (".*", "[a-z]+") and the
// rule must always run. Never wrong, only sometimes unhelpful: nil just
// means no prefilter.
func needlesFor(pattern string) []string {
	re, err := syntax.Parse(pattern, syntax.Perl|syntax.FoldCase)
	if err != nil {
		return nil
	}
	lits, ok := required(re.Simplify())
	if !ok || len(lits) == 0 {
		return nil
	}
	for _, l := range lits {
		if len(l) < 2 { // a single letter is in nearly every name
			return nil
		}
	}
	return lits
}

// required is the literal set every match of re must contain one of.
func required(re *syntax.Regexp) ([]string, bool) {
	switch re.Op {
	case syntax.OpLiteral:
		return []string{strings.ToLower(string(re.Rune))}, true
	case syntax.OpCapture, syntax.OpPlus:
		return required(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return required(re.Sub[0])
		}
	case syntax.OpConcat:
		// Any part whose literals are required will do; prefer the one
		// whose shortest literal is longest (the most selective).
		var best []string
		bestLen := 0
		for _, sub := range re.Sub {
			if lits, ok := required(sub); ok {
				if m := minLen(lits); m > bestLen {
					best, bestLen = lits, m
				}
			}
		}
		return best, best != nil
	case syntax.OpAlternate:
		var all []string
		for _, sub := range re.Sub {
			lits, ok := required(sub)
			if !ok {
				return nil, false
			}
			all = append(all, lits...)
		}
		return all, true
	}
	return nil, false
}

func minLen(lits []string) int {
	m := -1
	for _, l := range lits {
		if m < 0 || len(l) < m {
			m = len(l)
		}
	}
	return m
}

func (s *regexSet) len() int {
	if s == nil {
		return 0
	}
	return len(s.rules)
}
