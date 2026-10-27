package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"minos/internal/config"
)

func TestResolveInstallMethodPrecedence(t *testing.T) {
	tests := []struct {
		name                     string
		override, stamp, version string
		docker                   bool
		want                     string
	}{
		// The operator override beats everything, containers included.
		{"override beats docker", methodBinary, methodDocker, "0.8.0", true, methodBinary},
		{"override beats stamp", methodSource, methodBinary, "0.8.0", false, methodSource},
		// Runtime container markers beat the stamp: a release binary run
		// inside a container must get the Docker command.
		{"docker beats stamp", "", methodBinary, "0.8.0", true, methodDocker},
		// The stamp beats the version heuristic: a source build of an
		// exact tag has no pre-release suffix but is still a source build.
		{"stamp beats heuristic", "", methodSource, "0.8.0", false, methodSource},
		{"binary stamp", "", methodBinary, "0.8.0", false, methodBinary},
		// An unknown stamp value falls through to the heuristic.
		{"garbage stamp ignored", "", "snap", "0.1.0-dev", false, methodSource},
		// Unstamped: dev suffix implies source, a bare version binary.
		{"heuristic dev is source", "", "", "0.1.0-dev", false, methodSource},
		{"heuristic release is binary", "", "", "0.8.0", false, methodBinary},
	}
	for _, tt := range tests {
		if got := resolveInstallMethod(tt.override, tt.stamp, tt.version, tt.docker); got != tt.want {
			t.Errorf("%s: resolveInstallMethod(%q, %q, %q, %v) = %q, want %q",
				tt.name, tt.override, tt.stamp, tt.version, tt.docker, got, tt.want)
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

func TestUpdateEndpointHonoursConfigOverride(t *testing.T) {
	s, store := newTestServer(t, "")
	if err := store.Update(func(c *config.Config) error {
		c.UpdateInstallMethod = "docker"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s.Router(), "GET", "/api/update", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"install_method":"docker"`) ||
		!strings.Contains(body, "docker compose pull") {
		t.Errorf("override not honoured: %s", body)
	}
}

type checkingSource struct {
	latest string
	err    error
	calls  int
}

func (c *checkingSource) Latest() (string, bool) { return c.latest, c.latest != "" }
func (c *checkingSource) CheckNow(context.Context) (string, bool, error) {
	c.calls++
	return c.latest, c.latest != "", c.err
}

func TestUpdateCheckNow(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()
	if rec := doJSON(t, r, "POST", "/api/update/check", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no checker: %d", rec.Code)
	}
	src := &checkingSource{latest: "9.9.9"}
	s.updates = src
	rec := doJSON(t, r, "POST", "/api/update/check", "", nil)
	if rec.Code != http.StatusOK || src.calls != 1 || !strings.Contains(rec.Body.String(), `"latest":"9.9.9"`) {
		t.Errorf("check: %d %s (calls %d)", rec.Code, rec.Body, src.calls)
	}
	src.err = errors.New("could not reach GitHub: no route to host")
	rec = doJSON(t, r, "POST", "/api/update/check", "", nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "could not reach GitHub") {
		t.Errorf("failed check: %d %s", rec.Code, rec.Body)
	}
}
