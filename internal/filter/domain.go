package filter

import "strings"

// NormalizeDomain lowercases a domain and strips a single trailing dot.
// It returns "" if the input is not a plausible DNS name: labels must be
// 1-63 bytes of [a-z0-9_-], the whole name at most 253 bytes, ASCII only.
// (Underscores appear in real blocklists and in service records; IDNs are
// expected in punycode form.)
func NormalizeDomain(s string) string {
	s = strings.TrimSuffix(s, ".")
	if len(s) == 0 || len(s) > 253 {
		return ""
	}
	lower := true
	labelLen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			labelLen++
		case c >= 'A' && c <= 'Z':
			lower = false
			labelLen++
		case c == '.':
			if labelLen == 0 {
				return "" // empty label ("..", leading dot)
			}
			labelLen = 0
		default:
			return ""
		}
		if labelLen > 63 {
			return ""
		}
	}
	if labelLen == 0 {
		return ""
	}
	if !lower {
		s = strings.ToLower(s)
	}
	return s
}

// reverseLabels turns "ads.doubleclick.com" into "com.doubleclick.ads.".
// Keys in that form let a subdomain lookup walk parent domains by slicing
// prefixes at label boundaries, which allocates nothing.
func reverseLabels(domain string) string {
	var b strings.Builder
	b.Grow(len(domain) + 1)
	end := len(domain)
	for i := len(domain) - 1; i >= 0; i-- {
		if domain[i] == '.' {
			b.WriteString(domain[i+1 : end])
			b.WriteByte('.')
			end = i
		}
	}
	b.WriteString(domain[:end])
	b.WriteByte('.')
	return b.String()
}

// unreverseLabels is the inverse of reverseLabels, for reconstructing the
// human-readable rule from a matched key ("com.doubleclick." → "doubleclick.com").
func unreverseLabels(key string) string {
	trimmed := strings.TrimSuffix(key, ".")
	return strings.TrimSuffix(reverseLabels(trimmed), ".")
}
