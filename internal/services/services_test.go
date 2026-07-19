package services

import (
	"strings"
	"testing"
)

// Every catalog service can be pardoned: its allow bundle must cover at
// least everything its deny bundle names, so allowing a service always
// shadows blocking it.
func TestAllowDomainsSupersetOfDomains(t *testing.T) {
	for _, svc := range All() {
		allow := make(map[string]bool)
		for _, d := range AllowDomains(svc.Name) {
			allow[d] = true
		}
		for _, d := range svc.Domains {
			if !allow[d] {
				t.Errorf("%s: allow bundle is missing deny domain %q", svc.Name, d)
			}
		}
	}
}

// allowExtra keys must reference catalog services, and entries must be bare
// lowercase hostnames — precise hosts, never a shared-CDN apex.
func TestAllowExtrasWellFormed(t *testing.T) {
	sharedApexes := map[string]bool{
		"cloudfront.net": true, "akamaihd.net": true, "akamaized.net": true,
		"fastly.net": true, "edgesuite.net": true,
	}
	for name, extras := range allowExtra {
		if !Exists(name) {
			t.Errorf("allowExtra[%q]: no such catalog service", name)
		}
		for _, d := range extras {
			if d != strings.ToLower(d) || strings.ContainsAny(d, "/: ") {
				t.Errorf("allowExtra[%q]: %q is not a bare lowercase hostname", name, d)
			}
			if sharedApexes[d] {
				t.Errorf("allowExtra[%q]: %q is a shared-CDN apex — pardoning it would allow unrelated hosts", name, d)
			}
		}
	}
}

// The encrypted-dns bundle must stay curated and conservative: bare
// provider-owned hostnames only, never shared infrastructure that would
// break unrelated resolution when a kids group blocks it.
func TestEncryptedDNSBundleWellFormed(t *testing.T) {
	domains := Domains("encrypted-dns")
	if len(domains) == 0 {
		t.Fatal("encrypted-dns service missing from the catalog")
	}
	for _, d := range domains {
		if d != strings.ToLower(d) || strings.ContainsAny(d, "/: ") {
			t.Errorf("encrypted-dns: %q is not a bare lowercase hostname", d)
		}
	}
}

// The catalog itself must stay well-formed as it grows: unique lowercase
// slug names, non-empty labels, and bare lowercase hostnames throughout.
func TestCatalogWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, svc := range All() {
		if svc.Name == "" || svc.Name != strings.ToLower(svc.Name) ||
			strings.ContainsAny(svc.Name, "/:. ") {
			t.Errorf("service name %q is not a lowercase slug", svc.Name)
		}
		if seen[svc.Name] {
			t.Errorf("duplicate service name %q", svc.Name)
		}
		seen[svc.Name] = true
		if svc.Label == "" {
			t.Errorf("%s: empty label", svc.Name)
		}
		if len(svc.Domains) == 0 {
			t.Errorf("%s: no domains", svc.Name)
		}
		for _, d := range svc.Domains {
			if d != strings.ToLower(d) || strings.ContainsAny(d, "/: ") || !strings.Contains(d, ".") {
				t.Errorf("%s: %q is not a bare lowercase hostname", svc.Name, d)
			}
		}
	}
}

func TestAllowDomainsUnknownService(t *testing.T) {
	if got := AllowDomains("no-such-service"); got != nil {
		t.Errorf("unknown service allow bundle = %v, want nil", got)
	}
}
