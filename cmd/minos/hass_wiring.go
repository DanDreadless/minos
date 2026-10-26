package main

import (
	"errors"
	"strings"

	"minos/internal/breakage"
	"minos/internal/config"
	"minos/internal/dnsproxy"
	"minos/internal/filter"
	"minos/internal/hass"
	"minos/internal/notify"
	"minos/internal/querylog"
	"minos/internal/threats"
	"minos/internal/updates"
)

// homeAssistantActor labels Home Assistant's changes in the settings history.
const homeAssistantActor = "Home Assistant (MQTT)"

// newHomeAssistant builds the MQTT bridge's view of Minos: everything it
// reads is an atomic load or a small in-memory list, so the 30-second state
// refresh costs nothing measurable.
func newHomeAssistant(store *config.Store, engine *filter.Engine, qlog *querylog.Log, proxy *dnsproxy.Server,
	checker *updates.Checker, watcher *threats.Detector, broken *breakage.Detector,
) *hass.Bridge {
	return hass.New(store, hass.Hooks{
		Snapshot: func() hass.Snapshot {
			cfg := store.Get()
			total, blocked, _ := qlog.Stats()
			paused, _ := engine.Paused()
			s := hass.Snapshot{
				Paused:         paused,
				QueriesTotal:   total,
				QueriesBlocked: blocked,
				Rules:          engine.Current().Rules(),
				Threats:        len(watcher.Findings()),
				Broken:         len(broken.Suggestions()),
			}
			configured := map[string]bool{}
			for _, u := range cfg.DNS.Upstreams {
				configured[u.Address] = true
			}
			for _, st := range proxy.UpstreamStats() {
				if configured[st.Name] && st.State == dnsproxy.UpstreamFailing {
					s.UpstreamProblem = true
				}
			}
			if cfg.UpdateCheck {
				s.LatestVersion, _ = checker.Latest()
			}
			for _, c := range cfg.Clients {
				if c.Name == "" {
					continue
				}
				key := c.MAC
				if key == "" {
					key = c.IP
				}
				s.Devices = append(s.Devices, hass.Device{Key: key, Name: c.Name, Blocked: c.Blocked})
			}
			return s
		},
		Pause:  engine.Pause,
		Resume: engine.Resume,
		SetDeviceBlocked: func(key string, block bool) error {
			return store.UpdateAs(homeAssistantActor, "", func(c *config.Config) error {
				for i := range c.Clients {
					if c.Clients[i].MAC == key || c.Clients[i].MAC == "" && c.Clients[i].IP == key {
						c.Clients[i].Blocked = block
						return nil
					}
				}
				return errors.New("no such device")
			})
		},
		UIURL: func() string {
			cfg := store.Get()
			if u := strings.TrimSuffix(cfg.Notifications.UIURL, "/"); u != "" {
				return u
			}
			return notify.DefaultUIBase(cfg.API.Listen)
		},
	}, version)
}
