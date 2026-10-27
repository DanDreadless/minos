package trackers

import (
	"slices"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, tc := range []struct {
		name, company string
		tracking      bool
	}{
		{"googletagmanager.com", "google", false},    // "Site essentials"
		{"www.google.com", "google", false},          // Google's own site, though the source says advertising
		{"www.google-analytics.com", "google", true}, // Under a tracking name
		{"pagead2.googlesyndication.com", "google", true},
		{"pixel.facebook.com", "meta", true},
		{"www.facebook.com", "meta", false},
		{"samsungacr.com", "samsung", true},
		{"yandex.ru", "yandex", false},
		{"mc.yandex.ru", "yandex", true},
	} {
		got, ok := Lookup(tc.name)
		if !ok || got.CompanyID != tc.company || got.Tracking != tc.tracking || got.Name == "" {
			t.Errorf("Lookup(%q) = %+v, %v; want %s, tracking %v", tc.name, got, ok, tc.company, tc.tracking)
		}
	}
	for _, name := range []string{"example.org", "com", "", "notgoogletagmanager.com"} {
		if got, ok := Lookup(name); ok {
			t.Errorf("Lookup(%q) = %+v, want no match", name, got)
		}
	}
	if CompanyName("google") != "Google" || CompanyName("nope") != "" {
		t.Errorf("company names: %q %q", CompanyName("google"), CompanyName("nope"))
	}
}

// TestBlockingNeverTakesAServiceDown: "block this company" blocks tracking
// names only — never a company's own sites and infrastructure, which the
// source data also files under advertising.
func TestBlockingNeverTakesAServiceDown(t *testing.T) {
	for id, keep := range map[string][]string{
		"google":                   {"google.com", "googletagmanager.com", "1e100.net", "youtube.com", "gstatic.com", "googleapis.com"},
		"meta":                     {"facebook.com", "facebook.net", "instagram.com", "whatsapp.net"},
		"microsoft":                {"bing.com", "microsoft.com", "linkedin.com"},
		"yandex":                   {"yandex.ru", "yandex.com", "ya.ru", "yastatic.net"},
		"amazon_associates":        {"amazon.com", "elasticbeanstalk.com", "amazonaws.com", "cloudfront.net"},
		"apollo_global_management": {"yahoo.com", "yimg.com", "tumblr.com"},
	} {
		got := Domains(id)
		if len(got) == 0 && id != "apollo_global_management" {
			t.Errorf("%s: nothing to block", id)
		}
		for _, d := range keep {
			if slices.Contains(got, d) {
				t.Errorf("blocking %s would block %s", id, d)
			}
		}
		if !slices.IsSorted(got) {
			t.Errorf("%s: not sorted", id)
		}
	}
	if !slices.Contains(Domains("google"), "googlesyndication.com") || !slices.Contains(Domains("meta"), "pixel.facebook.com") {
		t.Error("core tracking names missing")
	}
	if !Exists("google") || Exists("no-such-company") {
		t.Error("Exists")
	}
}

func TestTableIsWellFormed(t *testing.T) {
	d := get()
	if len(d.byDomain) < 4000 || len(d.companies) < 2000 || len(d.tracking) < 5000 {
		t.Fatalf("table looks truncated: %d domains, %d companies, %d tracking names",
			len(d.byDomain), len(d.companies), len(d.tracking))
	}
	for name, tr := range d.byDomain {
		if _, ok := d.companies[tr.CompanyID]; !ok {
			t.Errorf("%s: unknown company %q", name, tr.CompanyID)
		}
	}
	for name, id := range d.tracking {
		if _, ok := d.companies[id]; !ok {
			t.Errorf("tracking name %s: unknown company %q", name, id)
		}
	}
}
