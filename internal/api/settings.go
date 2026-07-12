package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"minos/internal/config"
)

// configView is the settings payload the UI reads. The API token is never
// echoed back — only whether one is set. Listen addresses are included but
// marked read-only in the UI: they are the only settings needing a restart.
type configView struct {
	DNS struct {
		Listen          string               `json:"listen"`
		Upstreams       []config.Upstream    `json:"upstreams"`
		BlockTTL        uint32               `json:"block_ttl"`
		Cache           config.CacheConfig   `json:"cache"`
		LocalRecords    []config.LocalRecord `json:"local_records"`
		LocalTTL        uint32               `json:"local_ttl"`
		Routes          []config.Route       `json:"routes"`
		AllowFirefoxDoH bool                 `json:"allow_firefox_doh"`
	} `json:"dns"`
	Blocking struct {
		Mode                    string `json:"mode"`
		SafeSearch              bool   `json:"safe_search"`
		BlockICloudPrivateRelay bool   `json:"block_icloud_private_relay"`
	} `json:"blocking"`
	Lists struct {
		RefreshInterval string `json:"refresh_interval"`
	} `json:"lists"`
	QueryLog struct {
		Ephemeral     bool   `json:"ephemeral"`
		DBPath        string `json:"db_path"`
		RingSize      int    `json:"ring_size"`
		RetentionDays int    `json:"retention_days"`
	} `json:"querylog"`
	API struct {
		Listen   string `json:"listen"`
		TokenSet bool   `json:"token_set"`
	} `json:"api"`
	UpdateCheck   bool `json:"update_check"`
	Notifications struct {
		WebhookURL   string `json:"webhook_url"`
		NtfyURL      string `json:"ntfy_url"`
		NtfyTokenSet bool   `json:"ntfy_token_set"`
		Digest       string `json:"digest"`
		DigestTime   string `json:"digest_time"`
		DigestDay    string `json:"digest_day"`
	} `json:"notifications"`
	Discovery config.DiscoveryConfig `json:"discovery"`
}

func viewOf(c *config.Config) configView {
	var v configView
	v.DNS.Listen = c.DNS.Listen
	v.DNS.Upstreams = c.DNS.Upstreams
	v.DNS.BlockTTL = c.DNS.BlockTTL
	v.DNS.Cache = c.DNS.Cache
	v.DNS.LocalRecords = c.DNS.LocalRecords
	if v.DNS.LocalRecords == nil {
		v.DNS.LocalRecords = []config.LocalRecord{}
	}
	v.DNS.LocalTTL = c.DNS.LocalTTL
	v.DNS.Routes = c.DNS.Routes
	if v.DNS.Routes == nil {
		v.DNS.Routes = []config.Route{}
	}
	v.DNS.AllowFirefoxDoH = c.DNS.AllowFirefoxDoH
	v.Blocking.Mode = c.Blocking.Mode
	v.Blocking.SafeSearch = c.Blocking.SafeSearch
	v.Blocking.BlockICloudPrivateRelay = c.Blocking.BlockICloudPrivateRelay
	v.Lists.RefreshInterval = c.Lists.RefreshInterval.Std().String()
	v.QueryLog.Ephemeral = c.QueryLog.Ephemeral
	v.QueryLog.DBPath = c.QueryLog.DBPath
	v.QueryLog.RingSize = c.QueryLog.RingSize
	v.QueryLog.RetentionDays = c.QueryLog.RetentionDays
	v.API.Listen = c.API.Listen
	v.API.TokenSet = c.API.Token != ""
	v.UpdateCheck = c.UpdateCheck
	v.Notifications.WebhookURL = c.Notifications.WebhookURL
	v.Notifications.NtfyURL = c.Notifications.NtfyURL
	v.Notifications.NtfyTokenSet = c.Notifications.NtfyToken != ""
	v.Notifications.Digest = c.Notifications.Digest
	if v.Notifications.Digest == "" {
		v.Notifications.Digest = "off"
	}
	// Echo the resolved schedule so the UI shows the effective values.
	v.Notifications.DigestTime = c.Notifications.DigestTime
	if v.Notifications.DigestTime == "" {
		v.Notifications.DigestTime = "09:00"
	}
	v.Notifications.DigestDay = c.Notifications.DigestDay
	if v.Notifications.DigestDay == "" {
		v.Notifications.DigestDay = "monday"
	}
	v.Discovery = c.Discovery
	return v
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, viewOf(s.store.Get()))
}

