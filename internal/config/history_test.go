package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openHistoryStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "minos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHistoryRecordsPeoplesChanges(t *testing.T) {
	s := openHistoryStore(t)
	// Housekeeping is not recorded.
	if err := s.Update(func(c *Config) error { c.Blocking.Denylist = []string{"a.example"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if h := s.History(); len(h) != 0 {
		t.Fatalf("housekeeping recorded: %+v", h)
	}
	if err := s.UpdateAs("Password sign-in from 192.168.1.20", "", func(c *Config) error {
		c.Blocking.Allowlist = append(c.Blocking.Allowlist, "ok.example")
		c.DNS.RateLimit = 500
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := s.History()
	if len(h) != 2 || h[1].Actor != "Before history began" {
		t.Fatalf("history %+v", h)
	}
	if !slices.Equal(h[0].Keys, []string{"blocking.allowlist", "dns.rate_limit"}) || h[0].Actor != "Password sign-in from 192.168.1.20" {
		t.Fatalf("latest %+v", h[0])
	}
	// A change that changes nothing is not a version.
	if err := s.UpdateAs("API token", "", func(c *Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(s.History()) != 2 {
		t.Error("a no-op change was recorded")
	}
	// A password change is recorded (by key name only).
	if err := s.UpdateAs("API token", "", func(c *Config) error { c.API.PasswordHash = "$argon2id$new"; return nil }); err != nil {
		t.Fatal(err)
	}
	if h := s.History(); !slices.Equal(h[0].Keys, []string{"api.password_hash"}) {
		t.Fatalf("password change keys %v", h[0].Keys)
	}
}

func TestHistoryDiffHidesSecrets(t *testing.T) {
	s := openHistoryStore(t)
	if err := s.UpdateAs("me", "", func(c *Config) error {
		c.API.Token = "supersecrettoken"
		c.Blocking.Allowlist = []string{"ok.example"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := s.History()
	diff, err := s.VersionDiff(h[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, l := range diff {
		text.WriteString(l.Op + l.Text + "\n")
	}
	got := text.String()
	if strings.Contains(got, "supersecrettoken") {
		t.Fatalf("a secret leaked into the diff:\n%s", got)
	}
	if !strings.Contains(got, "+        - ok.example") || !strings.Contains(got, "+    token: (hidden)") {
		t.Errorf("diff does not show the change:\n%s", got)
	}
	if _, err := s.VersionDiff("../../etc/passwd"); err != ErrNoVersion {
		t.Errorf("a path as a version id: %v", err)
	}
}

func TestRestoreKeepsSecretsAndListeners(t *testing.T) {
	s := openHistoryStore(t)
	if err := s.UpdateAs("me", "", func(c *Config) error {
		c.Blocking.Allowlist = []string{"first.example"}
		c.API.Token = "old-token"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first := s.History()[0].ID
	if err := s.UpdateAs("me", "", func(c *Config) error {
		c.Blocking.Allowlist = []string{"second.example"}
		c.API.Token = "new-token"
		c.API.PasswordHash = "$argon2id$current"
		c.DNS.Listen = ":5353"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreVersion(first, "Password sign-in from 192.168.1.20"); err != nil {
		t.Fatal(err)
	}
	c := s.Get()
	if !slices.Equal(c.Blocking.Allowlist, []string{"first.example"}) {
		t.Errorf("allowlist not restored: %v", c.Blocking.Allowlist)
	}
	if c.API.Token != "new-token" || c.API.PasswordHash != "$argon2id$current" {
		t.Error("restore changed sign-in or the API token")
	}
	if c.DNS.Listen != ":5353" {
		t.Error("restore changed a restart-only setting")
	}
	h := s.History()
	if !strings.HasPrefix(h[0].Note, "Restored the settings from ") || !slices.Contains(h[0].Keys, "blocking.allowlist") {
		t.Errorf("restore not recorded as its own change: %+v", h[0])
	}
	if err := s.RestoreVersion("12345", "me"); err != ErrNoVersion {
		t.Errorf("restoring an unknown version: %v", err)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	s := openHistoryStore(t)
	for i := range MaxHistory + 5 {
		if err := s.UpdateAs("me", "", func(c *Config) error { c.DNS.RateLimit = 100 + i; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.History()); n != MaxHistory {
		t.Fatalf("%d versions kept, want %d", n, MaxHistory)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.Path()), "history", "*.yaml"))
	if len(files) != MaxHistory {
		t.Errorf("%d snapshot files, want %d", len(files), MaxHistory)
	}
	if info, err := os.Stat(files[0]); err == nil && info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("snapshots readable by others: %v", info.Mode())
	}
}
