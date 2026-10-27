package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"time"

	"minos/internal/config"
	"minos/internal/threats"
)

// summaryDevice names a device for the weekly summary, with its page key.
type summaryDevice struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Lookups int    `json:"lookups,omitempty"`
}

// weekSummary is the dashboard's "This week": the last seven days told as
// a handful of facts, each with the previous week's figure where one means
// something.
type weekSummary struct {
	Start       time.Time `json:"start"`
	Lookups     int       `json:"lookups"`
	Blocked     int       `json:"blocked"`
	PrevLookups int       `json:"prev_lookups"`
	PrevBlocked int       `json:"prev_blocked"`

	// NewDevices are devices first seen this week (the newest five), of
	// NewDeviceCount in all.
	NewDevices     []summaryDevice `json:"new_devices"`
	NewDeviceCount int             `json:"new_device_count"`
	Busiest        *summaryDevice  `json:"busiest,omitempty"`

	// Security is what the threat-feed blocklists caught; SecurityLists
	// names the ones switched on (none means there is nothing to catch with).
	Security struct {
		Blocked int             `json:"blocked"`
		Devices []summaryDevice `json:"devices"`
		Lists   []string        `json:"lists"`
	} `json:"security"`
	SecurityLists []string `json:"security_lists"`

	// Tracking sums the privacy report's tracking companies.
	Tracking struct {
		Companies int              `json:"companies"`
		Allowed   int              `json:"allowed"`
		Blocked   int              `json:"blocked"`
		Top       []trackingLeader `json:"top"`
	} `json:"tracking"`

	SettingsChanges int                  `json:"settings_changes"`
	LastChange      *config.HistoryEntry `json:"last_change,omitempty"`
}

type trackingLeader struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Allowed int    `json:"allowed"`
}

// handleSummary answers the dashboard's weekly summary. Every part comes
// from data kept anyway — the rollups, the device registry, the settings
// history — so it costs about what the privacy report does.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), statsDeadline)
	defer cancel()
	now := time.Now()
	week := 7 * 24 * time.Hour
	since := now.Add(-week)
	out := weekSummary{Start: since, NewDevices: []summaryDevice{}}
	out.Security.Devices, out.Security.Lists, out.SecurityLists = []summaryDevice{}, []string{}, []string{}
	out.Tracking.Top = []trackingLeader{}
	fail := func(err error) { writeStatsErr(ctx, w, err, 7*24) }

	var err error
	if out.Lookups, out.Blocked, err = s.qlog.Totals(ctx, since); err != nil {
		fail(err)
		return
	}
	two, twoBlocked, err := s.qlog.Totals(ctx, since.Add(-week))
	if err != nil {
		fail(err)
		return
	}
	out.PrevLookups, out.PrevBlocked = max(0, two-out.Lookups), max(0, twoBlocked-out.Blocked)

	cfg := s.store.Get()
	devs := s.clients.Devices(cfg)
	type seen struct {
		d     summaryDevice
		first time.Time
	}
	var fresh []seen
	for _, d := range devs {
		if d.FirstSeen == nil || d.FirstSeen.Before(since) {
			continue
		}
		name, key := deviceOf(devs, d.IP)
		fresh = append(fresh, seen{summaryDevice{Key: key, Name: name, Address: d.IP}, *d.FirstSeen})
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].first.After(fresh[j].first) })
	out.NewDeviceCount = len(fresh)
	for i := range min(len(fresh), 5) {
		out.NewDevices = append(out.NewDevices, fresh[i].d)
	}

	if top, err := s.qlog.TopClients(ctx, since, 1); err != nil {
		fail(err)
		return
	} else if len(top) == 1 {
		name, key := deviceOf(devs, top[0].Client)
		out.Busiest = &summaryDevice{Key: key, Name: name, Address: top[0].Client, Lookups: top[0].Total}
	}

	security := map[string]bool{}
	for _, src := range cfg.Lists.Sources {
		if src.Enabled && !src.Audit && threats.ThreatFeed(src.Name, src.URL) {
			security[src.Name] = true
			out.SecurityLists = append(out.SecurityLists, src.Name)
		}
	}
	if len(security) > 0 {
		counts, err := s.qlog.BlocksByListClient(ctx, since)
		if err != nil {
			fail(err)
			return
		}
		devices := map[string]bool{}
		for _, c := range counts {
			if !security[c.List] {
				continue
			}
			out.Security.Blocked += c.Count
			if !slices.Contains(out.Security.Lists, c.List) {
				out.Security.Lists = append(out.Security.Lists, c.List)
			}
			name, key := deviceOf(devs, c.Client)
			if !devices[key] {
				devices[key] = true
				out.Security.Devices = append(out.Security.Devices, summaryDevice{Key: key, Name: name, Address: c.Client})
			}
		}
	}

	companies, _, err := s.companyReport(ctx, "", 7)
	if err != nil {
		fail(err)
		return
	}
	for _, c := range companies {
		if !c.Tracking {
			continue
		}
		out.Tracking.Companies++
		out.Tracking.Allowed += c.Allowed
		out.Tracking.Blocked += c.Blocked
		if c.Allowed > 0 && len(out.Tracking.Top) < 3 { // Already ordered by what got through
			out.Tracking.Top = append(out.Tracking.Top, trackingLeader{ID: c.ID, Name: c.Name, Allowed: c.Allowed})
		}
	}

	for _, h := range s.store.History() { // Newest first
		if h.Time.Before(since) {
			break
		}
		if len(h.Keys) == 0 {
			continue // The "before history began" starting point
		}
		out.SettingsChanges++
		if out.LastChange == nil {
			last := h
			out.LastChange = &last
		}
	}
	writeJSON(w, http.StatusOK, out)
}
