package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"

	"minos/internal/clients"
	"minos/internal/config"
)

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
	key := chi.URLParam(r, "key")
	var upd clientUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&upd); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of: name, mac, group, blocked")
		return
	}
	isMAC := keyIsMAC(key)
	err := s.store.Update(func(c *config.Config) error {
		for i := range c.Clients {
			if !clientMatchesKey(c.Clients[i], key, isMAC) {
				continue
			}
			applyClientUpdate(&c.Clients[i], upd)
			if isMAC { // keep the last-known IP fresh
				if cur := s.clients.CurrentIP(key); cur != "" {
					c.Clients[i].IP = cur
				}
			}
			return nil
		}
		fresh := s.freshClient(key, isMAC, upd)
		applyClientUpdate(&fresh, upd)
		c.Clients = append(c.Clients, fresh)
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

// clientMatchesKey reports whether a configured client is the one addressed by
// key. A MAC key matches only MAC-keyed clients; an IP key matches only
// IP-keyed ones, so a bare IP never hijacks a device tracked by MAC.
func clientMatchesKey(cl config.Client, key string, isMAC bool) bool {
	if isMAC {
		return cl.MAC != "" && clients.NormalizeMAC(cl.MAC) == clients.NormalizeMAC(key)
	}
	return cl.MAC == "" && cl.IP == key
}

// freshClient builds a new assignment for key. A MAC key resolves its current
// IP from the registry, falling back to the body's IP hint, so the stored
// Client always carries a (valid) last-known address.
func (s *Server) freshClient(key string, isMAC bool, upd clientUpdate) config.Client {
	if !isMAC {
		return config.Client{IP: key}
	}
	cl := config.Client{MAC: clients.NormalizeMAC(key)}
	if cl.IP = s.clients.CurrentIP(key); cl.IP == "" && upd.IP != nil {
		cl.IP = *upd.IP
	}
	return cl
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
	key := chi.URLParam(r, "key")
	isMAC := keyIsMAC(key)
	err := s.store.Update(func(c *config.Config) error {
		kept := c.Clients[:0]
		found := false
		for _, cl := range c.Clients {
			if clientMatchesKey(cl, key, isMAC) {
				found = true
				continue
			}
			kept = append(kept, cl)
		}
		if !found {
			return errNotFound
		}
		c.Clients = kept
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
	Name       *string   `json:"name"`
	Mode       *string   `json:"mode"`
	Allowlist  *[]string `json:"allowlist"`
	Denylist   *[]string `json:"denylist"`
	Services   *[]string `json:"services"`
	SafeSearch *bool     `json:"safe_search"`
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
