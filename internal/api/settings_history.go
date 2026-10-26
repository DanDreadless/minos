package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"minos/internal/config"
)

type actorKey struct{}

// withActor records how the request was authorized, for the settings
// history ("Password sign-in", "API token", "Open access").
func withActor(r *http.Request, how string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, how))
}

// actor describes who is making a request's change: how it was authorized
// and from where. The minos command says so with a header (a label, not a
// credential).
func actor(r *http.Request) string {
	how, _ := r.Context().Value(actorKey{}).(string)
	if how == "" {
		how = "Open access"
	}
	if r.Header.Get("X-Minos-Client") == "cli" {
		how = "minos command"
	}
	return how + " from " + clientAddr(r)
}

// update changes the config on behalf of the request, recording the change
// in the settings history.
func (s *Server) update(r *http.Request, mutate func(*config.Config) error) error {
	return s.store.UpdateAs(actor(r), "", mutate)
}

func (s *Server) handleSettingsHistory(w http.ResponseWriter, _ *http.Request) {
	h := s.store.History()
	if h == nil {
		h = []config.HistoryEntry{}
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleSettingsHistoryDiff(w http.ResponseWriter, r *http.Request) {
	diff, err := s.store.VersionDiff(chi.URLParam(r, "id"))
	if errors.Is(err, config.ErrNoVersion) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// handleSettingsRestore makes a recorded version current again (sign-in,
// secrets and restart-only settings excepted — see config.RestoreVersion).
func (s *Server) handleSettingsRestore(w http.ResponseWriter, r *http.Request) {
	err := s.store.RestoreVersion(chi.URLParam(r, "id"), actor(r))
	if errors.Is(err, config.ErrNoVersion) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}
