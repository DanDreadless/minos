package importer

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"minos/internal/config"
	"minos/internal/services"
)

// adguardYAML is the slice of AdGuard Home's config we can map onto Minos.
// Rewrites and blocked services moved from dns: to filtering: in v0.107,
// so both locations are read. Unknown fields are ignored by design.
type adguardYAML struct {
	Filters          []adguardFilter `yaml:"filters"`
	WhitelistFilters []adguardFilter `yaml:"whitelist_filters"`
	UserRules        []string        `yaml:"user_rules"`
	DNS              struct {
		Rewrites        []adguardRewrite       `yaml:"rewrites"`
		BlockedServices adguardBlockedServices `yaml:"blocked_services"`
	} `yaml:"dns"`
	Filtering struct {
		Rewrites        []adguardRewrite       `yaml:"rewrites"`
		BlockedServices adguardBlockedServices `yaml:"blocked_services"`
	} `yaml:"filtering"`
}

type adguardFilter struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
	Name    string `yaml:"name"`
}

type adguardRewrite struct {
	Domain string `yaml:"domain"`
	Answer string `yaml:"answer"`
}

type adguardBlockedServices struct {
	IDs []string `yaml:"ids"`
}

// adguardServiceAliases maps AdGuard service ids to our catalog names where
// they differ. Identical names (tiktok, youtube, discord, ...) need no entry.
var adguardServiceAliases = map[string]string{
	"epic_games":   "epicgames",
	"amazon_prime": "primevideo",
	"disney_plus":  "disneyplus",
	"twitter_x":    "twitter",
}

// AdGuard imports from an AdGuard Home YAML config (AdGuardHome.yaml):
// filter subscriptions, user rules, DNS rewrites, and blocked services.
func AdGuard(path string, cfg *config.Config) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read AdGuard config: %w", err)
	}
	var ag adguardYAML
	if err := yaml.Unmarshal(data, &ag); err != nil {
		return nil, fmt.Errorf("parse AdGuard config: %w", err)
	}

	rep := &Report{}
	for i, f := range ag.Filters {
		if f.URL == "" {
			continue
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = fmt.Sprintf("adguard-%d", i+1)
		}
		// AdGuard filter lists are adblock syntax; our parser downgrades
		// unsupported rules to counted skips.
		if mergeList(cfg, name, f.URL, "adblock", "block", f.Enabled) {
			rep.Lists++
		}
	}
	for i, f := range ag.WhitelistFilters {
		if f.URL == "" {
			continue
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = fmt.Sprintf("adguard-allow-%d", i+1)
		}
		// Whitelist filters map to action:allow subscriptions — membership
		// makes every rule an allow, matching AdGuard's semantics.
		if mergeList(cfg, name, f.URL, "adblock", "allow", f.Enabled) {
			rep.Lists++
		}
	}

	for _, rule := range ag.UserRules {
		importUserRule(rule, cfg, rep)
	}

	rewrites := append(append([]adguardRewrite{}, ag.DNS.Rewrites...), ag.Filtering.Rewrites...)
	for _, rw := range rewrites {
		importRewrite(rw, cfg, rep)
	}

	ids := append(append([]string{}, ag.DNS.BlockedServices.IDs...), ag.Filtering.BlockedServices.IDs...)
	for _, id := range ids {
		name := id
		if alias, ok := adguardServiceAliases[id]; ok {
			name = alias
		}
		if !services.Exists(name) {
			rep.skip("blocked service %q: not in the Minos catalog", id)
			continue
		}
		if !contains(cfg.Blocking.Services, name) {
			cfg.Blocking.Services = append(cfg.Blocking.Services, name)
			rep.Services++
		}
	}
	return rep, nil
}

// importUserRule maps one AdGuard user rule onto the allow/deny lists.
// Same dialect subset as the list parser: ||domain^, @@||domain^, bare
// domains. Everything fancier is skipped with the rule as the reason.
func importUserRule(rule string, cfg *config.Config, rep *Report) {
	line := strings.TrimSpace(rule)
	if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
		return
	}
	allow := false
	if strings.HasPrefix(line, "@@") {
		allow = true
		line = line[2:]
	}
	line = strings.TrimPrefix(line, "||")
	if i := strings.IndexByte(line, '^'); i >= 0 {
		if i != len(line)-1 {
			rep.skip("user rule %q: options after separator are not supported", rule)
			return
		}
		line = line[:i]
	}
	if strings.ContainsAny(line, "/*^$|#?&=~") {
		rep.skip("user rule %q: only plain domain rules are supported", rule)
		return
	}
	if allow {
		rep.Allow += mergeDomain(&cfg.Blocking.Allowlist, line)
	} else {
		rep.Deny += mergeDomain(&cfg.Blocking.Denylist, line)
	}
}

// importRewrite maps one AdGuard DNS rewrite onto a local record: an IP
// answer becomes an A/AAAA record, anything else a CNAME.
func importRewrite(rw adguardRewrite, cfg *config.Config, rep *Report) {
	if rw.Domain == "" || rw.Answer == "" {
		return
	}
	rec := config.LocalRecord{Name: rw.Domain}
	switch ip := net.ParseIP(rw.Answer); {
	case ip == nil:
		rec.CNAME = rw.Answer
	case ip.To4() != nil:
		rec.A = []string{rw.Answer}
	default:
		rec.AAAA = []string{rw.Answer}
	}
	mergeLocalRecord(cfg, rec, rep)
}