// settingsUpdate uses pointers so omitted fields stay untouched. Only
// runtime-applicable settings are writable here; listen addresses and
// query-log storage locations stay file-only.
type settingsUpdate struct {
	DNS *struct {
		Upstreams *[]config.Upstream `json:"upstreams"`
		BlockTTL  *uint32            `json:"block_ttl"`
		Cache     *struct {
			Enabled    *bool   `json:"enabled"`
			MaxEntries *int    `json:"max_entries"`
			MinTTL     *uint32 `json:"min_ttl"`
			MaxTTL     *uint32 `json:"max_ttl"`
			ServeStale *bool   `json:"serve_stale"`
		} `json:"cache"`
		LocalRecords    *[]config.LocalRecord `json:"local_records"`
		LocalTTL        *uint32               `json:"local_ttl"`
		Routes          *[]config.Route       `json:"routes"`
		AllowFirefoxDoH *bool                 `json:"allow_firefox_doh"`
	} `json:"dns"`
	Blocking *struct {
		Mode                    *string `json:"mode"`
		SafeSearch              *bool   `json:"safe_search"`
		BlockICloudPrivateRelay *bool   `json:"block_icloud_private_relay"`
	} `json:"blocking"`
	Lists *struct {
		RefreshInterval *string `json:"refresh_interval"`
	} `json:"lists"`
	QueryLog *struct {
		RingSize      *int `json:"ring_size"`
		RetentionDays *int `json:"retention_days"`
	} `json:"querylog"`
	API *struct {
		Token *string `json:"token"`
	} `json:"api"`
	UpdateCheck   *bool `json:"update_check"`
	Notifications *struct {
		WebhookURL *string `json:"webhook_url"`
		NtfyURL    *string `json:"ntfy_url"`
		NtfyToken  *string `json:"ntfy_token"`
		Digest     *string `json:"digest"`
		DigestTime *string `json:"digest_time"`
		DigestDay  *string `json:"digest_day"`
	} `json:"notifications"`
	Discovery *struct {
		SSDP *bool `json:"ssdp"`
	} `json:"discovery"`
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	var upd settingsUpdate
	if err := dec.Decode(&upd); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid settings body: %v", err))
		return
	}
	var refreshInterval time.Duration
	if upd.Lists != nil && upd.Lists.RefreshInterval != nil {
		var err error
		refreshInterval, err = time.ParseDuration(*upd.Lists.RefreshInterval)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("invalid refresh_interval %q", *upd.Lists.RefreshInterval))
			return
		}
	}
	err := s.store.Update(func(c *config.Config) error {
		if upd.DNS != nil {
			if upd.DNS.Upstreams != nil {
				c.DNS.Upstreams = *upd.DNS.Upstreams
			}
			if upd.DNS.BlockTTL != nil {
				c.DNS.BlockTTL = *upd.DNS.BlockTTL
			}
			if upd.DNS.Cache != nil {
				if upd.DNS.Cache.Enabled != nil {
					c.DNS.Cache.Enabled = *upd.DNS.Cache.Enabled
				}
				if upd.DNS.Cache.MaxEntries != nil {
					c.DNS.Cache.MaxEntries = *upd.DNS.Cache.MaxEntries
				}
				if upd.DNS.Cache.MinTTL != nil {
					c.DNS.Cache.MinTTL = *upd.DNS.Cache.MinTTL
				}
				if upd.DNS.Cache.MaxTTL != nil {
					c.DNS.Cache.MaxTTL = *upd.DNS.Cache.MaxTTL
				}
				if upd.DNS.Cache.ServeStale != nil {
					c.DNS.Cache.ServeStale = *upd.DNS.Cache.ServeStale
				}
			}
			if upd.DNS.LocalRecords != nil {
				c.DNS.LocalRecords = *upd.DNS.LocalRecords
			}
			if upd.DNS.LocalTTL != nil {
				c.DNS.LocalTTL = *upd.DNS.LocalTTL
			}
			if upd.DNS.Routes != nil {
				c.DNS.Routes = *upd.DNS.Routes
			}
			if upd.DNS.AllowFirefoxDoH != nil {
				c.DNS.AllowFirefoxDoH = *upd.DNS.AllowFirefoxDoH
			}
		}
		if upd.Blocking != nil {
			if upd.Blocking.Mode != nil {
				c.Blocking.Mode = *upd.Blocking.Mode
			}
			if upd.Blocking.SafeSearch != nil {
				c.Blocking.SafeSearch = *upd.Blocking.SafeSearch
			}
			if upd.Blocking.BlockICloudPrivateRelay != nil {
				c.Blocking.BlockICloudPrivateRelay = *upd.Blocking.BlockICloudPrivateRelay
			}
		}
		if upd.Lists != nil && upd.Lists.RefreshInterval != nil {
			c.Lists.RefreshInterval = config.Duration(refreshInterval)
		}
		if upd.QueryLog != nil {
			if upd.QueryLog.RingSize != nil {
				c.QueryLog.RingSize = *upd.QueryLog.RingSize
			}
			if upd.QueryLog.RetentionDays != nil {
				c.QueryLog.RetentionDays = *upd.QueryLog.RetentionDays
			}
		}
		if upd.API != nil && upd.API.Token != nil {
			c.API.Token = *upd.API.Token
		}
		if upd.UpdateCheck != nil {
			c.UpdateCheck = *upd.UpdateCheck
		}
		if upd.Notifications != nil {
			if upd.Notifications.WebhookURL != nil {
				c.Notifications.WebhookURL = *upd.Notifications.WebhookURL
			}
			if upd.Notifications.NtfyURL != nil {
				c.Notifications.NtfyURL = *upd.Notifications.NtfyURL
			}
			if upd.Notifications.NtfyToken != nil {
				c.Notifications.NtfyToken = *upd.Notifications.NtfyToken
			}
			if upd.Notifications.Digest != nil {
				c.Notifications.Digest = *upd.Notifications.Digest
			}
			if upd.Notifications.DigestTime != nil {
				c.Notifications.DigestTime = *upd.Notifications.DigestTime
			}
			if upd.Notifications.DigestDay != nil {
				c.Notifications.DigestDay = *upd.Notifications.DigestDay
			}
		}
		if upd.Discovery != nil && upd.Discovery.SSDP != nil {
			c.Discovery.SSDP = *upd.Discovery.SSDP
		}
		return nil
	})
	if err != nil {
		// Validation failures are the caller's mistake; nothing was applied.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOf(s.store.Get()))
}

