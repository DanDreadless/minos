package filter

import (
	"errors"
	"regexp"
	"strings"
)

// Wildcard rules: an entry with a "*" in it — "*.example.com",
// "ads*.example.com" — in your own allowed or blocked domains. "*" stands
// for any run of characters, dots included, and the rule must match the
// whole name, so "*.example.com" is every subdomain of example.com at any
// depth but not example.com itself (the same as a wildcard local DNS
// record). A plain "example.com" already covers the domain and all its
// subdomains.
//
// A wildcard compiles to an anchored regex and joins the regex rules
// (regex.go): same cap, same prefilter — the literal text around the
// stars — and the same place in the order, after the exact-domain rules.
// Subscribed lists never reach here: their parsers skip anything that is
// not a plain domain before it is added.

// IsWildcardRule reports an entry that uses "*" and is otherwise made of
// domain characters.
func IsWildcardRule(s string) bool {
	if !strings.Contains(s, "*") || len(s) > 253 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '*':
		default:
			return false
		}
	}
	return true
}

// NormalizeWildcard lowercases a wildcard rule, drops a trailing dot and
// collapses runs of "*", returning "" when it is not a usable wildcard: it
// needs some text besides the stars, and no empty labels ("a..b").
func NormalizeWildcard(s string) string {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if !IsWildcardRule(s) {
		return ""
	}
	for strings.Contains(s, "**") {
		s = strings.ReplaceAll(s, "**", "*")
	}
	if strings.Trim(s, "*.") == "" || strings.HasPrefix(s, ".") || strings.Contains(s, "..") {
		return ""
	}
	return s
}

// CompileWildcardRule validates a wildcard rule and compiles it to the
// anchored, case-insensitive regex it stands for.
func CompileWildcardRule(entry string) (*regexp.Regexp, error) {
	norm := NormalizeWildcard(entry)
	if norm == "" {
		return nil, errors.New(`a wildcard rule is a domain with "*" in it, e.g. *.example.com`)
	}
	parts := strings.Split(norm, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.Compile("(?i)^" + strings.Join(parts, ".*") + "$")
}
