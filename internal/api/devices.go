package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"minos/internal/clients"
	"minos/internal/config"
)

// clientKey reads the {key} route param URL-decoded. chi leaves path segments
// percent-encoded, so a MAC's colons arrive as %3A from the browser's
// encodeURIComponent; an IP key is unaffected.
func clientKey(r *http.Request) string {
	key := chi.URLParam(r, "key")
	if dec, err := url.PathUnescape(key); err == nil {
		return dec
	}
	return key
}

// --- Devices ---

func (s *Server) handleGetClients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.clients.Devices(s.store.Get()))
}

type clientUpdate struct {
	Name    *string `json:"name"`
	MAC     *string `json:"mac"`
	Group   *string `json:"group"`
	Blocked *bool   `json:"blocked"`
	// IP is a last-known-address hint used only when creating a MAC-keyed
	// assignment for a device that isn't currently in the ARP table, so the
	// stored Client still carries a valid IP (config validation requires one).
	IP *string `json:"ip"`
}

// handleUpdateClient upserts the configured assignment for one device. The key
// is the device's MAC when it has one (so the assignment follows it across DHCP
// leases), else its IP. Setting name="", group="", blocked=false removes any
// meaning from the entry, but it stays until DELETEd — harmless either way.
func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	key := clientKey(r)
	var upd clientUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&upd); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of: name, mac, group, blocked")
		return
	}
	isMAC := keyIsMAC(key)
	err := s.store.Update(func(c *config.Config) error {
		// Pull out the device's existing assignment(s) — a MAC match or a
		// legacy IP-keyed entry for one of its current IPs (assignments made
		// before v0.10 were IP-keyed). Removing every match before re-adding a
		// single entry keeps one config.Client per device and self-heals any
		// duplicate.
		ips := s.deviceIPs(key, upd)
		cl, _ := takeDeviceClient(c, key, isMAC, ips)
		applyClientUpdate(&cl, upd)
		if isMAC {
			cl.MAC = clients.NormalizeMAC(key)
			if cur := s.clients.CurrentIP(key); cur != "" {
				cl.IP = cur
			} else if cl.IP == "" && upd.IP != nil {
				cl.IP = *upd.IP
			}
		} else {
			cl.IP = key
		}
		c.Clients = append(c.Clients, cl)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.clients.Devices(s.store.Get()))
}

// keyIsMAC reports whether a client route key is a MAC address (else an IP).
func keyIsMAC(key string) bool {
	_, err := net.ParseMAC(key)
	return err == nil
}

// deviceIPs is the set of addresses that identify the device addressed by key:
// every live IP currently carrying that MAC, plus the request's IP hint.
func (s *Server) deviceIPs(key string, upd clientUpdate) map[string]bool {
	set := map[string]bool{}
	if keyIsMAC(key) {
		for _, ip := range s.clients.IPsForMAC(key) {
			set[ip] = true
		}
	}
	if upd.IP != nil && *upd.IP != "" {
		set[*upd.IP] = true
	}
	return set
}

// clientIsDevice reports whether cl is an assignment for the device addressed
// by key. A MAC key matches a same-MAC client or a legacy IP-keyed client for
// one of the device's current IPs; an IP key matches only the IP-keyed client.
func clientIsDevice(cl config.Client, key string, isMAC bool, ips map[string]bool) bool {
	if isMAC {
		if cl.MAC != "" {
			return clients.NormalizeMAC(cl.MAC) == clients.NormalizeMAC(key)
		}
		return ips[cl.IP]
	}
	return cl.MAC == "" && cl.IP == key
}

// takeDeviceClient removes every assignment for the addressed device from c and
// returns the first one found (zero Client if none), so the caller can re-add a
// single reconciled entry.
func takeDeviceClient(c *config.Config, key string, isMAC bool, ips map[string]bool) (config.Client, bool) {
	var out config.Client
	found := false
	kept := c.Clients[:0]
	for _, cl := range c.Clients {
		if clientIsDevice(cl, key, isMAC, ips) {
			if !found {
				out, found = cl, true
			}
			continue
		}
		kept = append(kept, cl)
	}
	c.Clients = kept
	return out, found
}