// handleExportConfig downloads the live config as YAML — a backup the user
// can restore by dropping it in as minos.yaml. Includes the API token, so it
// is only reachable through the authenticated API like everything else.
func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	data, err := yaml.Marshal(s.store.Get())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="minos.yaml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// --- Blocklist source management ---

type listSourceBody struct {
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Format  *string `json:"format"`
	Action  *string `json:"action"` // "block" (default) or "allow"
	Enabled *bool   `json:"enabled"`
	// Audit compiles the list for "would block" logging without enforcement.
	Audit *bool `json:"audit"`
}

func (s *Server) handleAddList(w http.ResponseWriter, r *http.Request) {
	var body listSourceBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON: {\"name\", \"url\", \"format\", \"action\", \"enabled\"}")
		return
	}
	if body.Name == nil || body.URL == nil || *body.Name == "" {
		writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}
	src := config.ListSource{Name: *body.Name, URL: *body.URL, Format: "hosts", Enabled: true}
	if body.Format != nil {
		src.Format = *body.Format
	}
	if body.Enabled != nil {
		src.Enabled = *body.Enabled
	}
	if body.Audit != nil {
		src.Audit = *body.Audit
	}
	allow := false
	if body.Action != nil {
		switch *body.Action {
		case "allow":
			allow = true
		case "", "block":
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("action: must be block or allow, got %q", *body.Action))
			return
		}
	}
	err := s.store.Update(func(c *config.Config) error {
		for _, existing := range append(append([]config.ListSource{}, c.Lists.Sources...), c.Lists.AllowSources...) {
			if existing.Name == src.Name {
				return fmt.Errorf("a list named %q already exists", src.Name)
			}
		}
		if allow {
			c.Lists.AllowSources = append(c.Lists.AllowSources, src)
		} else {
			c.Lists.Sources = append(c.Lists.Sources, src)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Fetch the new list now so the caller sees rule counts on return.
	s.lists.EnsureFetched(r.Context())
	writeJSON(w, http.StatusCreated, s.lists.Status())
}

func (s *Server) handleUpdateList(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body listSourceBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with any of: url, format, action, enabled")
		return
	}
	urlChanged := false
	err := s.store.Update(func(c *config.Config) error {
		slices := [2]*[]config.ListSource{&c.Lists.Sources, &c.Lists.AllowSources}
		for si, slice := range slices {
			for i := range *slice {
				src := &(*slice)[i]
				if src.Name != name {
					continue
				}
				if body.URL != nil && *body.URL != src.URL {
					src.URL = *body.URL
					urlChanged = true
				}
				if body.Format != nil {
					src.Format = *body.Format
				}
				if body.Enabled != nil {
					src.Enabled = *body.Enabled
				}
				if body.Audit != nil {
					src.Audit = *body.Audit
				}
				if body.Action != nil {
					// Moving between block and allow means moving the
					// source between the two config slices.
					var wantAllow bool
					switch *body.Action {
					case "allow":
						wantAllow = true
					case "", "block":
					default:
						return fmt.Errorf("action: must be block or allow, got %q", *body.Action)
					}
					if isAllow := si == 1; wantAllow != isAllow {
						moved := *src
						*slice = append((*slice)[:i], (*slice)[i+1:]...)
						other := slices[1-si]
						*other = append(*other, moved)
					}
				}
				return nil
			}
		}
		return errNotFound
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no list named %q", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if urlChanged {
		// The cached body belongs to the old URL; refetch from the new one.
		s.lists.Forget(name)
		s.lists.EnsureFetched(r.Context())
	}
	writeJSON(w, http.StatusOK, s.lists.Status())
}

var errNotFound = errors.New("not found")

func (s *Server) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	err := s.store.Update(func(c *config.Config) error {
		found := false
		for _, slice := range [2]*[]config.ListSource{&c.Lists.Sources, &c.Lists.AllowSources} {
			kept := (*slice)[:0]
			for _, src := range *slice {
				if src.Name == name {
					found = true
					continue
				}
				kept = append(kept, src)
			}
			*slice = kept
		}
		if !found {
			return errNotFound
		}
		return nil
	})
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no list named %q", name))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.lists.Forget(name)
	writeJSON(w, http.StatusOK, s.lists.Status())
}
