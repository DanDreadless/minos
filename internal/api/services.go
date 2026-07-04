package api

import (
	"encoding/json"
	"net/http"

	"minos/internal/config"
	"minos/internal/services"
)

// servicesView pairs the static catalog with the set blocked for everyone.
// (Per-group service blocks travel with the group objects instead.)
type servicesView struct {
	Catalog []services.Service `json:"catalog"`
	Blocked []string           `json:"blocked"`
}

func (s *Server) servicesView() servicesView {
	blocked := s.store.Get().Blocking.Services
	if blocked == nil {
		blocked = []string{}
	}
	return servicesView{Catalog: services.All(), Blocked: blocked}
}

func (s *Server) handleGetServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.servicesView())
}

// handleUpdateServices replaces the globally blocked service set. Unknown
// names are rejected by config validation, so nothing partial ever applies.
func (s *Server) handleUpdateServices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Blocked *[]string `json:"blocked"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil || body.Blocked == nil {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"blocked\": [\"tiktok\", ...]}")
		return
	}
	err := s.store.Update(func(c *config.Config) error {
		c.Blocking.Services = *body.Blocked
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.servicesView())
}
