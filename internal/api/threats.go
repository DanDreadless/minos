package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/threats"
)

// SetThreats wires the threat detector in (optional).
func (s *Server) SetThreats(d *threats.Detector) { s.threats = d }

type threatView struct {
	threats.Finding
	// Device is the device's label or discovered name, when it has one.
	Device string `json:"device,omitempty"`
}

// handleThreats lists devices behaving like malware. Empty when none (or no
// detector).
func (s *Server) handleThreats(w http.ResponseWriter, _ *http.Request) {
	out := []threatView{}
	if s.threats != nil {
		devs := s.clients.Devices(s.store.Get())
		for _, f := range s.threats.Findings() {
			v := threatView{Finding: f}
			for _, d := range devs {
				if d.IP == f.Client || slices.Contains(d.IPs, f.Client) {
					v.Device = firstNonEmpty(d.Name, d.Hostname, d.Model)
					break
				}
			}
			out = append(out, v)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDismissThreat hides a finding ("this is fine") for a while.
func (s *Server) handleDismissThreat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string `json:"kind"`
		Client string `json:"client"`
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Kind == "" || body.Client == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with kind, client and domain")
		return
	}
	if s.threats != nil {
		s.threats.Dismiss(threats.Kind(body.Kind), body.Client, body.Domain)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

// handleIgnoreThreatDomain adds a domain to threats.ignore — never flagged
// again, on any device — and clears its current findings.
func (s *Server) handleIgnoreThreatDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"domain\": \"...\"}")
		return
	}
	norm := filter.NormalizeDomain(strings.TrimSpace(body.Domain))
	if norm == "" {
		writeError(w, http.StatusBadRequest, "not a valid domain")
		return
	}
	err := s.update(r, func(c *config.Config) error {
		if !slices.Contains(c.Threats.Ignore, norm) {
			c.Threats.Ignore = append(c.Threats.Ignore, norm)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.threats != nil {
		s.threats.Forget(norm)
	}
	writeJSON(w, http.StatusOK, map[string]string{"domain": norm, "status": "ignored"})
}
