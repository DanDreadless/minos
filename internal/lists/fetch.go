package lists

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"minos/internal/filter"
)

// failKind classifies why a source's last attempt failed. It decides both
// what the user is told and how soon Minos tries again.
type failKind int

const (
	failNone failKind = iota
	// failTransient: network trouble, 5xx, 429 — retried soon, with backoff.
	failTransient
	// failRetired: 404 or 410 — the publisher removed it. Retried only at
	// the normal interval (it may come back), never hammered.
	failRetired
	// failUnauthorized: 401 or 403 — the feed now needs credentials.
	failUnauthorized
	// failSuspicious: the download arrived but failed vetting (a web page
	// instead of a list, or a sudden collapse in size); the last good copy
	// stays in force.
	failSuspicious
)

// fetchError is a classified fetch failure.
type fetchError struct {
	kind       failKind
	retryAfter time.Duration // from a 429/503 Retry-After, 0 if none
	err        error
}

func (e *fetchError) Error() string { return e.err.Error() }
func (e *fetchError) Unwrap() error { return e.err }

func kindOf(err error) failKind {
	var fe *fetchError
	if errors.As(err, &fe) {
		return fe.kind
	}
	return failTransient
}

// Variables so tests can shrink them.
var (
	// attemptBackoff spaces the quick retries inside one refresh, for blips.
	attemptBackoff = []time.Duration{1 * time.Second, 3 * time.Second}
	// retrySchedule spaces the retries of a source that is still failing
	// after those, by consecutive failures, capped at the refresh interval.
	// Before it existed a failed source waited a whole interval (24 h by
	// default) — a Pi that booted before its Wi-Fi had no lists for a day.
	retrySchedule = []time.Duration{
		1 * time.Minute, 2 * time.Minute, 5 * time.Minute,
		15 * time.Minute, 30 * time.Minute, time.Hour,
	}
	// maxRetryAfter caps how long a server's Retry-After can stall a refresh.
	maxRetryAfter = time.Minute
)

const (
	// shrinkFloor: below this many rules a list is too small for a size
	// collapse to mean anything (tiny feeds legitimately swing).
	shrinkFloor = 100
	// shrinkAcceptAfter: the same smaller body served this many refreshes in
	// a row is taken as the list's real new size, so a list that genuinely
	// halved heals on its own instead of being frozen at its old copy.
	shrinkAcceptAfter = 3
)

// fetchResult is one download. notModified means a 304 answered a
// conditional request: the held body is current and body is nil.
type fetchResult struct {
	body         []byte
	notModified  bool
	etag         string
	lastModified string
}

// fetch downloads one list with a hard size cap and timeout. With validators
// in prev it asks conditionally, so an unchanged list costs a 304 instead of
// the whole download — most refreshes, for most lists.
func (m *Manager) fetch(ctx context.Context, url string, prev cacheMeta) (fetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetchResult{}, &fetchError{kind: failRetired, err: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("User-Agent", m.ua)
	if prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	if prev.LastModified != "" {
		req.Header.Set("If-Modified-Since", prev.LastModified)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fetchResult{}, &fetchError{kind: failTransient, err: fmt.Errorf("fetch: %w", err)}
	}
	defer resp.Body.Close()
	res := fetchResult{etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified && (prev.ETag != "" || prev.LastModified != "") {
		res.notModified = true
		return res, nil
	}
	if resp.StatusCode != http.StatusOK {
		return fetchResult{}, classifyStatus(resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return fetchResult{}, &fetchError{kind: failTransient, err: fmt.Errorf("read body: %w", err)}
	}
	if len(body) > maxListBytes {
		return fetchResult{}, &fetchError{kind: failSuspicious, err: fmt.Errorf("list exceeds %d byte cap", maxListBytes)}
	}
	res.body = body
	return res, nil
}

func classifyStatus(resp *http.Response) *fetchError {
	err := fmt.Errorf("fetch: unexpected status %s", resp.Status)
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return &fetchError{kind: failRetired, err: err}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &fetchError{kind: failUnauthorized, err: err}
	}
	fe := &fetchError{kind: failTransient, err: err}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		fe.retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	}
	return fe
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return min(time.Duration(s)*time.Second, maxRetryAfter)
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, maxRetryAfter)
		}
	}
	return 0
}

// fetchWithRetry retries transient failures a couple of times inside one
// refresh, so a blip doesn't cost a whole retry cycle. Permanent ones
// (retired, unauthorized) return at once.
func (m *Manager) fetchWithRetry(ctx context.Context, url string, prev cacheMeta) (fetchResult, error) {
	for attempt := 0; ; attempt++ {
		res, err := m.fetch(ctx, url, prev)
		if err == nil || kindOf(err) != failTransient || attempt >= len(attemptBackoff) {
			return res, err
		}
		wait := attemptBackoff[attempt]
		var fe *fetchError
		if errors.As(err, &fe) && fe.retryAfter > wait {
			wait = fe.retryAfter
		}
		select {
		case <-ctx.Done():
			return res, err
		case <-time.After(wait):
		}
	}
}

