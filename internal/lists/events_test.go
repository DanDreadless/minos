package lists

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

type eventLog struct {
	mu     sync.Mutex
	states []ListState
}

func (l *eventLog) record(_ string, state ListState, _ string) {
	l.mu.Lock()
	l.states = append(l.states, state)
	l.mu.Unlock()
}

func (l *eventLog) got() []ListState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ListState(nil), l.states...)
}

// A retired list is reported once — not on every refresh — and its
// recovery once.
func TestListEventsFireOncePerTransition(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\n")
	m, _ := testManager(t, srv.URL, "")
	var log eventLog
	m.OnListEvent(log.record)
	m.Refresh(context.Background())

	srv.set("", http.StatusGone)
	m.Refresh(context.Background())
	m.Refresh(context.Background())
	srv.set("ads.example\n", 0)
	m.Refresh(context.Background())
	m.Refresh(context.Background())

	got := log.got()
	if len(got) != 2 || got[0] != ListRetired || got[1] != ListOK {
		t.Errorf("events = %v, want [retired ok]", got)
	}
}

// A Pi that boots before its network fails its first attempts; that must
// not page anyone unless the failure survives several retries.
func TestTransientFailureNotifiesOnlyAfterRetries(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\n")
	srv.set("", http.StatusServiceUnavailable)
	m, _ := testManager(t, srv.URL, "")
	var log eventLog
	m.OnListEvent(log.record)

	for i := 1; i < notifyAfterFailures; i++ {
		m.Refresh(context.Background())
	}
	if got := log.got(); len(got) != 0 {
		t.Fatalf("events after %d boot failures = %v, want none", notifyAfterFailures-1, got)
	}
	m.Refresh(context.Background())
	if got := log.got(); len(got) != 1 || got[0] != ListUnavailable {
		t.Errorf("events = %v, want [unavailable] once the failure persisted", got)
	}
}

// Recovering from a failure nobody was told about is not news.
func TestNoRecoveryEventWithoutAFailureEvent(t *testing.T) {
	fastRetries(t)
	srv := newListServer(t, "ads.example\n")
	srv.set("", http.StatusServiceUnavailable)
	m, _ := testManager(t, srv.URL, "")
	var log eventLog
	m.OnListEvent(log.record)
	m.Refresh(context.Background())
	srv.set("ads.example\n", 0)
	m.Refresh(context.Background())
	if got := log.got(); len(got) != 0 {
		t.Errorf("events = %v, want none for an unreported blip", got)
	}
}
