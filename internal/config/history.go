package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings history: every change made through the API (a person, a script or
// the minos command — never Minos's own housekeeping) keeps a snapshot of
// the settings it produced, so any version can be inspected and restored.
// Snapshots live beside the config in history/, the newest MaxHistory of
// them; they hold secrets exactly as the config file does, and the diff and
// API never show those.

// MaxHistory is how many versions are kept.
const MaxHistory = 100

// HistoryEntry is one recorded change.
type HistoryEntry struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`
	Actor string    `json:"actor"` // Who: "Password sign-in from 192.168.1.20", "minos command", …
	Note  string    `json:"note,omitempty"`
	// Keys are the settings that changed, as dotted config keys
	// ("blocking.allowlist"); the UI names them from the user guide.
	Keys []string `json:"keys"`
}

// ErrNoVersion is returned for an unknown history id.
var ErrNoVersion = errors.New("no such settings version")

type history struct {
	dir string
	mu  sync.Mutex
}

func (h *history) indexPath() string { return filepath.Join(h.dir, "index.json") }

func (h *history) load() []HistoryEntry {
	var out []HistoryEntry
	if b, err := os.ReadFile(h.indexPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (h *history) store(entries []HistoryEntry) error {
	b, err := json.MarshalIndent(entries, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(h.dir, ".index-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, h.indexPath())
}

func (h *history) writeSnapshot(id string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(h.dir, id+".yaml"), data, 0o600)
}

// record adds a version. The first change ever recorded also stores the
// settings as they were before it, so it too can be undone.
func (h *history) record(prev, next *Config, keys []string, actor, note string, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return err
	}
	entries := h.load()
	if len(entries) == 0 {
		base := HistoryEntry{ID: versionID(now.Add(-time.Nanosecond)), Time: now, Actor: "Before history began", Keys: []string{}}
		if err := h.writeSnapshot(base.ID, prev); err != nil {
			return err
		}
		entries = append(entries, base)
	}
	id := versionID(now)
	if last := entries[len(entries)-1].ID; idNum(id) <= idNum(last) {
		// The clock can be coarse (Windows) or step back: ids must still
		// grow, or a quick second change would overwrite the first.
		id = strconv.FormatInt(idNum(last)+1, 10)
	}
	e := HistoryEntry{ID: id, Time: now, Actor: actor, Note: note, Keys: keys}
	if err := h.writeSnapshot(e.ID, next); err != nil {
		return err
	}
	entries = append(entries, e)
	for len(entries) > MaxHistory {
		_ = os.Remove(filepath.Join(h.dir, entries[0].ID+".yaml"))
		entries = entries[1:]
	}
	return h.store(entries)
}

func versionID(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func idNum(id string) int64 {
	n, _ := strconv.ParseInt(id, 10, 64)
	return n
}

func (h *history) snapshot(id string) (*Config, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return nil, ErrNoVersion // Ids are numbers; anything else is not a file we wrote
	}
	data, err := os.ReadFile(filepath.Join(h.dir, id+".yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoVersion
	}
	if err != nil {
		return nil, err
	}
	c, err := parseTolerant(data)
	if err != nil {
		return nil, fmt.Errorf("read version %s: %w", id, err)
	}
	return c, nil
}

// History returns the recorded versions, newest first.
func (s *Store) History() []HistoryEntry {
	if s.hist == nil {
		return nil
	}
	s.hist.mu.Lock()
	defer s.hist.mu.Unlock()
	e := s.hist.load()
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
	return e
}

// DiffLine is one line of a settings diff: Op is " ", "+" or "-".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// VersionDiff compares a version with the one before it, secrets hidden,
// keeping two lines of context around each change.
func (s *Store) VersionDiff(id string) ([]DiffLine, error) {
	if s.hist == nil {
		return nil, ErrNoVersion
	}
	entries := s.History()
	idx := -1
	for i, e := range entries {
		if e.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return nil, ErrNoVersion
	}
	if idx+1 == len(entries) {
		return []DiffLine{}, nil // The first version has nothing before it
	}
	after, err := s.hist.snapshot(id)
	if err != nil {
		return nil, err
	}
	before, err := s.hist.snapshot(entries[idx+1].ID)
	if err != nil {
		return nil, err
	}
	a, err := yaml.Marshal(Redacted(before))
	if err != nil {
		return nil, err
	}
	b, err := yaml.Marshal(Redacted(after))
	if err != nil {
		return nil, err
	}
	return diffLines(strings.Split(string(a), "\n"), strings.Split(string(b), "\n"), 2), nil
}

// RestoreVersion makes a recorded version current again — except sign-in and
// other secrets, and the settings that need a restart, which stay as they are
// now: restoring can never lock anyone out or move a listener. Recorded as a
// change of its own, so it can be undone too.
func (s *Store) RestoreVersion(id, actor string) error {
	if s.hist == nil {
		return ErrNoVersion
	}
	old, err := s.hist.snapshot(id)
	if err != nil {
		return err
	}
	var when string
	for _, e := range s.History() {
		if e.ID == id {
			when = e.Time.Local().Format("2 Jan 15:04")
		}
	}
	return s.UpdateAs(actor, "Restored the settings from "+when, func(c *Config) error {
		next := old.Clone()
		keepLive(next, c)
		*c = *next
		return nil
	})
}

// keepLive copies onto restored what a restore must not change: sign-in and
// API credentials, notification and certificate secrets, list request
// headers, and the file-only (restart-needed) settings.
func keepLive(restored, cur *Config) {
	restored.API = cur.API
	restored.Notifications.NtfyToken = cur.Notifications.NtfyToken
	restored.DNS.Listen = cur.DNS.Listen
	restored.DNS.TLS = cur.DNS.TLS
	restored.QueryLog.Ephemeral = cur.QueryLog.Ephemeral
	restored.QueryLog.DBPath = cur.QueryLog.DBPath
	headers := map[string]map[string]string{}
	for _, src := range append(append([]ListSource{}, cur.Lists.Sources...), cur.Lists.AllowSources...) {
		if len(src.Headers) > 0 {
			headers[src.Name] = src.Headers
		}
	}
	for i := range restored.Lists.Sources {
		restored.Lists.Sources[i].Headers = headers[restored.Lists.Sources[i].Name]
	}
	for i := range restored.Lists.AllowSources {
		restored.Lists.AllowSources[i].Headers = headers[restored.Lists.AllowSources[i].Name]
	}
}

const hidden = "(hidden)"

// Redacted returns a copy with every secret replaced by "(hidden)" — for
// showing settings, never for saving them.
func Redacted(c *Config) *Config {
	r := c.Clone()
	hide := func(s *string) {
		if *s != "" {
			*s = hidden
		}
	}
	hide(&r.API.Token)
	hide(&r.API.PasswordHash)
	hide(&r.API.SessionKey)
	hide(&r.API.TOTPSecret)
	hide(&r.Notifications.NtfyToken)
	if r.DNS.TLS.ACME != nil {
		acme := *r.DNS.TLS.ACME // A copy: the clone may share the pointer
		hide(&acme.APIToken)
		hide(&acme.TSIGSecret)
		r.DNS.TLS.ACME = &acme
	}
	for _, list := range []*[]ListSource{&r.Lists.Sources, &r.Lists.AllowSources} {
		for i := range *list {
			if len((*list)[i].Headers) > 0 {
				h := map[string]string{}
				for k := range (*list)[i].Headers {
					h[k] = hidden
				}
				(*list)[i].Headers = h
			}
		}
	}
	return r
}

// ChangedKeys lists the settings that differ between a and b as dotted
// config keys, descending into sections but treating lists as one setting.
// It compares the real values (a password change is a change) but returns
// only key names.
func ChangedKeys(a, b *Config) []string {
	var ma, mb map[string]any
	toMap := func(c *Config, m *map[string]any) {
		data, _ := yaml.Marshal(c)
		_ = yaml.Unmarshal(data, m)
	}
	toMap(a, &ma)
	toMap(b, &mb)
	out := []string{}
	var walk func(prefix string, x, y map[string]any)
	walk = func(prefix string, x, y map[string]any) {
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		for k := range keys {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			xv, yv := x[k], y[k]
			xm, xok := xv.(map[string]any)
			ym, yok := yv.(map[string]any)
			switch {
			case xok && yok:
				walk(path, xm, ym)
			case !reflect.DeepEqual(xv, yv):
				out = append(out, path)
			}
		}
	}
	walk("", ma, mb)
	sort.Strings(out)
	return out
}

// diffLines is a line diff (longest common subsequence) trimmed to context
// lines around each change; a gap is shown as a "…" line.
func diffLines(a, b []string, context int) []DiffLine {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var all []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			all = append(all, DiffLine{" ", a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			all = append(all, DiffLine{"-", a[i]})
			i++
		default:
			all = append(all, DiffLine{"+", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		all = append(all, DiffLine{"-", a[i]})
	}
	for ; j < m; j++ {
		all = append(all, DiffLine{"+", b[j]})
	}
	keep := make([]bool, len(all))
	for k, l := range all {
		if l.Op != " " {
			for c := max(0, k-context); c <= min(len(all)-1, k+context); c++ {
				keep[c] = true
			}
		}
	}
	out := []DiffLine{}
	gap := false
	for k, l := range all {
		if keep[k] {
			out = append(out, l)
			gap = false
		} else if !gap && len(out) > 0 {
			out = append(out, DiffLine{" ", "…"})
			gap = true
		}
	}
	return out
}
