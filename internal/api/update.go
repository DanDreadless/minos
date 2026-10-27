package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// repo is the GitHub "owner/name" used in upgrade guidance URLs.
const repo = "DanDreadless/minos"

// Install methods. Release builds stamp one via -ldflags (see
// cmd/minos main.installMethod); detection refines it at runtime.
const (
	methodDocker = "docker"
	methodBinary = "binary" // quick-install script / release binary
	methodSource = "source" // built from source
)

type updateResponse struct {
	Current       string `json:"current"`
	Latest        string `json:"latest,omitempty"`
	Available     bool   `json:"available"`
	InstallMethod string `json:"install_method"`
	Command       string `json:"command"`
	NotesURL      string `json:"notes_url"`
	// SelfUpdate is true when the "Update now" button can work here;
	// otherwise SelfUpdateBlocked says why not.
	SelfUpdate        bool   `json:"self_update"`
	SelfUpdateBlocked string `json:"self_update_blocked,omitempty"`
}

// handleUpdate reports the running/latest version and, crucially, the exact
// command to upgrade *this* instance — detected from how it was installed —
// so the "new version available" notice is actionable rather than just a link.
// The command is display-only; the one-click update goes through the root
// helper instead (selfupdate.go).
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var latest string
	var available bool
	if s.updates != nil {
		latest, available = s.updates.Latest()
	}
	method := resolveInstallMethod(s.store.Get().UpdateInstallMethod,
		s.installMethod, s.version, inDocker())
	tag := ""
	notesURL := "https://github.com/" + repo + "/releases"
	if latest != "" {
		tag = "v" + latest
		notesURL = "https://github.com/" + repo + "/releases/tag/" + tag
	}
	blocked := s.selfUpdateBlocked(method)
	writeJSON(w, http.StatusOK, updateResponse{
		Current:           s.version,
		Latest:            latest,
		Available:         available,
		InstallMethod:     method,
		Command:           upgradeCommand(method, tag),
		NotesURL:          notesURL,
		SelfUpdate:        blocked == "",
		SelfUpdateBlocked: blocked,
	})
}

// resolveInstallMethod decides how this instance was installed, most
// trustworthy source first: the operator's config override
// (update_install_method — for deployments detection can't see, e.g. a
// distro package), runtime container markers (they beat the build stamp so
// a release binary run inside a container still gets the Docker command —
// wrong guidance is worse than none), the build-time -ldflags stamp, and
// finally a version heuristic: a pre-release suffix implies a source build.
func resolveInstallMethod(override, stamp, version string, docker bool) string {
	if override != "" {
		return override // validated by config: binary|docker|source
	}
	if docker {
		return methodDocker
	}
	switch stamp {
	case methodBinary, methodDocker, methodSource:
		return stamp
	}
	if strings.ContainsAny(version, "-+") { // e.g. 0.1.0-dev
		return methodSource
	}
	return methodBinary
}

func inDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(b), "docker") || strings.Contains(string(b), "containerd") {
			return true
		}
	}
	return false
}

func underSystemd() bool {
	if os.Getenv("INVOCATION_ID") != "" {
		return true
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// upgradeCommand returns the shell command(s) to upgrade, for the detected
// method. tag is the target release tag (e.g. "v0.8.0"), or "" if unknown.
func upgradeCommand(method, tag string) string {
	switch method {
	case methodDocker:
		return "docker compose pull && docker compose up -d"
	case methodSource:
		target := tag
		if target == "" {
			target = "main"
		}
		return fmt.Sprintf("git fetch --tags && git checkout %s && make build   # then restart Minos", target)
	default: // binary / quick-install
		cmd := fmt.Sprintf("curl -fsSL https://raw.githubusercontent.com/%s/main/deploy/install.sh | sudo sh", repo)
		if underSystemd() {
			return cmd + " && sudo systemctl restart minos"
		}
		return cmd + "   # then restart Minos"
	}
}

// updateChecker can check for a release on demand (updates.Checker).
type updateChecker interface {
	CheckNow(ctx context.Context) (latest string, available bool, err error)
}

// handleUpdateCheck is Settings' "Check now": it asks for the latest
// release at once, rather than waiting for the daily check, and answers as
// GET /api/update does. A failure (no network, GitHub's rate limit, a
// development build) is a 502 with the reason in words.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	c, ok := s.updates.(updateChecker)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "checking for updates is not available here")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, _, err := c.CheckNow(ctx); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.handleUpdate(w, r)
}