// looksLikeHTML catches the classic silent failure: a captive portal, a
// CDN error page or a "this list has moved" page served with a 200. Every
// line would parse as skipped and the list would quietly protect nothing.
func looksLikeHTML(body []byte) bool {
	head := bytes.ToLower(bytes.TrimSpace(body[:min(len(body), 512)]))
	return bytes.HasPrefix(head, []byte("<!doctype html")) || bytes.HasPrefix(head, []byte("<html")) ||
		bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<head"))
}

// countRules runs the real parser over body without storing anything.
func countRules(format string, allow bool, body []byte) int {
	b := filter.NewCountingBuilder()
	_, _ = Parse(format, "vet", allow, bytes.NewReader(body), b)
	return b.Counted()
}

// vet decides whether a freshly downloaded body may replace the copy in
// force. prevRules < 0 means there is no previous copy, so there is nothing
// to protect and only an HTML page is refused. Caller holds no lock.
func (m *Manager) vet(src annotated, body []byte, prevRules int) error {
	if looksLikeHTML(body) {
		// Usually a captive portal or a CDN error page: transient, so it is
		// retried soon like any other blip.
		return &fetchError{kind: failTransient,
			err: errors.New("the server sent a web page, not a list — kept the previous copy")}
	}
	if prevRules < shrinkFloor {
		return nil
	}
	n := countRules(src.Format, src.allow, body)
	if n*2 >= prevRules {
		m.mu.Lock()
		delete(m.rejects, src.Name)
		m.mu.Unlock()
		return nil
	}
	// The same smaller body shrinkAcceptAfter refreshes running is the
	// list's real new size, not a glitch.
	h := bodyHash(body)
	m.mu.Lock()
	r := m.rejects[src.Name]
	if r.hash == h {
		r.count++
	} else {
		r = rejection{hash: h, count: 1}
	}
	m.rejects[src.Name] = r
	m.mu.Unlock()
	if r.count >= shrinkAcceptAfter {
		slog.Info("list shrank and stayed small; accepting its new size",
			"list", src.Name, "rules", n, "was", prevRules)
		m.mu.Lock()
		delete(m.rejects, src.Name)
		m.mu.Unlock()
		return nil
	}
	return &fetchError{kind: failSuspicious, err: fmt.Errorf(
		"the new download has %d rules against %d before — kept the previous copy (accepted if it stays this size %d refreshes running)",
		n, prevRules, shrinkAcceptAfter)}
}

// rejection tracks a body vet refused, to recognise it coming back.
type rejection struct {
	hash  string
	count int
}

// refreshSource fetches one source (conditionally, when a body is already
// held), vets it, and records the outcome. A failure of any kind keeps the
// last good body in force.
func (m *Manager) refreshSource(ctx context.Context, src annotated) {
	m.mu.Lock()
	_, have := m.cached[src.Name]
	prev := m.meta[src.Name]
	prevRules := -1
	if st := m.status[src.Name]; have && st != nil {
		prevRules = st.Rules
	}
	m.mu.Unlock()
	if !have {
		prev = cacheMeta{} // no body to fall back on: must download it
	}

	res, err := m.fetchWithRetry(ctx, src.URL, prev)
	if err == nil && !res.notModified {
		err = m.vet(src, res.body, prevRules)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureStatus(src)
	st.LastAttempt = time.Now()
	if err != nil {
		slog.Warn("list fetch failed", "list", src.Name, "url", src.URL, "err", err)
		st.LastError = err.Error()
		st.ConsecutiveFailures++
		st.failKind = kindOf(err)
		return
	}
	meta := cacheMeta{ETag: res.etag, LastModified: res.lastModified, FetchedAt: time.Now()}
	if res.notModified {
		if meta.ETag == "" {
			meta.ETag = prev.ETag
		}
		if meta.LastModified == "" {
			meta.LastModified = prev.LastModified
		}
		meta.SHA256 = prev.SHA256
		if m.disk != nil {
			if err := m.disk.touch(src.URL, meta); err != nil {
				slog.Debug("list cache touch", "list", src.Name, "err", err)
			}
		}
	} else {
		m.cached[src.Name] = res.body
		meta.SHA256 = bodyHash(res.body)
		if m.disk != nil {
			if err := m.disk.store(src.URL, res.body, meta); err != nil {
				slog.Warn("list cache write failed", "list", src.Name, "err", err)
			}
		}
	}
	m.meta[src.Name] = meta
	st.LastRefresh = meta.FetchedAt
	st.LastError = ""
	st.ConsecutiveFailures = 0
	st.failKind = failNone
}

// retryDelay is how long to wait before retrying the failing sources, or 0
// when none needs an early retry. Only transient failures retry early.
// Retired and unauthorized sources wait for the normal interval (a publisher
// who removed a list shouldn't be polled every minute for it), and so does a
// shrink rejection — its "same body three refreshes running" rule is meant
// to span real refreshes, not three retries in eight minutes.
func (m *Manager) retryDelay(interval time.Duration) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	worst := 0
	for _, st := range m.status {
		if st.Enabled && st.failKind == failTransient && st.ConsecutiveFailures > worst {
			worst = st.ConsecutiveFailures
		}
	}
	if worst == 0 {
		return 0
	}
	d := retrySchedule[min(worst, len(retrySchedule))-1]
	return min(d, interval)
}