func applyClientUpdate(cl *config.Client, upd clientUpdate) {
	if upd.Name != nil {
		cl.Name = *upd.Name
	}
	if upd.MAC != nil {
		cl.MAC = *upd.MAC
	}
	if upd.Group != nil {
		g := *upd.Group
		if g == "default" {
			g = ""
		}
		cl.Group = g
	}
	if upd.Blocked != nil {
		cl.Blocked = *upd.Blocked
	}
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	key := clientKey(r)
	isMAC := keyIsMAC(key)
	err := s.store.Update(func(c *config.Config) error {
		// Remove the device's assignment(s) — MAC match or a legacy IP-keyed
		// entry for one of its current IPs.
		if _, found := takeDeviceClient(c, key, isMAC, s.deviceIPs(key, clientUpdate{})); !found {
			return errNotFound
		}
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no configured client %q", key))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.clients.Devices(s.store.Get()))
}

// --- Groups ---

func (s *Server) handleGetGroups(w http.ResponseWriter, r *http.Request) {
	groups := s.store.Get().Groups
	if groups == nil {
		groups = []config.Group{}
	}
	writeJSON(w, http.StatusOK, groups)
}

type groupBody struct {
	Name            *string   `json:"name"`
	Mode            *string   `json:"mode"`
	Allowlist       *[]string `json:"allowlist"`
	Denylist        *[]string `json:"denylist"`
	Services        *[]string `json:"services"`
	AllowedServices *[]string `json:"allowed_services"`
	// Custom-service selections ride separate fields end to end (API and
	// YAML): mixing them into Services would put custom names in a key an
	// older binary validates against its compiled-in catalog.
	CustomServices        *[]string `json:"custom_services"`
	AllowedCustomServices *[]string `json:"allowed_custom_services"`
	SafeSearch            *bool     `json:"safe_search"`
	// Schedule distinguishes three states: absent (untouched), JSON null
	// (clear the schedule), or an object (set it).
	Schedule json.RawMessage `json:"schedule"`
}

// applySchedule interprets the raw schedule field onto a group.
func applySchedule(g *config.Group, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil // absent: leave as is
	}
	if string(raw) == "null" {
		g.Schedule = nil
		return nil
	}
	var sch config.Schedule
	if err := json.Unmarshal(raw, &sch); err != nil {
		return fmt.Errorf("schedule must be null or {days?, start, end}: %w", err)
	}
	g.Schedule = &sch
	return nil
}

func (s *Server) handleAddGroup(w http.ResponseWriter, r *http.Request) {
	var body groupBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"name\", \"mode\", \"allowlist\"?, \"denylist\"?}")
		return
	}
	if body.Name == nil || *body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	g := config.Group{Name: *body.Name, Mode: "filter"}
	if body.Mode != nil {
		g.Mode = *body.Mode
	}
	if body.Allowlist != nil {
		g.Allowlist = *body.Allowlist
	}
	if body.Denylist != nil {
		g.Denylist = *body.Denylist
	}
	if body.Services != nil {
		g.Services = *body.Services
	}
	if body.AllowedServices != nil {
		g.AllowedServices = *body.AllowedServices
	}
	if body.CustomServices != nil {
		g.CustomServices = *body.CustomServices
	}
	if body.AllowedCustomServices != nil {
		g.AllowedCustomServices = *body.AllowedCustomServices
	}
	if body.SafeSearch != nil {
		g.SafeSearch = *body.SafeSearch
	}
	if err := applySchedule(&g, body.Schedule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.store.Update(func(c *config.Config) error {
		for _, existing := range c.Groups {
			if existing.Name == g.Name {
				return fmt.Errorf("a group named %q already exists", g.Name)
			}
		}
		c.Groups = append(c.Groups, g)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.store.Get().Groups)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body groupBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of: mode, allowlist, denylist")
		return
	}
	err := s.store.Update(func(c *config.Config) error {
		for i := range c.Groups {
			g := &c.Groups[i]
			if g.Name != name {
				continue
			}
			if body.Mode != nil {
				g.Mode = *body.Mode
			}
			if body.Allowlist != nil {
				g.Allowlist = *body.Allowlist
			}
			if body.Denylist != nil {
				g.Denylist = *body.Denylist
			}
			if body.Services != nil {
				g.Services = *body.Services
			}
			if body.AllowedServices != nil {
				g.AllowedServices = *body.AllowedServices
			}
			if body.CustomServices != nil {
				g.CustomServices = *body.CustomServices
			}
			if body.AllowedCustomServices != nil {
				g.AllowedCustomServices = *body.AllowedCustomServices
			}
			if body.SafeSearch != nil {
				g.SafeSearch = *body.SafeSearch
			}
			if err := applySchedule(g, body.Schedule); err != nil {
				return err
			}
			return nil
		}
		return errNotFound
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no group named %q", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.store.Get().Groups)
}

// handleDeleteGroup removes a group; members fall back to the default rules.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	err := s.store.Update(func(c *config.Config) error {
		kept := c.Groups[:0]
		found := false
		for _, g := range c.Groups {
			if g.Name == name {
				found = true
				continue
			}
			kept = append(kept, g)
		}
		if !found {
			return errNotFound
		}
		c.Groups = kept
		for i := range c.Clients {
			if c.Clients[i].Group == name {
				c.Clients[i].Group = ""
			}
		}
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no group named %q", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.store.Get().Groups)
}
