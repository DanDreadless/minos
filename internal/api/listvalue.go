package api

import (
	"context"
	"math/bits"
	"net/http"
	"sort"
	"strconv"
	"time"

	"minos/internal/querylog"
	"minos/internal/threats"
)

// listValue is one blocklist's entry in the value report.
type listValue struct {
	List string `json:"list"`
	// Security marks a threat-intelligence list (threats.ThreatFeed): for
	// one of those, blocking nothing is the good outcome, not a reason to
	// remove it.
	Security bool `json:"security"`
	// Rules, UniqueRules and CoveredBy compare the lists' exact-domain rules.
	Rules       int            `json:"rules"`
	UniqueRules int            `json:"unique_rules"`
	CoveredBy   []listCoverage `json:"covered_by"`
	// FreedBytes is roughly the memory removing the list would free.
	FreedBytes int64 `json:"freed_bytes"`
	// Blocks is the lookups in the window the list blocks today; OnlyBlocks
	// those no other compared list would have blocked, with the top names.
	Blocks       int                  `json:"blocks"`
	OnlyBlocks   int                  `json:"only_blocks"`
	OnlyExamples []querylog.TopDomain `json:"only_examples"`
}

type listCoverage struct {
	List  string `json:"list"`
	Rules int    `json:"rules"`
}

// handleListValue is the blocklist value report: for each enforcing
// blocklist, how much of it the others already cover, the memory it costs,
// and how many of the window's blocked lookups only it would have caught.
// Everything is judged against the rules in force now, so a list added
// last week is credited for lookups before it was added.
func (s *Server) handleListValue(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 90 {
			writeError(w, http.StatusBadRequest, "days must be 1-90")
			return
		}
		days = n
	}
	m := s.engine.Current()
	resp := struct {
		Days     int         `json:"days"`
		Compared []string    `json:"compared"`
		Lists    []listValue `json:"lists"`
		// BlockedLookups is every blocked lookup in the window; the lists'
		// figures cover those their rules account for.
		BlockedLookups int `json:"blocked_lookups"`
		// NamesTruncated: more names were blocked than the report checks
		// (the busiest querylog.MaxBlockedNames are).
		NamesTruncated bool `json:"names_truncated"`
	}{Days: days, Compared: m.Tracked(), Lists: []listValue{}}
	if resp.Compared == nil {
		resp.Compared = []string{}
	}
	overlap := m.Overlap()
	if len(overlap) == 0 {
		writeJSON(w, http.StatusOK, resp) // Fewer than two lists: nothing to compare
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), statsDeadline)
	defer cancel()
	names, err := s.qlog.BlockedNames(ctx, time.Now().AddDate(0, 0, -days))
	if err != nil {
		writeStatsErr(ctx, w, err, days*24)
		return
	}
	resp.NamesTruncated = len(names) >= querylog.MaxBlockedNames

	n := len(overlap)
	blocks := make([]int, n)
	only := make([]int, n)
	examples := make([][]querylog.TopDomain, n)
	for i, d := range names {
		resp.BlockedLookups += d.Count
		if i%1024 == 0 && ctx.Err() != nil {
			writeStatsErr(ctx, w, ctx.Err(), days*24)
			return
		}
		mask := m.Covering(d.QName)
		if mask == 0 || !m.Match(d.QName).Blocked {
			continue // Blocked by something else, or allowed now
		}
		for l := range n {
			if mask&(1<<l) != 0 {
				blocks[l] += d.Count
			}
		}
		if bits.OnesCount16(mask) == 1 {
			l := bits.TrailingZeros16(mask)
			only[l] += d.Count
			if len(examples[l]) < 5 { // Names arrive busiest first
				examples[l] = append(examples[l], d)
			}
		}
	}
	urls := map[string]string{}
	for _, src := range s.store.Get().Lists.Sources {
		urls[src.Name] = src.URL
	}
	for l, o := range overlap {
		v := listValue{
			Security: threats.ThreatFeed(o.List, urls[o.List]),
			List:     o.List, Rules: o.Rules, UniqueRules: o.Unique, FreedBytes: o.FreedBytes,
			Blocks: blocks[l], OnlyBlocks: only[l], OnlyExamples: examples[l], CoveredBy: []listCoverage{},
		}
		if v.OnlyExamples == nil {
			v.OnlyExamples = []querylog.TopDomain{}
		}
		for other, c := range o.CoveredBy {
			v.CoveredBy = append(v.CoveredBy, listCoverage{List: other, Rules: c})
		}
		sort.Slice(v.CoveredBy, func(i, j int) bool { return v.CoveredBy[i].Rules > v.CoveredBy[j].Rules })
		resp.Lists = append(resp.Lists, v)
	}
	writeJSON(w, http.StatusOK, resp)
}
