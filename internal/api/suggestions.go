package api

import (
	"encoding/json"
	"net/http"
	"slices"

	"minos/internal/breakage"
)

// SetBreakage wires the "something broke" detector in (optional).
func (s *Server) SetBreakage(d *breakage.Detector) { s.breakage = d }

type suggestionView struct {
	breakage.Suggestion
	// Device is the device's label or discovered name, when it has one.
	Device string `json:"device,omitempty"`
}

// handleSuggestions lists apps that look broken by blocking: a device
// retrying a blocked name in a burst. Empty when none (or no detector).
func (s *Server) handleSuggestions(w http.ResponseWriter, _ *http.Request) {
	out := []suggestionView{}
	if s.breakage != nil {
		devs := s.clients.Devices(s.store.Get())
		for _, sg := range s.breakage.Suggestions() {
			v := suggestionView{Suggestion: sg}
			for _, d := range devs {
				if d.IP == sg.Client || slices.Contains(d.IPs, sg.Client) {
					v.Device = firstNonEmpty(d.Name, d.Hostname, d.Model)
					break
				}
			}
			out = append(out, v)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDismissSuggestion hides a suggestion for a day.
func (s *Server) handleDismissSuggestion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Client string `json:"client"`
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Client == "" || body.Domain == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with client and domain")
		return
	}
	if s.breakage != nil {
		s.breakage.Dismiss(body.Client, body.Domain)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
