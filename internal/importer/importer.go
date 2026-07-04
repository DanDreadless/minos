// Package importer migrates settings from other DNS sinkholes — Pi-hole's
// gravity.db/custom.list and AdGuard Home's YAML — into the Minos config.
// Importers only ever append: existing Minos settings are never removed or
// overwritten, and duplicates are dropped. Anything that cannot map onto a
// Minos concept is skipped with a reason in the Report, never guessed at.
package importer

import (
	"fmt"

	"minos/internal/config"
	"minos/internal/filter"
)

// Report says what an import added and what it had to leave behind.
type Report struct {
	Lists        int
	Allow        int
	Deny         int
	LocalRecords int
	Services     int
	Skipped      []string // human-readable reasons, one per skipped item
}

const maxSkipReasons = 50

func (r *Report) skip(format string, args ...any) {
	if len(r.Skipped) == maxSkipReasons {
		r.Skipped = append(r.Skipped, "... more items skipped")
	}
	if len(r.Skipped) > maxSkipReasons {
		return
	}
	r.Skipped = append(r.Skipped, fmt.Sprintf(format, args...))
}

// String renders the report for CLI output.
func (r *Report) String() string {
	s := fmt.Sprintf("imported: %d blocklists, %d allowed domains, %d blocked domains, %d local records, %d blocked services",
		r.Lists, r.Allow, r.Deny, r.LocalRecords, r.Services)
	for _, reason := range r.Skipped {
		s += "\n  skipped: " + reason
	}
	return s
}

// mergeDomain appends a normalized domain to list unless already present.
// Returns 1 when added, so callers can tally.
func mergeDomain(list *[]string, domain string) int {
	norm := filter.NormalizeDomain(domain)
	if norm == "" {
		return 0
	}
	for _, d := range *list {
		if d == norm {
			return 0
		}
	}
	*list = append(*list, norm)
	return 1
}

// mergeList appends a list subscription unless its URL is already
// subscribed; name collisions get a numeric suffix.
func mergeList(cfg *config.Config, name, url, format string, enabled bool) bool {
	names := make(map[string]bool, len(cfg.Lists.Sources))
	for _, src := range cfg.Lists.Sources {
		if src.URL == url {
			return false
		}
		names[src.Name] = true
	}
	unique := name
	for i := 2; names[unique]; i++ {
		unique = fmt.Sprintf("%s-%d", name, i)
	}
	cfg.Lists.Sources = append(cfg.Lists.Sources, config.ListSource{
		Name: unique, URL: url, Format: format, Enabled: enabled,
	})
	return true
}

// mergeLocalRecord appends a local record unless the name is taken.
func mergeLocalRecord(cfg *config.Config, rec config.LocalRecord, rep *Report) {
	for i := range cfg.DNS.LocalRecords {
		existing := &cfg.DNS.LocalRecords[i]
		if existing.Name != rec.Name {
			continue
		}
		// Same name with address records on both sides: merge addresses.
		if existing.CNAME == "" && rec.CNAME == "" {
			for _, a := range rec.A {
				if !contains(existing.A, a) {
					existing.A = append(existing.A, a)
				}
			}
			for _, a := range rec.AAAA {
				if !contains(existing.AAAA, a) {
					existing.AAAA = append(existing.AAAA, a)
				}
			}
			return
		}
		rep.skip("local record %q: a record with that name already exists", rec.Name)
		return
	}
	cfg.DNS.LocalRecords = append(cfg.DNS.LocalRecords, rec)
	rep.LocalRecords++
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
