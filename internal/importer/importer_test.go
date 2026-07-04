package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"minos/internal/config"
)

// writeGravityDB builds a minimal Pi-hole gravity.db fixture.
func writeGravityDB(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "gravity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE adlist (id INTEGER PRIMARY KEY, address TEXT, enabled BOOLEAN, comment TEXT)`,
		`CREATE TABLE domainlist (id INTEGER PRIMARY KEY, type INTEGER, domain TEXT, enabled BOOLEAN)`,
		`INSERT INTO adlist (address, enabled, comment) VALUES
			('https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts', 1, 'StevenBlack'),
			('https://example.com/other-list.txt', 0, ''),
			('https://example.com/dupe.txt', 1, 'StevenBlack')`,
		`INSERT INTO domainlist (type, domain, enabled) VALUES
			(0, 'allowed.example.com', 1),
			(1, 'denied.example.com', 1),
			(1, 'disabled.example.com', 0),
			(2, '^regex-allow\.', 1),
			(3, '(^|\.)regex-deny\.com$', 1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
}

func TestPiholeImport(t *testing.T) {
	dir := t.TempDir()
	writeGravityDB(t, dir)
	customList := "# comment\n192.168.1.10 nas.home.lab\nfd00::10 v6.home.lab\nbadline\nnot-an-ip host.lab\n"
	if err := os.WriteFile(filepath.Join(dir, "custom.list"), []byte(customList), 0o644); err != nil {
		t.Fatal(err)
	}

	// Existing config already subscribes to StevenBlack: the import must
	// dedupe it by URL, not by name.
	cfg := config.Default()
	rep, err := Pihole(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("imported config is invalid: %v", err)
	}

	if rep.Lists != 2 {
		t.Errorf("Lists = %d, want 2 (StevenBlack URL deduped)", rep.Lists)
	}
	if len(cfg.Lists.Sources) != 3 {
		t.Fatalf("sources = %d, want 3", len(cfg.Lists.Sources))
	}
	// The duplicate comment name must get a suffix, and the disabled list
	// stays disabled.
	var sawSuffix, sawDisabled bool
	for _, src := range cfg.Lists.Sources {
		if src.Name == "StevenBlack-2" {
			sawSuffix = true
		}
		if src.URL == "https://example.com/other-list.txt" && !src.Enabled {
			sawDisabled = true
		}
	}
	if !sawSuffix || !sawDisabled {
		t.Errorf("sources = %+v, want a StevenBlack-2 suffix and a disabled other-list", cfg.Lists.Sources)
	}

	if rep.Allow != 1 || cfg.Blocking.Allowlist[0] != "allowed.example.com" {
		t.Errorf("allowlist = %v (rep %d), want allowed.example.com", cfg.Blocking.Allowlist, rep.Allow)
	}
	if rep.Deny != 1 || cfg.Blocking.Denylist[0] != "denied.example.com" {
		t.Errorf("denylist = %v (rep %d), want denied.example.com (disabled excluded)", cfg.Blocking.Denylist, rep.Deny)
	}
	if rep.LocalRecords != 2 {
		t.Errorf("LocalRecords = %d, want 2", rep.LocalRecords)
	}
	// Two regex rules + two bad custom.list lines.
	if len(rep.Skipped) != 4 {
		t.Errorf("Skipped = %v, want 4 reasons", rep.Skipped)
	}
}

func TestPiholeMissingDB(t *testing.T) {
	if _, err := Pihole(t.TempDir(), config.Default()); err == nil {
		t.Error("expected an error for a directory without gravity.db")
	}
}

const adguardFixture = `
filters:
  - enabled: true
    url: https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt
    name: AdGuard DNS filter
  - enabled: false
    url: https://example.com/second.txt
    name: ""
whitelist_filters:
  - enabled: true
    url: https://example.com/allow.txt
    name: exceptions
user_rules:
  - '||ads.example.com^'
  - '@@||good.example.com^'
  - plain.example.com
  - '! a comment'
  - '||tracker.example.com^$important'
  - '/banner\d+/'
dns:
  rewrites:
    - domain: nas.home.lab
      answer: 192.168.1.10
filtering:
  rewrites:
    - domain: "*.lab.example.com"
      answer: 192.168.1.20
    - domain: alias.home.lab
      answer: nas.home.lab
  blocked_services:
    ids:
      - tiktok
      - epic_games
      - not_a_real_service
`

func TestAdGuardImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AdGuardHome.yaml")
	if err := os.WriteFile(path, []byte(adguardFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	rep, err := AdGuard(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("imported config is invalid: %v", err)
	}

	if rep.Lists != 2 {
		t.Errorf("Lists = %d, want 2", rep.Lists)
	}
	var adblockCount int
	for _, src := range cfg.Lists.Sources {
		if src.Format == "adblock" {
			adblockCount++
		}
	}
	if adblockCount != 2 {
		t.Errorf("adblock sources = %d, want 2", adblockCount)
	}

	if rep.Deny != 2 { // ads.example.com + plain.example.com
		t.Errorf("Deny = %d, want 2", rep.Deny)
	}
	if rep.Allow != 1 || cfg.Blocking.Allowlist[0] != "good.example.com" {
		t.Errorf("Allow = %d %v, want good.example.com", rep.Allow, cfg.Blocking.Allowlist)
	}

	if rep.LocalRecords != 3 {
		t.Errorf("LocalRecords = %d, want 3 (A, wildcard A, CNAME)", rep.LocalRecords)
	}
	var sawCNAME bool
	for _, r := range cfg.DNS.LocalRecords {
		if r.Name == "alias.home.lab" && r.CNAME == "nas.home.lab" {
			sawCNAME = true
		}
	}
	if !sawCNAME {
		t.Errorf("local records = %+v, want alias.home.lab CNAME", cfg.DNS.LocalRecords)
	}

	if rep.Services != 2 || !contains(cfg.Blocking.Services, "tiktok") || !contains(cfg.Blocking.Services, "epicgames") {
		t.Errorf("services = %v (rep %d), want tiktok + epicgames (alias mapped)", cfg.Blocking.Services, rep.Services)
	}

	// whitelist filter + $important rule + regex rule + unknown service.
	if len(rep.Skipped) != 4 {
		t.Errorf("Skipped = %v, want 4 reasons", rep.Skipped)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeGravityDB(t, dir)
	cfg := config.Default()
	if _, err := Pihole(dir, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := Pihole(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if again.Lists != 0 || again.Allow != 0 || again.Deny != 0 || again.LocalRecords != 0 {
		t.Errorf("second import added items: %+v, want all zero", again)
	}
}
