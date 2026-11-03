package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/services"
)

// Rules: every allow and block the user has made, in one place, each scoped
// to everyone, one group or one device — firewall style. The most specific
// rule wins when a lookup is judged (device, then group, then everyone; see
// dnsproxy.judge). This API is a view over the existing config keys, not a
// new store: everyone → blocking.allowlist/denylist and the services keys,
// a group → groups[].…, a device → clients[].… (MAC-keyed when the device
// has a MAC, so its rules follow it across address changes). The older
// per-list endpoints (/allowlist, /denylist, /services, /groups) keep
// working on the same keys.

// Scopes.
const (
	scopeEveryone = "everyone"
	scopeGroup    = "group"
	scopeDevice   = "device"
)

// Rule is one allow or block at one scope.
type Rule struct {
	Action string `json:"action"` // allow | block
	Kind   string `json:"kind"`   // domain | service
	// Value is the domain (or /regex/ rule), or the service name.
	Value string `json:"value"`
	Scope string `json:"scope"` // everyone | group | device
	// Target is the group name, or the device key (MAC, else IP).
	Target string `json:"target,omitempty"`
}

// ruleRequest adds or removes one rule. IP is a last-known address hint for
// a device Minos cannot currently see (see clientUpdate.IP).
type ruleRequest struct {
	Rule
	IP string `json:"ip,omitempty"`
}

// ruleSlots are the six lists one group or device keeps its rules in.
type ruleSlots struct {
	allow, deny               *[]string
	services, allowedServices *[]string
	custom, allowedCustom     *[]string
}

func (rs ruleSlots) slot(action string, catalog, custom bool) *[]string {
	switch {
	case !catalog && !custom: // a domain
		if action == "allow" {
			return rs.allow
		}
		return rs.deny
	case catalog:
		if action == "allow" {
			return rs.allowedServices
		}
		return rs.services
	default:
		if action == "allow" {
			return rs.allowedCustom
		}
		return rs.custom
	}
}

func groupSlots(g *config.Group) ruleSlots {
	return ruleSlots{&g.Allowlist, &g.Denylist, &g.Services, &g.AllowedServices, &g.CustomServices, &g.AllowedCustomServices}
}

func clientSlots(cl *config.Client) ruleSlots {
	return ruleSlots{&cl.Allowlist, &cl.Denylist, &cl.Services, &cl.AllowedServices, &cl.CustomServices, &cl.AllowedCustomServices}
}

// rulesOf lists every rule in c: everyone's first, then each group's, then
// each device's, each in the order they were added.
func rulesOf(c *config.Config) []Rule {
	out := []Rule{}
	add := func(action, kind, scope, target string, values []string) {
		for _, v := range values {
			out = append(out, Rule{Action: action, Kind: kind, Value: v, Scope: scope, Target: target})
		}
	}
	addSlots := func(scope, target string, rs ruleSlots) {
		add("allow", "domain", scope, target, *rs.allow)
		add("block", "domain", scope, target, *rs.deny)
		add("allow", "service", scope, target, *rs.allowedServices)
		add("allow", "service", scope, target, *rs.allowedCustom)
		add("block", "service", scope, target, *rs.services)
		add("block", "service", scope, target, *rs.custom)
	}
	add("allow", "domain", scopeEveryone, "", c.Blocking.Allowlist)
	add("block", "domain", scopeEveryone, "", c.Blocking.Denylist)
	add("allow", "service", scopeEveryone, "", c.Blocking.AllowedServices)
	add("block", "service", scopeEveryone, "", c.Blocking.Services)
	for _, cs := range c.Blocking.CustomServices {
		if cs.Allowed {
			add("allow", "service", scopeEveryone, "", []string{cs.Name})
		}
		if cs.Blocked {
			add("block", "service", scopeEveryone, "", []string{cs.Name})
		}
	}
	for i := range c.Groups {
		addSlots(scopeGroup, c.Groups[i].Name, groupSlots(&c.Groups[i]))
	}
	for i := range c.Clients {
		cl := &c.Clients[i]
		key := cl.IP
		if cl.MAC != "" {
			key = clients.NormalizeMAC(cl.MAC)
		}
		addSlots(scopeDevice, key, clientSlots(cl))
	}
	return out
}

func (s *Server) handleGetRules(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rulesOf(s.store.Get()))
}

// normalizeRule checks a rule's shape and canonicalises its value, reporting
// whether a service value names a catalog service or a custom one.
func normalizeRule(c *config.Config, r *Rule) (catalog, custom bool, err error) {
	if r.Action != "allow" && r.Action != "block" {
		return false, false, errors.New(`action must be "allow" or "block"`)
	}
	switch r.Scope {
	case scopeEveryone:
		r.Target = ""
	case scopeGroup, scopeDevice:
		if r.Target == "" {
			return false, false, fmt.Errorf("a %s rule needs a target", r.Scope)
		}
	default:
		return false, false, errors.New(`scope must be "everyone", "group" or "device"`)
	}
	switch r.Kind {
	case "domain":
		norm, err := domainOrRegex(r.Value)
		if err != nil {
			return false, false, err
		}
		r.Value = norm
		return false, false, nil
	case "service":
		if services.Exists(r.Value) {
			return true, false, nil
		}
		if services.FindCustom(c.Blocking.CustomServices, r.Value) != nil {
			return false, true, nil
		}
		return false, false, fmt.Errorf("no service named %q", r.Value)
	}
	return false, false, errors.New(`kind must be "domain" or "service"`)
}

func opposite(action string) string {
	if action == "allow" {
		return "block"
	}
	return "allow"
}

