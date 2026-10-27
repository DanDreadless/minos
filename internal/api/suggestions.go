package api

import (
	"encoding/json"
	"net/http"
	"slices"

	"minos/internal/breakage"
	"minos/internal/clients"
)

// SetBreakage wires the "something broke" detector in (optional).
func (s *Server) SetBreakage(d *breakage.Detector) { s.breakage = d }

type suggestionView struct {
	breakage.Suggestion
	// Device is the device's label or discovered name, when it has one;
	// DeviceKey its page (#/device/<key>): the MAC, else the address.
	Device    string `json:"device,omitempty"`
	DeviceKey string `json:"device_key"`
}

// handleSuggestions lists apps that look broken by blocking: a device
// retrying a blocked name in a burst. Empty when none (or no detector).
func (s *Server) handleSuggestions(w http.ResponseWriter, _ *http.Request) {
	out := []suggestionView{}
	if s.breakage != nil {
		devs := s.clients.Devices(s.store.Get())
		for _, sg := range s.breakage.Suggestions() {
			v := suggestionView{Suggestion: sg}
			v.Device, v.DeviceKey = deviceOf(devs, sg.Client)
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

// deviceOf names the device using ip, and gives its page key: the MAC
// when known, else the primary address (the address itself if unknown).
func deviceOf(devs []clients.Device, ip string) (name, key string) {
	for _, d := range devs {
		if d.IP == ip || slices.Contains(d.IPs, ip) {
			key = d.IP
			if d.MAC != "" {
				key = d.MAC
			}
			return firstNonEmpty(d.Name, d.Hostname, d.Model), key
		}
	}
	return "", ip
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
