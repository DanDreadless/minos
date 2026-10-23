package applog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newLogger(size int) (*slog.Logger, *Ring, *slog.LevelVar, *bytes.Buffer) {
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	ring := NewRing(size)
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv})
	return slog.New(NewHandler(inner, ring, lv)), ring, lv, &buf
}

func TestTeesToBothAndFlattensAttrs(t *testing.T) {
	log, ring, _, buf := newLogger(10)
	log.With("component", "lists").WithGroup("fetch").Info("list refreshed", "name", "StevenBlack", "rules", 71493,
		slog.Group("http", "status", 200))
	if !strings.Contains(buf.String(), "list refreshed") {
		t.Errorf("stderr handler missed the record: %q", buf)
	}
	recs := ring.Records(0)
	if len(recs) != 1 {
		t.Fatalf("ring holds %d records", len(recs))
	}
	r := recs[0]
	want := []Attr{{"component", "lists"}, {"fetch.name", "StevenBlack"}, {"fetch.rules", "71493"}, {"fetch.http.status", "200"}}
	if r.Level != "info" || r.Message != "list refreshed" || len(r.Attrs) != len(want) {
		t.Fatalf("record = %+v", r)
	}
	for i := range want {
		if r.Attrs[i] != want[i] {
			t.Errorf("attr %d = %v, want %v", i, r.Attrs[i], want[i])
		}
	}
}

func TestLevelChangesLive(t *testing.T) {
	log, ring, lv, buf := newLogger(10)
	log.Debug("hidden")
	lv.Set(slog.LevelDebug)
	log.Debug("shown")
	lv.Set(slog.LevelWarn)
	log.Info("hidden too")
	if got := ring.Records(0); len(got) != 1 || got[0].Message != "shown" {
		t.Errorf("ring = %+v", got)
	}
	if strings.Contains(buf.String(), "hidden") {
		t.Errorf("stderr got a filtered record: %q", buf)
	}
}

func TestRingWrapsAndPagesBySeq(t *testing.T) {
	log, ring, _, _ := newLogger(3)
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		log.Info(m)
	}
	recs := ring.Records(0)
	if len(recs) != 3 || recs[0].Message != "c" || recs[2].Message != "e" || recs[2].Seq != 5 {
		t.Fatalf("ring = %+v", recs)
	}
	if after := ring.Records(4); len(after) != 1 || after[0].Message != "e" {
		t.Errorf("records after 4 = %+v", after)
	}
	if ring.AlertSeq() != 0 {
		t.Error("an alert without any warning")
	}
	log.Warn("careful")
	log.Info("fine")
	if ring.AlertSeq() != 6 {
		t.Errorf("alert seq = %d, want 6", ring.AlertSeq())
	}
}

func TestSecretsNeverReachTheRing(t *testing.T) {
	log, ring, _, _ := newLogger(10)
	log.Info("config", "api_token", "abc123", "ntfy.Token", "t0k", "password", "hunter2", "key", "k",
		"auth_key", "ak", "url", "https://user:pa55@example.com/hosts?token=s3cret&format=hosts",
		"err", `Get "https://feed.example/list?apikey=zzz": timeout`, "name", "keyed-list", "rules", 5)
	got := map[string]string{}
	for _, a := range ring.Records(0)[0].Attrs {
		got[a.Key] = a.Value
	}
	for _, k := range []string{"api_token", "ntfy.Token", "password", "key", "auth_key"} {
		if got[k] != redacted {
			t.Errorf("%s = %q, want redacted", k, got[k])
		}
	}
	for k, v := range got {
		for _, secret := range []string{"abc123", "hunter2", "pa55", "s3cret", "zzz"} {
			if strings.Contains(v, secret) {
				t.Errorf("%s = %q leaks %q", k, v, secret)
			}
		}
	}
	if got["name"] != "keyed-list" || got["rules"] != "5" || !strings.Contains(got["url"], "example.com/hosts") {
		t.Errorf("ordinary values changed: %v", got)
	}
}

func TestSubscribeAndSlowViewers(t *testing.T) {
	log, ring, _, _ := newLogger(10)
	ch, cancel := ring.Subscribe()
	log.Error("boom", "err", "x")
	select {
	case r := <-ch:
		if r.Message != "boom" || r.Level != "error" {
			t.Errorf("streamed %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing streamed")
	}
	// A viewer that never reads must not block logging.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subBuffer*3; i++ {
			log.Info("flood")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logging blocked on a slow subscriber")
	}
	cancel()
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warning": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("ParseLevel accepted nonsense")
	}
}

// The journal gets the same redaction as the ring, and an error string
// keeps its punctuation around a redacted URL.
func TestJournalRedactedToo(t *testing.T) {
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	ring := NewRing(10)
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv, ReplaceAttr: ReplaceAttr})
	log := slog.New(NewHandler(inner, ring, lv))
	log.Warn("list fetch failed", "api_token", "abc", "err", `Get "https://x.example/l?token=s3cret": refused`)
	if out := buf.String(); strings.Contains(out, "s3cret") || strings.Contains(out, "abc") {
		t.Errorf("journal leaks a secret: %s", out)
	}
	got := ring.Records(0)[0].Attrs[1].Value
	if want := `Get "https://x.example/l?token=xxxxx": refused`; got != want {
		t.Errorf("err = %q, want %q", got, want)
	}
}
