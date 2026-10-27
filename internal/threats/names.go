package threats

import (
	"math"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// split returns a name's registrable domain ("example.co.uk") and the part
// before it ("a.b"), or ok=false for names without one (single labels,
// bare suffixes, local names).
func split(name string) (registrable, sub string, ok bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	reg, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return "", "", false
	}
	if _, icann := publicsuffix.PublicSuffix(name); !icann {
		// Private suffixes (cloudfront.net, github.io…) make every
		// customer a "registrable domain". Their random-looking customer
		// labels are infrastructure, not malware, so judge them by the
		// suffix owner's own domain instead.
		reg = icannRegistrable(name)
		if reg == "" {
			return "", "", false
		}
	}
	return reg, strings.TrimSuffix(strings.TrimSuffix(name, reg), "."), true
}

// icannRegistrable is the registrable domain under the longest ICANN suffix
// only, ignoring private suffixes ("cloudfront.net" for
// "d3x.cloudfront.net").
func icannRegistrable(name string) string {
	labels := strings.Split(name, ".")
	best := -1
	for i := len(labels) - 1; i >= 1; i-- {
		cand := strings.Join(labels[i:], ".")
		if suf, icann := publicsuffix.PublicSuffix(cand); icann && suf == cand {
			best = i
		}
	}
	if best < 1 {
		return ""
	}
	return strings.Join(labels[best-1:], ".")
}

// label is the registrable domain without its suffix ("example" of
// "example.co.uk").
func label(registrable string) string {
	suf, _ := publicsuffix.PublicSuffix(registrable)
	return strings.TrimSuffix(strings.TrimSuffix(registrable, suf), ".")
}

// entropy is the Shannon entropy of s in bits per character.
func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	h := 0.0
	n := float64(len(s))
	for _, c := range counts {
		if c > 0 {
			p := float64(c) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

// randomLabel reports whether a registrable label looks machine-generated:
// long, high-entropy, and either nearly vowel-free, full of long consonant
// runs, or letters and digits mixed through it. Real brands are short,
// pronounceable, or words; generated ones are not.
func randomLabel(l string) bool {
	if len(l) < 10 || strings.Contains(l, "-") {
		return false
	}
	letters, vowels, digits, run, longest := 0, 0, 0, 0, 0
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
			run = 0
		case strings.IndexByte("aeiouy", c) >= 0:
			letters++
			vowels++
			run = 0
		case c >= 'a' && c <= 'z':
			letters++
			run++
			longest = max(longest, run)
		default:
			return false // Not a plain ASCII label (IDN, underscores): leave it
		}
	}
	if letters == 0 || entropy(l) < 2.8 {
		return false
	}
	vowelRatio := float64(vowels) / float64(letters)
	mixed := digits >= 3 && letters >= 3
	if !(vowelRatio < 0.22 || longest >= 6 || mixed) {
		return false
	}
	// Compound names ("smartthings", "msftconnecttest") are built from
	// ordinary letter pairs; generated ones ("kqlxtqptsmys") mostly are not.
	return commonPairs(l) < 0.7
}

// commonPairs is the fraction of a label's adjacent letter pairs that are
// ordinary in English words and brand names; digits break pairs.
func commonPairs(l string) float64 {
	total, common := 0, 0
	for i := 0; i+1 < len(l); i++ {
		a, b := l[i], l[i+1]
		if a < 'a' || a > 'z' || b < 'a' || b > 'z' {
			continue
		}
		total++
		if strings.Contains(ordinaryPairs, string([]byte{a, b})) {
			common++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(common) / float64(total)
}

// ordinaryPairs lists letter pairs common in English words and product
// names, space-separated (a pair can only match where it sits).
const ordinaryPairs = " th he in er an re on at en nd ti es or te of ed is it al ar st to nt ng se ha" +
	" as ou io le ve co me de hi ri ro ic ne ea ra ce li ch ll be ma si om ur ca el ta la ns ge ly ei" +
	" os no pe do su pa ec ac ot di ol tr sh ad fo il wa lo ow pr ni em ho ss sa we so ct po am ee rs" +
	" ut ue ie id ab op ap ia mo im ag rt bl us ul ke ai cl ir ay gh ts ki ty ge un up ep et ex ff ft" +
	" fi fe fa fl fr gr gl go gu ga gi gn ck ks ka ku kn mp mb mm mi mu my nc ny nn nk ny oo ok oa oi" +
	" ob oc od og om oy ph pl pi pp pu py qu rc rd rg rk rl rm rn rp rr ru rv ry sc sk sl sm sn sp sq" +
	" sw sy tc tt tu tw tl tm ub uc ud ug um un ur va vi vo wh wi wo wr xp xt ya ye yo ys yt zo za ze" +
	" bo bu br by ci cr cu cy da du dr dy ef eg ek em eo eq ev ew ey ez fu hu hy ib if ik iv iz ja jo" +
	" ju lb ld lf lk lm lp lt lu lv mn nf nl ns nv nu oe ov ox ps pt rb rf rh sb sd sf sg sr tb td tf" +
	" tg tn tp tv tz ua uf ui uk uo uq ux vs xa xb xe xi yb yc yd yl ym yn yp yr yw zz "

// tunnelLike reports whether the part of a name before its registrable
// domain looks like data carried in DNS. Only long labels (16+ characters)
// count as data, and there must be at least 50 characters of it: hashed
// hostnames carry one hash-sized label among short fixed ones (measured on
// a real network: GlobalProtect's 20–28 + 11 + 2, a CDN's 29 + short),
// while tunnelling tools fill several labels, often to the 63 maximum.
func tunnelLike(sub string) bool {
	var data strings.Builder
	for _, l := range strings.Split(sub, ".") {
		if len(l) >= 16 {
			data.WriteString(l)
		}
	}
	if data.Len() < 50 {
		return false
	}
	return entropy(data.String()) >= 2.9
}

// hasLongLabel reports whether any label of name is at least n bytes.
func hasLongLabel(name string, n int) bool {
	run := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			run = 0
			continue
		}
		if run++; run >= n {
			return true
		}
	}
	return false
}
