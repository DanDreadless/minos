package api

import (
	"net/http"

	"minos/internal/hass"
)

// homeAssistant is the MQTT bridge as the API sees it.
type homeAssistant interface {
	Status() hass.Status
	// Changed asks for state to be published now.
	Changed()
}

// SetHomeAssistant connects the Home Assistant bridge: its status for
// Settings, and a nudge after a pause so Home Assistant shows it at once.
func (s *Server) SetHomeAssistant(h homeAssistant) { s.hass = h }

func (s *Server) homeAssistantChanged() {
	if s.hass != nil {
		s.hass.Changed()
	}
}

func (s *Server) handleHomeAssistantStatus(w http.ResponseWriter, _ *http.Request) {
	if s.hass == nil {
		writeJSON(w, http.StatusOK, hass.Status{})
		return
	}
	writeJSON(w, http.StatusOK, s.hass.Status())
}
