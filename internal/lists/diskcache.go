package lists

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// diskCache keeps the last good body of every list on disk, so a restart
// while a list host is down — or with no network at all — still starts with
// every list compiled. Before it existed the cache was memory only: reboot
// during an outage and that list came back with zero rules.
//
// Entries are keyed by URL (a rename keeps its cache; a URL change is a
// different list). SD-card discipline: the body is written only when its
// content changes, so a 304 or an identical 200 costs a small metadata
// write at most. Every write is temp-file + rename, so a power cut leaves
// either the old entry or the new one, never half of one.
type diskCache struct {
	dir string
}

// cacheMeta is the sidecar for one cached body.
type cacheMeta struct {
	URL          string    `json:"url"`
	SHA256       string    `json:"sha256"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	// Source is the mirror URL that served this body; empty = the primary.
	// Validators are only ever sent back to the URL that issued them.
	Source string `json:"source,omitempty"`
}

func newDiskCache(dir string) (*diskCache, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("list cache dir: %w", err)
	}
	return &diskCache{dir: dir}, nil
}

func cacheKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:12])
}

func (c *diskCache) paths(url string) (body, meta string) {
	k := cacheKey(url)
	return filepath.Join(c.dir, k+".list"), filepath.Join(c.dir, k+".json")
}

// load returns the cached body and its metadata, or ok=false when there is
// no usable entry. A body whose hash disagrees with its sidecar (a torn or
// tampered file) is treated as absent rather than compiled.
func (c *diskCache) load(url string) (body []byte, meta cacheMeta, ok bool) {
	bp, mp := c.paths(url)
	raw, err := os.ReadFile(mp)
	if err != nil {
		return nil, cacheMeta{}, false
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.URL != url {
		return nil, cacheMeta{}, false
	}
	body, err = os.ReadFile(bp)
	if err != nil {
		return nil, cacheMeta{}, false
	}
	if bodyHash(body) != meta.SHA256 {
		slog.Warn("cached list body does not match its checksum; ignoring it", "url", url)
		return nil, cacheMeta{}, false
	}
	return body, meta, true
}

// store records a freshly fetched body. The body file is rewritten only when
// the content differs from what is already on disk.
func (c *diskCache) store(url string, body []byte, meta cacheMeta) error {
	bp, mp := c.paths(url)
	meta.URL = url
	meta.SHA256 = bodyHash(body)
	var prev cacheMeta
	if raw, err := os.ReadFile(mp); err == nil && json.Unmarshal(raw, &prev) == nil &&
		prev.SHA256 == meta.SHA256 && fileExists(bp) {
		return c.writeMeta(mp, meta) // same content: metadata only
	}
	if err := atomicWrite(bp, body); err != nil {
		return err
	}
	return c.writeMeta(mp, meta)
}

// touch refreshes the metadata after a 304 — the body is unchanged.
func (c *diskCache) touch(url string, meta cacheMeta) error {
	_, mp := c.paths(url)
	var prev cacheMeta
	raw, err := os.ReadFile(mp)
	if err != nil || json.Unmarshal(raw, &prev) != nil {
		return fmt.Errorf("touch list cache: no entry for %s", url)
	}
	meta.URL, meta.SHA256 = url, prev.SHA256
	if meta.Source == "" {
		meta.Source = prev.Source
	}
	if meta.ETag == "" {
		meta.ETag = prev.ETag
	}
	if meta.LastModified == "" {
		meta.LastModified = prev.LastModified
	}
	return c.writeMeta(mp, meta)
}

func (c *diskCache) writeMeta(path string, meta cacheMeta) error {
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal list cache meta: %w", err)
	}
	return atomicWrite(path, raw)
}

// prune deletes entries for URLs no longer configured, so a removed list
// doesn't keep megabytes on the card forever.
func (c *diskCache) prune(keep map[string]bool) {
	keys := make(map[string]bool, len(keep))
	for u := range keep {
		keys[cacheKey(u)] = true
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".tmp-") { // a write a power cut interrupted
			_ = os.Remove(filepath.Join(c.dir, name))
			continue
		}
		k := strings.TrimSuffix(strings.TrimSuffix(name, ".list"), ".json")
		if k == name || keys[k] {
			continue // not ours, or still configured
		}
		if err := os.Remove(filepath.Join(c.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("prune list cache", "file", name, "err", err)
		}
	}
}

func bodyHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("list cache write: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("list cache write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("list cache write: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("list cache write: %w", err)
	}
	return nil
}
