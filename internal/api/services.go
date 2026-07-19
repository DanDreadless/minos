package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"minos/internal/config"
	"minos/internal/services"
)

// servicesView pairs the static catalog with the sets blocked and allowed
// for everyone, plus the user-defined custom services (which carry their
// own global blocked/allowed flags). (Per-group service blocks/pardons
// travel with the group objects instead.)
type servicesView struct {
	Catalog []services.Service `json:"catalog"`
	Blocked []string           `json:"blocked"`
	Allowed []string           `json:"allowed"`
	Custom  []services.Custom  `json:"custom"`
}

func (s *Server) servicesView() servicesView {
	b := s.store.Get().Blocking
	blocked, allowed, custom := b.Services, b.AllowedServices, b.CustomServices
	if blocked == nil {
		blocked = []string{}
	}
	if allowed == nil {
		allowed = []string{}
	}
	if custom == nil {
		custom = []services.Custom{}
	}
	return servicesView{Catalog: services.All(), Blocked: blocked, Allowed: allowed, Custom: custom}
}

func (s *Server) handleGetServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.servicesView())
}

// handleUpdateServices replaces the globally blocked and/or allowed service
// sets. Partial update: an omitted field is left unchanged, so an external
// caller written before "allowed" existed can't silently clear it. Unknown
// names are rejected by config validation, so nothing partial ever applies.
func (s *Server) handleUpdateServices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Blocked *[]string `json:"blocked"`
		Allowed *[]string `json:"allowed"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil ||
		(body.Blocked == nil && body.Allowed == nil) {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of: {\"blocked\": [...], \"allowed\": [...]}")
		return
	}
	err := s.store.Update(func(c *config.Config) error {
		if body.Blocked != nil {
			c.Blocking.Services = *body.Blocked
		}
		if body.Allowed != nil {
			c.Blocking.AllowedServices = *body.Allowed
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.servicesView())
}

// --- Custom services ---
//
// User-defined service bundles get their own sub-resource; the global
// blocked/allowed toggles live on each definition, never as names inside
// the catalog-validated blocked/allowed sets above (the downgrade-safety
// contract — see config.BlockingConfig.CustomServices).

type customServiceBody struct {
	Name       *string   `json:"name"`
	Label      *string   `json:"label"`
	Domains    *[]string `json:"domains"`
	AllowExtra *[]string `json:"allow_extra"`
	Blocked    *bool     `json:"blocked"`
	Allowed    *bool     `json:"allowed"`
}

// handleAddCustomService creates a definition. A missing name is slugified
// from the label; collisions with the built-in catalog are rejected (pick a
// different name), collisions with other customs de-collide with a suffix.
func (s *Server) handleAddCustomService(w http.ResponseWriter, r *http.Request) {
	var body customServiceBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil ||
		body.Domains == nil || len(*body.Domains) == 0 {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"label\", \"domains\": [...], \"name\"?, \"allow_extra\"?}")
		return
	}
	cs := services.Custom{Domains: *body.Domains}
	if body.Label != nil {
		cs.Label = *body.Label
	}
	if body.AllowExtra != nil {
		cs.AllowExtra = *body.AllowExtra
	}
	if body.Blocked != nil {
		cs.Blocked = *body.Blocked
	}
	if body.Allowed != nil {
		cs.Allowed = *body.Allowed
	}
	err := s.store.Update(func(c *config.Config) error {
		name := ""
		if body.Name != nil {
			name = *body.Name
		}
		if name == "" {
			name = slugify(cs.Label)
			if name == "" {
				return fmt.Errorf("name is required when the label yields no usable slug")
			}
			// De-collide against other customs only; a catalog collision
			// falls through to validation's plain error, telling the user
			// to pick a name rather than silently renaming their service.
			base := name
			for n := 2; services.FindCustom(c.Blocking.CustomServices, name) != nil; n++ {
				name = fmt.Sprintf("%s-%d", base, n)
			}
		} else if services.FindCustom(c.Blocking.CustomServices, name) != nil {
			return fmt.Errorf("a custom service named %q already exists", name)
		}
		cs.Name = name
		c.Blocking.CustomServices = append(c.Blocking.CustomServices, cs)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.servicesView())
}

// handleUpdateCustomService partially updates a definition. The name is the
// stable key — renaming is not supported (it is baked into group references
// and the docket's "service:<name>" attributions).
func (s *Server) handleUpdateCustomService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body customServiceBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of: label, domains, allow_extra, blocked, allowed")
		return
	}
	err := s.store.Update(func(c *config.Config) error {
		cs := services.FindCustom(c.Blocking.CustomServices, name)
		if cs == nil {
			return errNotFound
		}
		if body.Label != nil {
			cs.Label = *body.Label
		}
		if body.Domains != nil {
			cs.Domains = *body.Domains
		}
		if body.AllowExtra != nil {
			cs.AllowExtra = *body.AllowExtra
		}
		if body.Blocked != nil {
			cs.Blocked = *body.Blocked
		}
		if body.Allowed != nil {
			cs.Allowed = *body.Allowed
		}
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no custom service named %q", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.servicesView())
}

// handleDeleteCustomService removes a definition and scrubs its name from
// every group's custom selections in the same update, so validation never
// sees a dangling reference.
func (s *Server) handleDeleteCustomService(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	err := s.store.Update(func(c *config.Config) error {
		kept := c.Blocking.CustomServices[:0]
		found := false
		for _, cs := range c.Blocking.CustomServices {
			if cs.Name == name {
				found = true
				continue
			}
			kept = append(kept, cs)
		}
		if !found {
			return errNotFound
		}
		c.Blocking.CustomServices = kept
		for i := range c.Groups {
			c.Groups[i].CustomServices = removeString(c.Groups[i].CustomServices, name)
			c.Groups[i].AllowedCustomServices = removeString(c.Groups[i].AllowedCustomServices, name)
		}
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no custom service named %q", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.servicesView())
}

// slugify derives a config-safe name from a display label: lowercase,
// spaces and runs of other characters collapse to single hyphens, trimmed
// to the 40-char slug limit.
func slugify(label string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if pending && b.Len() > 0 {
				b.WriteByte('-')
			}
			pending = false
			b.WriteRune(r)
		default:
			pending = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.TrimSuffix(b.String()[:min(b.Len(), 40)], "-")
}

func removeString(list []string, s string) []string {
	kept := list[:0]
	for _, v := range list {
		if v != s {
			kept = append(kept, v)
		}
	}
	return kept
}
