// Package applog keeps Minos's own log viewable from the UI: a slog handler
// that writes every record to the usual destination (stderr → the journal
// or `docker logs`) and also into a small in-memory ring, with a level that
// changes live. Secrets never reach the ring: attributes whose names look
// like credentials, and credentials embedded in URLs, are redacted first.
//
// A leaf package (stdlib only), so the API and main can share it without
// any dependency on the rest of Minos.
package applog

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultSize is how many records the ring keeps: a few hours of a quiet
// server at info, enough to see what happened before a problem.
const DefaultSize = 2000

const subBuffer = 256

// Record is one log line as the UI sees it.
type Record struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"` // debug, info, warn, error
	Message string    `json:"message"`
	Attrs   []Attr    `json:"attrs,omitempty"`
}

// Attr is one key=value pair, flattened (groups become dotted keys).
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Ring holds the newest records and fans new ones out to subscribers.
type Ring struct {
	mu    sync.Mutex
	buf   []Record
	next  int // index the next record goes in
	full  bool
	seq   uint64
	alert uint64 // seq of the newest warn/error record
	subs  map[chan Record]struct{}
}

// NewRing returns a ring holding up to size records.
func NewRing(size int) *Ring {
	if size <= 0 {
		size = DefaultSize
	}
	return &Ring{buf: make([]Record, size), subs: make(map[chan Record]struct{})}
}

func (r *Ring) add(rec Record) {
	r.mu.Lock()
	r.seq++
	rec.Seq = r.seq
	if rec.Level == "warn" || rec.Level == "error" {
		r.alert = rec.Seq
	}
	r.buf[r.next] = rec
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	for ch := range r.subs {
		select {
		case ch <- rec:
		default: // a slow viewer misses lines rather than slowing logging
		}
	}
	r.mu.Unlock()
}

// Records returns the held records with Seq > after, oldest first.
func (r *Ring) Records(after uint64) []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Record
	n := r.next
	if r.full {
		n = len(r.buf)
	}
	start := 0
	if r.full {
		start = r.next
	}
	for i := 0; i < n; i++ {
		rec := r.buf[(start+i)%len(r.buf)]
		if rec.Seq > after {
			out = append(out, rec)
		}
	}
	return out
}

// AlertSeq is the sequence number of the newest warning or error (0: none
// since start) — the UI compares it with the last one the user has seen.
func (r *Ring) AlertSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alert
}

// Subscribe streams new records until cancel is called.
func (r *Ring) Subscribe() (<-chan Record, func()) {
	ch := make(chan Record, subBuffer)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs, ch)
			r.mu.Unlock()
		})
	}
}

// Handler tees records to an inner handler and the ring. Its level is a
// shared LevelVar, so changing it applies to both at once.
type Handler struct {
	inner  slog.Handler
	ring   *Ring
	level  *slog.LevelVar
	attrs  []Attr // from WithAttrs, already flattened and redacted
	prefix string // from WithGroup, "a.b."
}

// NewHandler wraps inner. inner must be built with the same LevelVar (or
// no level), since Enabled here is the one gate.
func NewHandler(inner slog.Handler, ring *Ring, level *slog.LevelVar) *Handler {
	return &Handler{inner: inner, ring: ring, level: level}
}

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.inner.Handle(ctx, r)
	rec := Record{Time: r.Time, Level: LevelName(r.Level), Message: r.Message}
	rec.Attrs = append(rec.Attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		rec.Attrs = appendAttr(rec.Attrs, h.prefix, a)
		return true
	})
	h.ring.add(rec)
	return err
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	h2 := *h
	h2.inner = h.inner.WithAttrs(as)
	h2.attrs = append([]Attr(nil), h.attrs...)
	for _, a := range as {
		h2.attrs = appendAttr(h2.attrs, h.prefix, a)
	}
	return &h2
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := *h
	h2.inner = h.inner.WithGroup(name)
	h2.prefix = h.prefix + name + "."
	return &h2
}

func appendAttr(out []Attr, prefix string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return out
	}
	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			out = appendAttr(out, p, g)
		}
		return out
	}
	key := prefix + a.Key
	val := a.Value.String()
	if a.Value.Kind() == slog.KindTime {
		val = a.Value.Time().Format(time.RFC3339)
	}
	return append(out, Attr{Key: key, Value: redact(key, val)})
}

// LevelName is the lowercase level word the API and UI use.
func LevelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}

// ParseLevel reads "debug", "info", "warn" or "error".
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("log level must be debug, info, warn or error, got %q", s)
}

const redacted = "[redacted]"

// ReplaceAttr applies the same redaction to another handler's output (the
// journal's text handler), so a secret is hidden everywhere, not only in
// the UI: pass it as slog.HandlerOptions.ReplaceAttr.
func ReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindString && a.Value.Kind() != slog.KindAny {
		return a
	}
	key := a.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + a.Key
	}
	v := a.Value.Resolve().String()
	if r := redact(key, v); r != v {
		return slog.String(a.Key, r)
	}
	return a
}

// secretWords: an attribute whose last key segment contains one of these
// holds a credential, whatever its value looks like.
var secretWords = []string{"token", "password", "passwd", "secret", "auth", "credential", "cookie", "apikey", "api_key"}

func secretKey(key string) bool {
	k := strings.ToLower(key)
	if i := strings.LastIndexByte(k, '.'); i >= 0 {
		k = k[i+1:]
	}
	if k == "key" || strings.HasSuffix(k, "_key") || strings.HasSuffix(k, "-key") {
		return true
	}
	for _, w := range secretWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// redact hides credentials: the whole value for a secret-named key, and
// inside any URL-shaped value the password and credential-like query
// parameters (a list URL with ?token=…, a webhook with user:pass@).
func redact(key, val string) string {
	if secretKey(key) && val != "" {
		return redacted
	}
	return redactURLs(val)
}

func redactURLs(val string) string {
	if !strings.Contains(val, "://") {
		return val
	}
	fields := strings.Fields(val)
	changed := false
	for i, f := range fields {
		trimmed := strings.Trim(f, `"'(),;:`)
		u, err := url.Parse(trimmed)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		dirty := false
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			dirty = true
		}
		q := u.Query()
		for k := range q {
			if secretKey(k) || strings.EqualFold(k, "key") {
				q.Set(k, "xxxxx")
				dirty = true
			}
		}
		if dirty {
			u.RawQuery = q.Encode()
			fields[i] = strings.Replace(f, trimmed, u.String(), 1)
			changed = true
		}
	}
	if !changed {
		return val
	}
	return strings.Join(fields, " ")
}
