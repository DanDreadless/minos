package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"minos/internal/querylog"
)

func TestPrivacyReport(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()
	now := time.Now()
	for _, e := range []struct {
		name, verdict string
		n             int
	}{
		{"pixel.facebook.com", querylog.VerdictAllowed, 3},
		{"pagead2.googlesyndication.com", querylog.VerdictBlocked, 2},
		{"www.google.com", querylog.VerdictAllowed, 4}, // Google's own site: not tracking
		{"www.googletagmanager.com", querylog.VerdictAllowed, 1},
		{"example.org", querylog.VerdictAllowed, 5}, // No company
	} {
		for range e.n {
			s.qlog.Record(querylog.Entry{Time: now, Client: "10.0.0.9", QName: e.name, QType: "A", Verdict: e.verdict})
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(s.qlog.Recent(0)) < 15 {
		time.Sleep(5 * time.Millisecond)
	}

	rec := doJSON(t, r, "GET", "/api/privacy?days=7&client=10.0.0.9", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Attribution string        `json:"attribution"`
		Companies   []companyView `json:"companies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]companyView{}
	for _, c := range got.Companies {
		byID[c.ID] = c
	}
	if len(got.Companies) != 2 || got.Attribution == "" {
		t.Fatalf("report %+v", got)
	}
	if m := byID["meta"]; m.Name != "Meta Platforms, Inc." || !m.Tracking || m.Allowed != 3 || !m.Blockable || m.BlockedAll {
		t.Errorf("meta %+v", m)
	}
	// Google: syndication blocked (tracking); Tag Manager ("essential") and
	// its own search page are "other", not tracking.
	if g := byID["google"]; g.Allowed != 0 || g.Blocked != 2 || g.Other != 5 || !g.Tracking || len(g.Domains) != 3 ||
		!slices.Contains(g.Categories, "Advertising") || !slices.Contains(g.Categories, "Site essentials") ||
		!slices.Contains(g.Categories, "Sites and services") {
		t.Errorf("google %+v", g)
	}
	// Tracking companies come first, busiest-allowed first.
	if got.Companies[0].ID != "meta" {
		t.Errorf("order: %s first", got.Companies[0].ID)
	}

	// Block by company: recorded in the config, validated.
	if rec := doJSON(t, r, "POST", "/api/privacy/companies", `{"id":"meta","blocked":true}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("block: %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(store.Get().Blocking.Companies, []string{"meta"}) {
		t.Errorf("companies %v", store.Get().Blocking.Companies)
	}
	doJSON(t, r, "POST", "/api/privacy/companies", `{"id":"meta","blocked":true}`, nil) // Twice is once
	if len(store.Get().Blocking.Companies) != 1 {
		t.Error("blocking twice added the company twice")
	}
	if rec := doJSON(t, r, "POST", "/api/privacy/companies", `{"id":"no-such-co","blocked":true}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown company: %d", rec.Code)
	}
	doJSON(t, r, "POST", "/api/privacy/companies", `{"id":"meta","blocked":false}`, nil)
	if len(store.Get().Blocking.Companies) != 0 {
		t.Error("unblock did not remove the company")
	}
	if rec := doJSON(t, r, "GET", "/api/privacy?days=0", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("days=0: %d", rec.Code)
	}
}
