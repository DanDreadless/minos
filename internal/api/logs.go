package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"minos/internal/applog"
)

// SetAppLog enables the application-log endpoints (the UI's Logs page).
func (s *Server) SetAppLog(ring *applog.Ring, level *slog.LevelVar) {
	s.appLog, s.logLevel = ring, level
}

type logsResponse struct {
	// Level is the level being recorded now: records below it are not
	// kept anywhere, so the page can say why debug lines are missing.
	Level    string          `json:"level"`
	Records  []applog.Record `json:"records"`
	AlertSeq uint64          `json:"alert_seq"`
}

// logFilter is the optional narrowing shared by the list and the download.
type logFilter struct {
	min   slog.Level
	q     string
	after uint64
}

func parseLogFilter(r *http.Request) (logFilter, error) {
	q := r.URL.Query()
	f := logFilter{min: slog.LevelDebug, q: strings.ToLower(strings.TrimSpace(q.Get("q")))}
	if v := q.Get("level"); v != "" {
		l, err := applog.ParseLevel(v)
		if err != nil {
			return f, err
		}
		f.min = l
	}
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return f, fmt.Errorf("after must be a sequence number")
		}
		f.after = n
	}
	return f, nil
}

func (f logFilter) match(rec applog.Record) bool {
	if l, _ := applog.ParseLevel(rec.Level); l < f.min {
		return false
	}
	if f.q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(rec.Message), f.q) {
		return true
	}
	for _, a := range rec.Attrs {
		if strings.Contains(strings.ToLower(a.Key+"="+a.Value), f.q) {
			return true
		}
	}
	return false
}

func (s *Server) filteredLogs(f logFilter) []applog.Record {
	out := []applog.Record{}
	for _, rec := range s.appLog.Records(f.after) {
		if f.match(rec) {
			out = append(out, rec)
		}
	}
	return out
}

// handleLogs serves the held application-log records, oldest first, with
// Seq > after (for polling) at or above level, matching q.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.appLog == nil {
		writeError(w, http.StatusServiceUnavailable, "application logs are not available")
		return
	}
	f, err := parseLogFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logsResponse{
		Level:    applog.LevelName(s.logLevel.Level()),
		Records:  s.filteredLogs(f),
		AlertSeq: s.appLog.AlertSeq(),
	})
}

// handleLogsStream pushes each new record as a JSON frame.
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	if s.appLog == nil {
		writeError(w, http.StatusServiceUnavailable, "application logs are not available")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusInternalError, "stream closed") }()
	ch, cancel := s.appLog.Subscribe()
	defer cancel()
	ctx := r.Context()
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		case rec := <-ch:
			if err := wsjson.Write(ctx, conn, rec); err != nil {
				return
			}
		}
	}
}

// handleLogsDownload serves the held records as plain text, one per line
// in the journal's key=value shape — the thing to attach to a bug report.
func (s *Server) handleLogsDownload(w http.ResponseWriter, r *http.Request) {
	if s.appLog == nil {
		writeError(w, http.StatusServiceUnavailable, "application logs are not available")
		return
	}
	f, err := parseLogFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := "minos-log-" + time.Now().Format("20060102-1504") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	fmt.Fprintf(&b, "# Minos %s — application log, %s (secrets redacted)\n", s.version, time.Now().Format(time.RFC3339))
	for _, rec := range s.filteredLogs(f) {
		fmt.Fprintf(&b, "%s %-5s %s", rec.Time.Format("2006-01-02T15:04:05.000Z07:00"),
			strings.ToUpper(rec.Level), rec.Message)
		for _, a := range rec.Attrs {
			v := a.Value
			if v == "" || strings.ContainsAny(v, " \t\"=") {
				v = strconv.Quote(v)
			}
			fmt.Fprintf(&b, " %s=%s", a.Key, v)
		}
		b.WriteByte('\n')
	}
	_, _ = w.Write([]byte(b.String()))
}
