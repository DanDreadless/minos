package filter

import (
	"sync/atomic"
	"time"
)

// Engine is the live judgment surface used by the DNS hot path.
// Reads are lock-free: the current Matcher sits behind an atomic pointer
// and pause state is an atomic timestamp.
type Engine struct {
	matcher     atomic.Pointer[Matcher]
	pausedUntil atomic.Int64 // unix nanos; 0 = not paused
}

func NewEngine() *Engine {
	e := &Engine{}
	e.matcher.Store(EmptyMatcher())
	return e
}

// Swap installs a freshly built Matcher. Safe to call while serving.
func (e *Engine) Swap(m *Matcher) { e.matcher.Store(m) }

// Current returns the active Matcher (for stats; do not mutate).
func (e *Engine) Current() *Matcher { return e.matcher.Load() }

// Match judges qname (normalized: lowercase, no trailing dot).
func (e *Engine) Match(qname string) Result {
	if e.pausedNow() {
		return Result{}
	}
	return e.matcher.Load().Match(qname)
}

// Pause disables blocking for d (a "recess"). d <= 0 pauses indefinitely.
func (e *Engine) Pause(d time.Duration) {
	if d <= 0 {
		e.pausedUntil.Store(-1)
		return
	}
	e.pausedUntil.Store(time.Now().Add(d).UnixNano())
}

// Resume re-enables blocking immediately.
func (e *Engine) Resume() { e.pausedUntil.Store(0) }

// Paused reports whether blocking is paused and, for timed pauses, until when.
func (e *Engine) Paused() (bool, time.Time) {
	v := e.pausedUntil.Load()
	switch {
	case v == 0:
		return false, time.Time{}
	case v < 0:
		return true, time.Time{}
	default:
		until := time.Unix(0, v)
		if time.Now().After(until) {
			return false, time.Time{}
		}
		return true, until
	}
}

func (e *Engine) pausedNow() bool {
	v := e.pausedUntil.Load()
	if v == 0 {
		return false
	}
	if v < 0 {
		return true
	}
	return time.Now().UnixNano() < v
}
