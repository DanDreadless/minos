package api

import (
	"strings"
	"testing"
)

func TestDetectInstallMethodDevIsSource(t *testing.T) {
	// A pre-release version is a source build (unless in a container, which
	// this test box is not).
	if !inDocker() {
		if got := detectInstallMethod("0.1.0-dev"); got != methodSource {
			t.Errorf("dev version = %q, want source", got)
		}
		if got := detectInstallMethod("0.8.0"); got != methodBinary {
			t.Errorf("release version = %q, want binary", got)
		}
	}
}

func TestUpgradeCommand(t *testing.T) {
	if got := upgradeCommand(methodDocker, "v0.8.0"); !strings.Contains(got, "docker compose pull") {
		t.Errorf("docker command = %q", got)
	}
	src := upgradeCommand(methodSource, "v0.8.0")
	if !strings.Contains(src, "checkout v0.8.0") || !strings.Contains(src, "make build") {
		t.Errorf("source command = %q", src)
	}
	// Unknown tag falls back to main for a source build.
	if got := upgradeCommand(methodSource, ""); !strings.Contains(got, "checkout main") {
		t.Errorf("source command (no tag) = %q", got)
	}
	bin := upgradeCommand(methodBinary, "v0.8.0")
	if !strings.Contains(bin, "install.sh") {
		t.Errorf("binary command = %q", bin)
	}
}

func TestUpdateEndpoint(t *testing.T) {
	s, _ := newTestServer(t, "")
	rec := doJSON(t, s.Router(), "GET", "/api/update", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{`"install_method"`, `"command"`, `"notes_url"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("response missing %s: %s", want, rec.Body.String())
		}
	}
}
