package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// repo is the GitHub "owner/name" used in upgrade guidance URLs.
const repo = "DanDreadless/minos"

// Install methods, detected at runtime (no build-time stamp yet).
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
}

// handleUpdate reports the running/latest version and, crucially, the exact
// command to upgrade *this* instance — detected from how it was installed —
// so the "new version available" notice is actionable rather than just a link.
// Display-only: Minos never runs the command itself.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var latest string
	var available bool
	if s.updates != nil {
		latest, available = s.updates.Latest()
	}
	method := detectInstallMethod(s.version)
	tag := ""
	notesURL := "https://github.com/" + repo + "/releases"
	if latest != "" {
		tag = "v" + latest
		notesURL = "https://github.com/" + repo + "/releases/tag/" + tag
	}
	writeJSON(w, http.StatusOK, updateResponse{
		Current:       s.version,
		Latest:        latest,
		Available:     available,
		InstallMethod: method,
		Command:       upgradeCommand(method, tag),
		NotesURL:      notesURL,
	})
}

// detectInstallMethod guesses how this instance was installed. A dev version
// (with a pre-release suffix) implies a source build; a container is detected
// at runtime regardless of the binary.
func detectInstallMethod(version string) string {
	if inDocker() {
		return methodDocker
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
