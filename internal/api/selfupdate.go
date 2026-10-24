package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// One-click update. Minos never has root and never replaces its own
// binary: it writes the requested tag to a file in its state directory, and
// minos-update.path starts the root helper (deploy/minos-update.sh), which
// verifies and installs the release and writes its progress back to
// update-status.json. Only binary installs under systemd with the helper
// present can offer it, and only once a password or API token protects the
// interface — an open interface must not be able to install software.

const (
	defaultUpdateHelper = "/usr/local/libexec/minos-update"
	updateRequestFile   = "update-request"
	updateStatusFile    = "update-status.json"
	updateNotifiedFile  = "update-status.notified"
	// updateStale is how long an in-progress state is believed: a helper
	// killed mid-run leaves its last state behind.
	updateStale = 15 * time.Minute
)

// UpdateProgress is the helper's last report (update-status.json). Every
// field is plain text from the helper's own fixed strings.
type UpdateProgress struct {
	State   string `json:"state"` // requested|downloading|verifying|installing|restarting|done|failed|rolled_back
	Version string `json:"version,omitempty"`
	From    string `json:"from,omitempty"`
	Message string `json:"message,omitempty"`
	Time    int64  `json:"time,omitempty"`
}

// Finished reports whether the helper is no longer working on it.
func (p UpdateProgress) Finished() bool {
	switch p.State {
	case "done", "failed", "rolled_back":
		return true
	}
	return false
}

func (s *Server) stateDir() string { return filepath.Dir(s.store.Path()) }

// updatePlatform is "" on a platform the helper can serve, otherwise why
// not. A variable so tests on any OS can reach the rest of the path.
var updatePlatform = func(method string) string {
	switch {
	case runtime.GOOS != "linux" || method != methodBinary:
		return "Updating from here works for Linux installs made with the install script."
	case !underSystemd():
		return "Updating from here needs Minos to run as the systemd service."
	}
	return ""
}

// selfUpdateBlocked is "" when this instance can update itself, otherwise
// the plain reason it cannot.
func (s *Server) selfUpdateBlocked(method string) string {
	if why := updatePlatform(method); why != "" {
		return why
	}
	if _, err := os.Stat(s.updateHelper); err != nil {
		return "The updater is not installed yet. Run the upgrade command once and the button appears from then on."
	}
	if c := s.store.Get().API; c.Token == "" && c.PasswordHash == "" {
		return "Set a password first (Settings → Access & security): installing software must need a sign-in."
	}
	return ""
}

func (s *Server) readUpdateProgress() UpdateProgress {
	var p UpdateProgress
	b, err := os.ReadFile(filepath.Join(s.stateDir(), updateStatusFile))
	if err != nil || len(b) > 4096 || json.Unmarshal(b, &p) != nil {
		return UpdateProgress{}
	}
	if !p.Finished() && p.State != "" && time.Since(time.Unix(p.Time, 0)) > updateStale {
		p = UpdateProgress{State: "failed", Version: p.Version, From: p.From, Time: p.Time,
			Message: "The updater stopped without finishing. Nothing should have changed; check the Logs."}
	}
	return p
}

func (s *Server) handleUpdateProgress(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.readUpdateProgress())
}

// handleUpdateInstall asks the helper to install the latest release.
func (s *Server) handleUpdateInstall(w http.ResponseWriter, _ *http.Request) {
	method := resolveInstallMethod(s.store.Get().UpdateInstallMethod, s.installMethod, s.version, inDocker())
	if why := s.selfUpdateBlocked(method); why != "" {
		writeError(w, http.StatusConflict, why)
		return
	}
	var latest string
	var available bool
	if s.updates != nil {
		latest, available = s.updates.Latest()
	}
	if !available {
		writeError(w, http.StatusConflict, "no newer release is known — turn on the update check in Settings → System")
		return
	}
	if p := s.readUpdateProgress(); p.State != "" && !p.Finished() {
		writeError(w, http.StatusConflict, "an update is already in progress")
		return
	}
	tag := "v" + latest
	dir := s.stateDir()
	if err := writeFileAtomic(dir, updateRequestFile, []byte(tag+"\n")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	queued, _ := json.Marshal(UpdateProgress{State: "requested", Version: tag, From: s.version,
		Message: "Waiting for the updater to start.", Time: time.Now().Unix()})
	if err := writeFileAtomic(dir, updateStatusFile, queued); err != nil {
		slog.Warn("could not record the update request", "err", err)
	}
	slog.Info("update requested from the web interface", "version", tag)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "requested", "version": tag})
}

// writeFileAtomic writes name in dir through a temporary file and a rename,
// so a reader never sees half a file.
func writeFileAtomic(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// WatchSelfUpdate calls onFinish once for each update the helper finishes
// (installed, failed or rolled back), including one that finished while
// Minos was restarting into the new version. update-status.notified
// remembers the last one announced, so a restart never repeats it.
func (s *Server) WatchSelfUpdate(ctx context.Context, onFinish func(UpdateProgress)) {
	marker := filepath.Join(s.stateDir(), updateNotifiedFile)
	check := func() {
		p := s.readUpdateProgress()
		if !p.Finished() || p.Time == 0 {
			return
		}
		stamp := strconv.FormatInt(p.Time, 10) + " " + p.State
		if b, _ := os.ReadFile(marker); string(b) == stamp {
			return
		}
		if err := writeFileAtomic(s.stateDir(), updateNotifiedFile, []byte(stamp)); err != nil {
			return // Better silent than announcing on every check.
		}
		if time.Since(time.Unix(p.Time, 0)) < time.Hour {
			onFinish(p)
		}
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		check()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
