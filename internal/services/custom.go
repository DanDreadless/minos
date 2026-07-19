package services

// Custom is a user-defined service: a named domain bundle that behaves
// like a catalog service — blockable and pardonable, globally and per
// group — but lives in the user's config, not the binary.
//
// Blocked/Allowed are the *global* toggles, the config-file analogue of
// membership in blocking.services / blocking.allowed_services. They travel
// inside this struct — and per-group selection lives in the groups' own
// custom_services keys — so a custom name never appears in the
// catalog-validated keys. That is what keeps downgrades boot-safe: an older
// binary's tolerant loader drops the unknown custom keys whole (the customs
// vanish — accepted under-blocking, mirror-image of the allowed_services
// over-blocking trade-off) instead of choking on an unknown service name in
// a key it validates against its compiled-in catalog.
type Custom struct {
	Name    string   `yaml:"name" json:"name"`
	Label   string   `yaml:"label,omitempty" json:"label"`
	Domains []string `yaml:"domains" json:"domains"`
	// AllowExtra are additional hosts pardoned (never blocked) when the
	// service is allowed — the same semantics as the catalog's allowExtra.
	AllowExtra []string `yaml:"allow_extra,omitempty" json:"allow_extra,omitempty"`
	Blocked    bool     `yaml:"blocked,omitempty" json:"blocked"`
	Allowed    bool     `yaml:"allowed,omitempty" json:"allowed"`
}

// FindCustom returns the definition named name, or nil. Linear scan —
// custom services number in the dozens at most, and callers are all off
// the hot path (rebuilds, validation, API reads).
func FindCustom(customs []Custom, name string) *Custom {
	for i := range customs {
		if customs[i].Name == name {
			return &customs[i]
		}
	}
	return nil
}

// CustomAllowDomains returns the domains pardoned when a custom service is
// allowed: its deny bundle plus the extras, like AllowDomains for the
// catalog.
func CustomAllowDomains(c *Custom) []string {
	if len(c.AllowExtra) == 0 {
		return c.Domains
	}
	out := make([]string, 0, len(c.Domains)+len(c.AllowExtra))
	out = append(out, c.Domains...)
	out = append(out, c.AllowExtra...)
	return out
}
