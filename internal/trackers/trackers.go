// Package trackers says which company is behind a domain, what the domain
// is for (advertising, analytics…), and whether looking it up is tracking.
//
// Companies and categories are AdGuard's companiesdb (CC BY-SA 4.0, built
// from WhoTracks.me). That data describes domains as they appear inside web
// pages, so it calls google.com or yandex.ru "advertising"; blocking or
// counting those by DNS name would be wrong. Tracking is therefore decided
// by the tracking names: Hagezi Pro rules (GPLv3) under a company's
// advertising, analytics or telemetry domains — see gen.go. The table is
// generated into trackers.tsv and parsed on first use. A leaf package.
package trackers

import (
	_ "embed"
	"sort"
	"strings"
	"sync"
)

//go:embed trackers.tsv
var table string

// Attribution must accompany anything shown from this data.
const Attribution = "Company data: AdGuard companiesdb (built from WhoTracks.me), CC BY-SA 4.0; tracking names: Hagezi DNS blocklists, GPLv3"

// Tracker is what the table knows about a looked-up name.
type Tracker struct {
	Domain    string // The table's entry: the name itself or a parent
	Name      string // e.g. "Google Analytics"
	CompanyID string
	Category  string // The source's category, e.g. "site_analytics"
	// Tracking: the name is (under) one of the company's tracking names.
	Tracking bool
}

type db struct {
	byDomain  map[string]Tracker
	companies map[string]string   // ID → name
	tracking  map[string]string   // Tracking name → company ID
	byCompany map[string][]string // Company ID → its tracking names
}

var (
	once   sync.Once
	loaded *db
)

func get() *db {
	once.Do(func() {
		d := &db{byDomain: map[string]Tracker{}, companies: map[string]string{},
			tracking: map[string]string{}, byCompany: map[string][]string{}}
		for line := range strings.Lines(table) {
			f := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
			switch {
			case len(f) == 3 && f[0] == "C":
				d.companies[f[1]] = f[2]
			case len(f) == 5 && f[0] == "D":
				d.byDomain[f[1]] = Tracker{Domain: f[1], CompanyID: f[2], Category: f[3], Name: f[4]}
			case len(f) == 3 && f[0] == "T":
				d.tracking[f[1]] = f[2]
				d.byCompany[f[2]] = append(d.byCompany[f[2]], f[1])
			}
		}
		loaded = d
	})
	return loaded
}

// parents calls fn with name and each parent, nearest first, until fn
// returns true.
func parents(name string, fn func(string) bool) {
	for name != "" {
		if fn(name) {
			return
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return
		}
		name = name[i+1:]
	}
}

// Lookup finds the company behind qname (lowercase, no trailing dot) from
// the name or its nearest listed parent, and whether it is tracking.
func Lookup(qname string) (Tracker, bool) {
	d := get()
	var t Tracker
	found := false
	parents(qname, func(n string) bool {
		t, found = d.byDomain[n]
		return found
	})
	parents(qname, func(n string) bool {
		if id, ok := d.tracking[n]; ok {
			if !found { // A tracking name outside any table domain cannot happen, but be safe
				t, found = Tracker{Domain: n, CompanyID: id, Category: "advertising"}, true
			}
			t.Tracking = t.CompanyID == id
			return true
		}
		return false
	})
	return t, found
}

// CompanyName is a company's display name ("" when unknown).
func CompanyName(id string) string { return get().companies[id] }

// Exists reports whether id is a company "block this company" can block.
func Exists(id string) bool { return len(get().byCompany[id]) > 0 }

// Domains returns what blocking a company blocks: its tracking names
// (sorted).
func Domains(id string) []string {
	out := append([]string(nil), get().byCompany[id]...)
	sort.Strings(out)
	return out
}

// CategoryLabel names a category in plain words.
func CategoryLabel(category string) string {
	if l, ok := labels[category]; ok {
		return l
	}
	return "Other"
}

var labels = map[string]string{
	"advertising":          "Advertising",
	"pornvertising":        "Adult advertising",
	"site_analytics":       "Analytics",
	"mobile_analytics":     "App analytics",
	"telemetry":            "Telemetry",
	"social_media":         "Social media",
	"audio_video_player":   "Video player",
	"customer_interaction": "Customer chat",
	"essential":            "Site essentials",
	"cdn":                  "Content delivery",
	"hosting":              "Hosting",
	"comments":             "Comments",
	"email":                "Email",
	"consent":              "Consent banner",
	"extensions":           "Browser extension",
	"misc":                 "Other",
	"unknown":              "Other",
}