// handleAddRule adds one rule. A scope holds one decision per domain or
// service, so adding an allow removes a block of the same value at the same
// scope (and the other way round) — the newest decision is the one in force.
func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, `body must be JSON: {"action", "kind", "value", "scope", "target"}`)
		return
	}
	rule := req.Rule
	changed := false
	err := s.update(r, func(c *config.Config) error {
		catalog, custom, err := normalizeRule(c, &rule)
		if err != nil {
			return err
		}
		changed, err = s.setRule(c, rule, catalog, custom, true, req.IP)
		return err
	})
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	if rule.Action == "allow" && rule.Kind == "domain" && s.breakage != nil {
		s.breakage.Resolve(rule.Value)
	}
	status := http.StatusCreated
	if !changed {
		status = http.StatusOK
	}
	writeJSON(w, status, rulesOf(s.store.Get()))
}

// handleDeleteRule removes one rule (the JSON body names it, because a
// /regex/ rule cannot travel in a path segment).
func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, `body must be JSON: {"action", "kind", "value", "scope", "target"}`)
		return
	}
	rule := req.Rule
	err := s.update(r, func(c *config.Config) error {
		catalog, custom, err := normalizeRule(c, &rule)
		if err != nil {
			return err
		}
		changed, err := s.setRule(c, rule, catalog, custom, false, "")
		if err == nil && !changed {
			return errNotFound
		}
		return err
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "no such rule")
		return
	}
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rulesOf(s.store.Get()))
}

var errNoTarget = errors.New("no such target")

func statusFor(err error) int {
	if errors.Is(err, errNoTarget) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// setRule adds (on) or removes (!on) rule in c, reporting whether anything
// changed.
func (s *Server) setRule(c *config.Config, rule Rule, catalog, custom, on bool, ipHint string) (bool, error) {
	if rule.Scope == scopeEveryone {
		return setEveryone(c, rule, catalog, custom, on)
	}
	var rs ruleSlots
	if rule.Scope == scopeGroup {
		i := slices.IndexFunc(c.Groups, func(g config.Group) bool { return g.Name == rule.Target })
		if i < 0 {
			return false, fmt.Errorf("%w: no group named %q", errNoTarget, rule.Target)
		}
		rs = groupSlots(&c.Groups[i])
	} else {
		cl, err := s.deviceEntry(c, rule.Target, ipHint, on)
		if err != nil {
			return false, err
		}
		if cl == nil {
			return false, nil // removing from a device with no rules
		}
		rs = clientSlots(cl)
	}
	slot := rs.slot(rule.Action, catalog, custom)
	if !on {
		n := len(*slot)
		*slot = removeString(*slot, rule.Value)
		return len(*slot) != n, nil
	}
	other := rs.slot(opposite(rule.Action), catalog, custom)
	*other = removeString(*other, rule.Value)
	if slices.Contains(*slot, rule.Value) {
		return false, nil
	}
	*slot = append(*slot, rule.Value)
	return true, nil
}

// setEveryone edits the global keys. Custom services keep their global state
// as flags on the definition.
func setEveryone(c *config.Config, rule Rule, catalog, custom, on bool) (bool, error) {
	if custom {
		cs := findCustomMut(c, rule.Value)
		flag, other := &cs.Allowed, &cs.Blocked
		if rule.Action == "block" {
			flag, other = &cs.Blocked, &cs.Allowed
		}
		if on {
			*other = false
		}
		changed := *flag != on
		*flag = on
		return changed, nil
	}
	allow, deny := &c.Blocking.Allowlist, &c.Blocking.Denylist
	if catalog {
		allow, deny = &c.Blocking.AllowedServices, &c.Blocking.Services
	}
	slot, other := allow, deny
	if rule.Action == "block" {
		slot, other = deny, allow
	}
	if !on {
		n := len(*slot)
		*slot = removeString(*slot, rule.Value)
		return len(*slot) != n, nil
	}
	*other = removeString(*other, rule.Value)
	if rule.Action == "allow" && !catalog {
		// A permanent allow supersedes a temporary one.
		c.Blocking.TemporaryAllows = withoutTemporary(c.Blocking.TemporaryAllows, rule.Value)
	}
	if slices.Contains(*slot, rule.Value) {
		return false, nil
	}
	*slot = append(*slot, rule.Value)
	return true, nil
}

func findCustomMut(c *config.Config, name string) *services.Custom {
	for i := range c.Blocking.CustomServices {
		if c.Blocking.CustomServices[i].Name == name {
			return &c.Blocking.CustomServices[i]
		}
	}
	return nil // unreachable: normalizeRule checked the name
}

// deviceEntry returns the config entry for the device addressed by key,
// creating it when create is set — keyed by MAC when key is one, exactly as
// a label or group assignment is (handleUpdateClient), so the rules follow
// the device across address changes. Without create, a device with no entry
// returns nil.
func (s *Server) deviceEntry(c *config.Config, key, ipHint string, create bool) (*config.Client, error) {
	isMAC := keyIsMAC(key)
	hint := clientUpdate{}
	if ipHint != "" {
		hint.IP = &ipHint
	}
	ips := s.deviceIPs(key, hint)
	cl, found := takeDeviceClient(c, key, isMAC, ips)
	if !found && !create {
		return nil, nil
	}
	if isMAC {
		cl.MAC = clients.NormalizeMAC(key)
		if cur := s.clients.CurrentIP(key); cur != "" {
			cl.IP = cur
		} else if cl.IP == "" {
			cl.IP = ipHint
		}
		if cl.IP == "" {
			return nil, fmt.Errorf("%w: Minos has not seen the device %s on the network, so it has no address to record", errNoTarget, key)
		}
	} else {
		cl.IP = key
	}
	c.Clients = append(c.Clients, cl)
	return &c.Clients[len(c.Clients)-1], nil
}
