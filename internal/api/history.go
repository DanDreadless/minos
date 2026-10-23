package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"minos/internal/filter"
	"minos/internal/querylog"
)

// historyFilter parses the query-log filter parameters shared by
// /querylog/history and /querylog/export. Every value is validated rather
// than passed through, so a typo is a 400 instead of silently no rows.
func historyFilter(q url.Values) (querylog.HistoryFilter, error) {
	verdict := q.Get("verdict")
	switch verdict {
	case "all":
		verdict = ""
	case "", querylog.VerdictBlocked, querylog.VerdictAllowed:
	default:
		return querylog.HistoryFilter{}, errors.New("verdict must be blocked, allowed, or all")
	}
	f := querylog.HistoryFilter{Search: q.Get("q"), Verdict: verdict, List: q.Get("list")}
	// dnssec narrows to one validation outcome — the Tribunal's counter
	// drill-down.
	if d := q.Get("dnssec"); d != "" {
		switch d {
		case querylog.DNSSECSecure, querylog.DNSSECInsecure,
			querylog.DNSSECBogus, querylog.DNSSECIndeterminate:
			f.DNSSEC = d
		default:
			return f, errors.New("dnssec must be secure, insecure, bogus, or indeterminate")
		}
	}
	// would_block narrows to entries an audit-mode list flagged.
	switch v := q.Get("would_block"); v {
	case "", "false":
	case "true", "1":
		f.WouldBlock = true
	default:
		return f, errors.New("would_block must be true or false")
	}
	// client is an exact-match filter (comma-separated for a multi-IP device),
	// distinct from the free-text q substring. Capped so the IN list stays sane.
	f.Clients = splitList(q.Get("client"), 32)
	if d := q.Get("domain"); d != "" {
		if f.Domain = filter.NormalizeDomain(d); f.Domain == "" {
			return f, fmt.Errorf("domain %q is not a domain name", d)
		}
	}
	f.Exclude = splitList(q.Get("exclude"), 8)
	if t := strings.ToUpper(strings.TrimSpace(q.Get("qtype"))); t != "" {
		if len(t) > 16 || strings.IndexFunc(t, notQTypeRune) >= 0 {
			return f, fmt.Errorf("qtype %q is not a record type", t)
		}
		f.QType = t
	}
	if u := strings.TrimSpace(q.Get("upstream")); u != "" {
		if len(u) > 512 {
			return f, errors.New("upstream is too long")
		}
		f.Upstream = u
	}
	if v := q.Get("since"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ms < 0 {
			return f, errors.New("since must be unix milliseconds")
		}
		f.Since = time.UnixMilli(ms)
	}
	return f, nil
}

// notQTypeRune: record types are letters and digits (A, AAAA, HTTPS,
// TYPE65534).
func notQTypeRune(r rune) bool {
	return (r < 'A' || r > 'Z') && (r < '0' || r > '9')
}

// splitList splits a comma-separated parameter, dropping blanks, keeping at
// most max entries.
func splitList(v string, max int) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
		if len(out) >= max {
			break
		}
	}
	return out
}

var exportHeader = []string{
	"time", "client", "domain", "type", "verdict", "list", "rule",
	"upstream", "duration_ms", "would_block_list", "would_block_rule", "dnssec",
}

// handleQueryLogExport streams the rows matching the history filter, newest
// first, as CSV (default) or JSON — up to querylog.ExportMaxRows, or `limit`.
// A failure after the first byte aborts the connection, so a download that
// stopped early reads as failed rather than as a complete, shorter file.
func (s *Server) handleQueryLogExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		writeError(w, http.StatusBadRequest, "format must be csv or json")
		return
	}
	limit := querylog.ExportMaxRows
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > querylog.ExportMaxRows {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("limit must be 1-%d", querylog.ExportMaxRows))
			return
		}
		limit = n
	}
	f, err := historyFilter(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := "minos-querylog-" + time.Now().Format("20060102-1504") + "." + format
	started := false
	start := func() {
		if started {
			return
		}
		started = true
		ct := "text/csv; charset=utf-8"
		if format == "json" {
			ct = "application/json; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
	}

	var emit func(querylog.Entry) error
	var finish func() error
	if format == "csv" {
		cw := csv.NewWriter(w)
		emit = func(e querylog.Entry) error {
			if !started {
				start()
				if err := cw.Write(exportHeader); err != nil {
					return err
				}
			}
			return cw.Write([]string{
				e.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00"), cell(e.Client), cell(e.QName),
				e.QType, e.Verdict, cell(e.List), cell(e.Rule), cell(e.Upstream),
				strconv.FormatFloat(e.DurationMs, 'f', -1, 64),
				cell(e.AuditList), cell(e.AuditRule), e.DNSSEC,
			})
		}
		finish = func() error {
			if !started {
				start()
				if err := cw.Write(exportHeader); err != nil {
					return err
				}
			}
			cw.Flush()
			return cw.Error()
		}
	} else {
		enc := json.NewEncoder(w)
		first := true
		emit = func(e querylog.Entry) error {
			start()
			sep := ","
			if first {
				sep, first = "[", false
			}
			if _, err := w.Write([]byte(sep)); err != nil {
				return err
			}
			return enc.Encode(e)
		}
		finish = func() error {
			start()
			end := "]\n"
			if first {
				end = "[]\n"
			}
			_, err := w.Write([]byte(end))
			return err
		}
	}

	err = s.qlog.ExportHistory(r.Context(), f, limit, emit)
	if err == nil {
		err = finish()
	}
	if err == nil {
		return
	}
	if !started {
		status := http.StatusInternalServerError
		if errors.Is(err, querylog.ErrSearchTimeout) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err.Error())
		return
	}
	slog.Warn("query log export cut short", "err", err)
	panic(http.ErrAbortHandler)
}

// cell defuses spreadsheet formulas: a cell a spreadsheet would evaluate
// (=, +, -, @ and the control characters that also trigger it) gets a
// leading apostrophe. Domain names never start with one, but rules (`@@`)
// and attacker-chosen names reaching the log can.
func cell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
