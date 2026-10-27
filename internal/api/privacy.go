package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"minos/internal/config"
	"minos/internal/trackers"
)

// companyView is one company in the privacy report.
type companyView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Categories are the plain-word categories of its domains seen.
	Categories []string `json:"categories"`
	// Tracking: at least one name looked up is one of its tracking names.
	Tracking bool `json:"tracking"`
	// Allowed and Blocked count lookups of its tracking names in the
	// window; Other its other names (its own sites and services).
	Allowed int `json:"allowed"`
	Blocked int `json:"blocked"`
	Other   int `json:"other"`
	// Domains are the busiest of its names looked up, with counts.
	Domains []companyDomain `json:"domains"`
	// Devices that looked it up (network report only).
	Devices []companyDevice `json:"devices,omitempty"`
	// Blockable: "block this company" has domains to block; BlockedAll:
	// it is already blocked that way.
	Blockable  bool `json:"blockable"`
	BlockedAll bool `json:"blocked_all"`
}

type companyDomain struct {
	Name     string `json:"name"`
	Tracker  string `json:"tracker"`
	Category string `json:"category"`
	Tracking bool   `json:"tracking"`
	Allowed  int    `json:"allowed"`
	Blocked  int    `json:"blocked"`
}

// categoryOf labels a looked-up name. The source files a company's own
// sites under its ad category (google.com: advertising), so a name that is
// not one of the tracking names is called what it is to the user.
func categoryOf(t trackers.Tracker) string {
	if !t.Tracking {
		switch t.Category {
		case "advertising", "pornvertising", "site_analytics", "mobile_analytics", "telemetry":
			return "Sites and services"
		}
	}
	return trackers.CategoryLabel(t.Category)
}

type companyDevice struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// handlePrivacy is the privacy report: which companies the network (or one
// device, ?client=ip,ip) looked up in the last ?days (1–90, whole UTC days),
// what got through and what was blocked.
func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 90 {
			writeError(w, http.StatusBadRequest, "days must be 1-90")
			return
		}
		days = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), statsDeadline)
	defer cancel()
	companies, start, err := s.companyReport(ctx, r.URL.Query().Get("client"), days)
	if err != nil {
		writeStatsErr(ctx, w, err, days*24)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Days        int           `json:"days"`
		WindowStart time.Time     `json:"window_start"`
		Attribution string        `json:"attribution"`
		Companies   []companyView `json:"companies"`
	}{days, start, trackers.Attribution, companies})
}

// companyReport aggregates the companies the network — or, with client
// (comma-separated addresses), one device — looked up over the last days
// whole UTC days: tracking companies first, by what got through. Shared by
// the Privacy page and the weekly summary.
func (s *Server) companyReport(ctx context.Context, client string, days int) ([]companyView, time.Time, error) {
	cfg := s.store.Get()
	devs := s.clients.Devices(cfg)
	var clients []string
	single := false
	if client != "" {
		single = true
		for _, c := range strings.Split(client, ",") {
			if c = strings.TrimSpace(c); c != "" && len(clients) < 64 {
				clients = append(clients, c)
			}
		}
	} else {
		for _, d := range devs {
			clients = append(clients, d.IPs...)
			if len(d.IPs) == 0 && d.IP != "" {
				clients = append(clients, d.IP)
			}
		}
	}
	slices.Sort(clients)
	clients = slices.Compact(clients)
	names, start, err := s.qlog.ClientNames(ctx, clients, time.Now().AddDate(0, 0, -(days-1)))
	if err != nil {
		return nil, start, err
	}

	blocked := map[string]bool{}
	for _, id := range cfg.Blocking.Companies {
		blocked[id] = true
	}
	type acc struct {
		view    companyView
		cats    map[string]bool
		domains map[string]*companyDomain
		devices map[string]string
	}
	byID := map[string]*acc{}
	known := map[string]trackers.Tracker{} // Lookups repeat across devices
	for _, n := range names {
		t, ok := known[n.QName]
		if !ok {
			t, ok = trackers.Lookup(n.QName)
			if !ok {
				known[n.QName] = trackers.Tracker{}
				continue
			}
			known[n.QName] = t
		}
		if t.CompanyID == "" {
			continue
		}
		a := byID[t.CompanyID]
		if a == nil {
			a = &acc{view: companyView{ID: t.CompanyID, Name: trackers.CompanyName(t.CompanyID),
				Blockable: trackers.Exists(t.CompanyID), BlockedAll: blocked[t.CompanyID]},
				cats: map[string]bool{}, domains: map[string]*companyDomain{}, devices: map[string]string{}}
			byID[t.CompanyID] = a
		}
		a.cats[categoryOf(t)] = true
		a.view.Tracking = a.view.Tracking || t.Tracking
		d := a.domains[n.QName]
		if d == nil {
			d = &companyDomain{Name: n.QName, Tracker: t.Name, Category: categoryOf(t), Tracking: t.Tracking}
			a.domains[n.QName] = d
		}
		blockedNow := n.Verdict == "blocked"
		switch {
		case !t.Tracking:
			a.view.Other += n.Count
		case blockedNow:
			a.view.Blocked += n.Count
		default:
			a.view.Allowed += n.Count
		}
		if blockedNow {
			d.Blocked += n.Count
		} else {
			d.Allowed += n.Count
		}
		if !single {
			name, key := deviceOf(devs, n.Client)
			if name == "" {
				name = n.Client
			}
			a.devices[key] = name
		}
	}

	companies := []companyView{}
	for _, a := range byID {
		v := a.view
		for c := range a.cats {
			v.Categories = append(v.Categories, c)
		}
		sort.Strings(v.Categories)
		for _, d := range a.domains {
			v.Domains = append(v.Domains, *d)
		}
		sort.Slice(v.Domains, func(i, j int) bool {
			return v.Domains[i].Allowed+v.Domains[i].Blocked > v.Domains[j].Allowed+v.Domains[j].Blocked
		})
		if len(v.Domains) > 10 {
			v.Domains = v.Domains[:10]
		}
		for k, name := range a.devices {
			v.Devices = append(v.Devices, companyDevice{Key: k, Name: name})
		}
		sort.Slice(v.Devices, func(i, j int) bool { return v.Devices[i].Name < v.Devices[j].Name })
		companies = append(companies, v)
	}
	// Tracking companies first, then by what got through.
	sort.Slice(companies, func(i, j int) bool {
		a, b := companies[i], companies[j]
		if a.Tracking != b.Tracking {
			return a.Tracking
		}
		if a.Allowed != b.Allowed {
			return a.Allowed > b.Allowed
		}
		if a.Blocked != b.Blocked {
			return a.Blocked > b.Blocked
		}
		return a.Other > b.Other
	})
	return companies, start, nil
}

// handleBlockCompany blocks or unblocks a company's advertising, analytics
// and telemetry domains for everyone: {"id": "google", "blocked": true}.
func (s *Server) handleBlockCompany(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Blocked bool   `json:"blocked"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		writeError(w, http.StatusBadRequest, `body must be JSON: {"id": "google", "blocked": true}`)
		return
	}
	err := s.update(r, func(c *config.Config) error {
		c.Blocking.Companies = slices.DeleteFunc(c.Blocking.Companies, func(id string) bool { return id == body.ID })
		if body.Blocked {
			c.Blocking.Companies = append(c.Blocking.Companies, body.ID)
			sort.Strings(c.Blocking.Companies)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": body.ID, "blocked": body.Blocked, "domains": len(trackers.Domains(body.ID))})
}
