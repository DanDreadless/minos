package importer

import (
	"bufio"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"minos/internal/config"
)

// Pi-hole domainlist types (gravity.db schema, v5 and v6).
const (
	piholeExactAllow = 0
	piholeExactDeny  = 1
	piholeRegexAllow = 2
	piholeRegexDeny  = 3
)

// Pihole imports from a Pi-hole configuration directory (typically
// /etc/pihole): blocklist subscriptions and exact allow/deny domains from
// gravity.db, and local DNS records from custom.list when present.
// path may also point straight at a gravity.db file.
func Pihole(path string, cfg *config.Config) (*Report, error) {
	dbPath := path
	dir := filepath.Dir(path)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		dbPath = filepath.Join(path, "gravity.db")
		dir = path
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no gravity.db at %s (point at your Pi-hole config directory, usually /etc/pihole)", dbPath)
	}

	rep := &Report{}
	if err := piholeGravity(dbPath, cfg, rep); err != nil {
		return nil, err
	}
	// custom.list holds Pi-hole's "Local DNS Records"; absent on many installs.
	customList := filepath.Join(dir, "custom.list")
	if _, err := os.Stat(customList); err == nil {
		if err := piholeCustomList(customList, cfg, rep); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

func piholeGravity(dbPath string, cfg *config.Config, rep *Report) error {
	// mode=ro keeps this safe to run against a live Pi-hole's database.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open gravity.db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT address, enabled, comment FROM adlist`)
	if err != nil {
		return fmt.Errorf("read adlists (is this a Pi-hole gravity.db?): %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var address string
		var enabled bool
		var comment sql.NullString
		if err := rows.Scan(&address, &enabled, &comment); err != nil {
			return fmt.Errorf("scan adlist: %w", err)
		}
		n++
		name := strings.TrimSpace(comment.String)
		if name == "" {
			name = fmt.Sprintf("pihole-%d", n)
		}
		// Pi-hole treats every adlist as hosts format; so do we.
		if mergeList(cfg, name, address, "hosts", enabled) {
			rep.Lists++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read adlists: %w", err)
	}

	domains, err := db.Query(`SELECT type, domain, enabled FROM domainlist`)
	if err != nil {
		return fmt.Errorf("read domainlist: %w", err)
	}
	defer domains.Close()
	for domains.Next() {
		var kind int
		var domain string
		var enabled bool
		if err := domains.Scan(&kind, &domain, &enabled); err != nil {
			return fmt.Errorf("scan domainlist: %w", err)
		}
		if !enabled {
			continue
		}
		switch kind {
		case piholeExactAllow:
			rep.Allow += mergeDomain(&cfg.Blocking.Allowlist, domain)
		case piholeExactDeny:
			rep.Deny += mergeDomain(&cfg.Blocking.Denylist, domain)
		case piholeRegexAllow, piholeRegexDeny:
			rep.skip("regex rule %q: Minos does not support regex rules", domain)
		}
	}
	return domains.Err()
}

// piholeCustomList parses Pi-hole's custom.list: one "IP hostname..." per line.
func piholeCustomList(path string, cfg *config.Config, rep *Report) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open custom.list: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			rep.skip("custom.list line %q: not \"IP hostname\"", line)
			continue
		}
		ip := net.ParseIP(fields[0])
		if ip == nil {
			rep.skip("custom.list line %q: %q is not an IP", line, fields[0])
			continue
		}
		for _, host := range fields[1:] {
			rec := config.LocalRecord{Name: host}
			if ip.To4() != nil {
				rec.A = []string{fields[0]}
			} else {
				rec.AAAA = []string{fields[0]}
			}
			mergeLocalRecord(cfg, rec, rep)
		}
	}
	return scanner.Err()
}
