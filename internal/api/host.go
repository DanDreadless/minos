package api

import (
	"net/http"

	"minos/internal/hostinfo"
)

// hostResponse is the machine Minos runs on. Info is flattened to the top
// level and the reading nested, so a client reads identity and load
// without unwrapping twice.
//
// Supported is false on container installs, and then nothing else is
// present — see hostinfo.Collector.Supported for why a container reports
// no host health at all. Clients must branch on it rather than treating
// missing fields as zeroes.
type hostResponse struct {
	Supported bool `json:"supported"`
	hostinfo.Info
	Sample *hostinfo.Sample `json:"sample,omitempty"`
}

// handleHost reports host identity and the newest resource reading. The
// sampler runs on its own ticker, so this is one atomic load — it never
// blocks, and never sleeps to compute a CPU delta.
func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	if s.host == nil || !s.host.Supported() {
		// Deliberately not a 404: the endpoint exists and answered. The
		// feature does not apply here, which is a different thing from
		// the route being missing, and the UI hides the card on it.
		//
		// A bare struct rather than a zeroed hostResponse: the flattened
		// Info would otherwise serialise its zero values, and stating
		// "container": false inside a container is worse than saying
		// nothing at all.
		writeJSON(w, http.StatusOK, struct {
			Supported bool `json:"supported"`
		}{false})
		return
	}
	writeJSON(w, http.StatusOK, hostResponse{
		Supported: true,
		Info:      s.host.Info(),
		Sample:    s.host.Latest(),
	})
}
