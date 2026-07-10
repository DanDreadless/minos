package api

import (
	"encoding/json"
	"net/http"

	"minos/internal/config"
	"minos/internal/services"
)

// servicesView pairs the static catalog with the sets blocked and allowed
// for everyone. (Per-group service blocks/pardons travel with the group
// objects instead.)
type servicesView struct {
	Catalog []services.Service `json:"catalog"`
	Blocked []string           `json:"blocked"`
	Allowed []string           `json:"allowed"`
}

func (s *Server) servicesView() servicesView {
	b := s.store.Get().Blocking
	blocked, allowed := b.Services, b.AllowedServices
	if blocked == nil {
		blocked = []string{}
	}
	if allowed == nil {
		allowed = []string{}
	}
	return servicesView{Catalog: services.All(), Blocked: blocked, Allowed: allowed}
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
