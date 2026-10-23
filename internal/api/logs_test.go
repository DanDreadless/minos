package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"minos/internal/applog"
)

func TestAppLogEndpoints(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()
	if rec := doJSON(t, r, "GET", "/api/logs", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without a ring: status = %d", rec.Code)
	}

	lv := new(slog.LevelVar)
	ring := applog.NewRing(100)
	log := slog.New(applog.NewHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lv}), ring, lv))
	s.SetAppLog(ring, lv)
	log.Info("matcher rebuilt", "rules", 72929)
	log.Warn("list fetch failed", "name", "URLhaus", "url", "https://feed.example/list?token=s3cret")
	log.Error("query log flush failed", "err", "disk full")

	get := func(path string) logsResponse {
		t.Helper()
		rec := doJSON(t, r, "GET", path, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var out logsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := get("/api/logs")
	if all.Level != "info" || len(all.Records) != 3 || all.AlertSeq != 3 {
		t.Fatalf("all = %+v", all)
	}
	if got := get("/api/logs?level=warn"); len(got.Records) != 2 {
		t.Errorf("warn+ = %d records", len(got.Records))
	}
	if got := get("/api/logs?q=urlhaus"); len(got.Records) != 1 || got.Records[0].Level != "warn" {
		t.Errorf("search on an attribute value = %+v", got.Records)
	}
	if got := get("/api/logs?after=2"); len(got.Records) != 1 || got.Records[0].Seq != 3 {
		t.Errorf("after=2 = %+v", got.Records)
	}
	for _, bad := range []string{"/api/logs?level=loud", "/api/logs?after=x"} {
		if rec := doJSON(t, r, "GET", bad, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", bad, rec.Code)
		}
	}

	rec := doJSON(t, r, "GET", "/api/logs/download", "", nil)
	body := rec.Body.String()
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "minos-log-") ||
		!strings.Contains(body, "WARN  list fetch failed name=URLhaus") || strings.Contains(body, "s3cret") {
		t.Errorf("download:\n%s", body)
	}

	var st statusResponse
	_ = json.Unmarshal(doJSON(t, r, "GET", "/api/status", "", nil).Body.Bytes(), &st)
	if st.LogAlertSeq != 3 {
		t.Errorf("status log_alert_seq = %d, want 3", st.LogAlertSeq)
	}

	if rec := doJSON(t, r, "PUT", "/api/config", `{"log_level":"debug"}`, nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"log_level":"debug"`) {
		t.Errorf("PUT log_level: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "PUT", "/api/config", `{"log_level":"chatty"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad log_level: status = %d", rec.Code)
	}
}
