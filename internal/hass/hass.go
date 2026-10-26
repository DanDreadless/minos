// Package hass announces Minos to Home Assistant over MQTT: discovery
// messages make a "Minos" device with switches, buttons and sensors appear
// in Home Assistant with no YAML, retained state topics keep them current,
// and command topics let Home Assistant pause blocking or block a device.
//
// Minos's state reaches this package through Hooks built in main, so it
// imports nothing but config and mqtt. Everything runs off the query path.
package hass

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"minos/internal/config"
	"minos/internal/mqtt"
)

// Snapshot is the state published to Home Assistant.
type Snapshot struct {
	Paused         bool
	QueriesTotal   uint64 // Since Minos started
	QueriesBlocked uint64
	Rules          int
	// UpstreamProblem: an upstream resolver is failing.
	UpstreamProblem bool
	Threats         int // Open threat findings
	Broken          int // Open "something may be broken" suggestions
	// LatestVersion is the newest release, when the update check is on.
	LatestVersion string
	Devices       []Device
}

// Device is a named device that gets a "Block" switch.
type Device struct {
	Key     string // The device's MAC, or its IP when it has none
	Name    string
	Blocked bool
}

// Hooks connect the bridge to the rest of Minos.
type Hooks struct {
	Snapshot func() Snapshot
	// Pause pauses blocking; zero means until resumed.
	Pause  func(time.Duration)
	Resume func()
	// SetDeviceBlocked changes a device's block (a recorded settings change).
	SetDeviceBlocked func(key string, blocked bool) error
	// UIURL is the address of Minos's web interface, for the device page link.
	UIURL func() string
}

// Status is the connection state shown in Settings.
type Status struct {
	Enabled   bool       `json:"enabled"`
	Connected bool       `json:"connected"`
	Error     string     `json:"error,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

// statePeriod is how often state is republished (it is also published
// straight after every command and settings change).
const statePeriod = 30 * time.Second

// Bridge keeps the MQTT connection in line with the settings.
type Bridge struct {
	store   *config.Store
	hooks   Hooks
	version string
	poke    chan struct{}

	mu     sync.Mutex
	status Status
	conn   *connection
}

// New returns a bridge; Run starts it.
func New(store *config.Store, hooks Hooks, version string) *Bridge {
	return &Bridge{store: store, hooks: hooks, version: version, poke: make(chan struct{}, 1)}
}

// Status reports the connection state.
func (b *Bridge) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

// Changed asks for state to be published now (after a pause from the web
// interface, say), instead of at the next period.
func (b *Bridge) Changed() {
	select {
	case b.poke <- struct{}{}:
	default:
	}
}

// Run follows the settings until ctx ends: it connects, reconnects with new
// settings, and disconnects when the integration is switched off.
func (b *Bridge) Run(ctx context.Context) {
	b.store.OnChange(func(*config.Config) { b.Changed() })
	tick := time.NewTicker(statePeriod)
	defer tick.Stop()
	var cur config.HomeAssistantConfig
	var stop func(retire bool)
	for {
		want := b.store.Get().HomeAssistant
		if want != cur {
			if stop != nil {
				// Switched off: remove Minos from Home Assistant. Changed
				// settings: retire the old entities only if the topics move.
				od, ot := cur.Prefixes()
				nd, nt := want.Prefixes()
				stop(!want.Enabled || od != nd || ot != nt || cur.Broker != want.Broker)
				stop = nil
			}
			cur = want
			b.setStatus(Status{Enabled: want.Enabled})
			if want.Enabled {
				stop = b.connect(ctx, want)
			}
		}
		if c := b.current(); c != nil {
			c.publishAll(false)
		}
		select {
		case <-ctx.Done():
			if stop != nil {
				stop(false)
			}
			return
		case <-tick.C:
		case <-b.poke:
		}
	}
}

func (b *Bridge) current() *connection {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn
}

func (b *Bridge) setStatus(s Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = s
}

// connect starts a client for cfg and returns the function that stops it.
func (b *Bridge) connect(ctx context.Context, cfg config.HomeAssistantConfig) func(retire bool) {
	disc, topic := cfg.Prefixes()
	c := &connection{
		b:         b,
		disc:      disc,
		topic:     topic,
		node:      nodeID(topic),
		switches:  cfg.DeviceSwitches,
		published: map[string]bool{},
	}
	client, err := mqtt.New(mqtt.Options{
		Broker:    cfg.Broker,
		ClientID:  "minos-" + c.node,
		Username:  cfg.Username,
		Password:  cfg.Password,
		Will:      &mqtt.Message{Topic: c.availability(), Payload: []byte("offline"), Retain: true},
		Subscribe: []string{topic + "/+/set", topic + "/device/+/set", disc + "/status", disc + "/+/" + c.node + "/+/config"},
		OnConnect: func() { c.publishAll(true) },
		OnMessage: c.handle,
		OnState: func(up bool, err error) {
			s := Status{Enabled: true, Connected: up}
			now := time.Now()
			s.Since = &now
			if err != nil {
				s.Error = err.Error()
				slog.Warn("home assistant: MQTT connection failed", "broker", cfg.Broker, "err", err)
			} else if up {
				slog.Info("home assistant: connected to the MQTT broker", "broker", cfg.Broker)
			}
			b.setStatus(s)
		},
	})
	if err != nil {
		b.setStatus(Status{Enabled: true, Error: err.Error()})
		return func(bool) {}
	}
	c.client = client
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		client.Run(cctx)
		close(done)
	}()
	b.mu.Lock()
	b.conn = c
	b.mu.Unlock()
	return func(retire bool) {
		if retire {
			c.retire()
		}
		b.mu.Lock()
		b.conn = nil
		b.mu.Unlock()
		cancel()
		<-done
	}
}

// connection is one configured connection: its topics and what it has
// announced.
type connection struct {
	b        *Bridge
	client   *mqtt.Client
	disc     string
	topic    string
	node     string
	switches bool

	mu        sync.Mutex
	published map[string]bool // Discovery topics announced on this connection
	devices   map[string]string
	lastState map[string]string // Topic → payload last sent
}

func (c *connection) availability() string { return c.topic + "/availability" }

// nodeID turns the topic prefix into an id safe for discovery topics and
// unique IDs.
func nodeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// entity is one discovery message.
type entity struct {
	component string // switch, button, sensor, binary_sensor, update
	id        string
	payload   map[string]any
}

// entities lists what Minos announces for snapshot s.
func (c *connection) entities(s Snapshot) []entity {
	st := c.topic + "/state"
	es := []entity{
		{"switch", "blocking", map[string]any{
			"name": "Blocking", "icon": "mdi:shield-check",
			"state_topic": st, "value_template": "{{ value_json.blocking }}",
			"command_topic": c.topic + "/blocking/set",
		}},
		{"button", "pause_5m", map[string]any{
			"name": "Pause blocking for 5 minutes", "icon": "mdi:pause-circle-outline",
			"command_topic": c.topic + "/pause/set", "payload_press": "5",
		}},
		{"button", "pause_1h", map[string]any{
			"name": "Pause blocking for 1 hour", "icon": "mdi:pause-circle-outline",
			"command_topic": c.topic + "/pause/set", "payload_press": "60",
		}},
		{"sensor", "queries", map[string]any{
			"name": "Queries", "icon": "mdi:dns", "unit_of_measurement": "queries",
			"state_class": "total_increasing", "state_topic": st, "value_template": "{{ value_json.queries }}",
		}},
		{"sensor", "blocked", map[string]any{
			"name": "Blocked queries", "icon": "mdi:shield-off", "unit_of_measurement": "queries",
			"state_class": "total_increasing", "state_topic": st, "value_template": "{{ value_json.blocked }}",
		}},
		{"sensor", "blocked_share", map[string]any{
			"name": "Share blocked", "icon": "mdi:percent", "unit_of_measurement": "%",
			"state_class": "measurement", "suggested_display_precision": 1,
			"state_topic": st, "value_template": "{{ value_json.blocked_share }}",
		}},
		{"sensor", "rules", map[string]any{
			"name": "Blocklist rules", "icon": "mdi:format-list-bulleted", "entity_category": "diagnostic",
			"state_class": "measurement", "state_topic": st, "value_template": "{{ value_json.rules }}",
		}},
		{"binary_sensor", "upstream_problem", map[string]any{
			"name": "Upstream resolver problem", "device_class": "problem",
			"state_topic": st, "value_template": "{{ value_json.upstream_problem }}",
		}},
		{"binary_sensor", "threat", map[string]any{
			"name": "Threat suspected", "device_class": "problem", "icon": "mdi:biohazard",
			"state_topic": st, "value_template": "{{ value_json.threat }}",
		}},
		{"binary_sensor", "broken", map[string]any{
			"name": "Blocking may have broken something", "device_class": "problem", "icon": "mdi:wrench",
			"state_topic": st, "value_template": "{{ value_json.broken }}",
		}},
	}
	if s.LatestVersion != "" {
		es = append(es, entity{"update", "update", map[string]any{
			"name": "Update", "title": "Minos", "entity_category": "diagnostic",
			"state_topic": c.topic + "/update",
		}})
	}
	if c.switches {
		for _, d := range s.Devices {
			id := "device_" + nodeID(d.Key)
			es = append(es, entity{"switch", id, map[string]any{
				"name": "Block " + d.Name, "icon": "mdi:lan-disconnect",
				"state_topic":   c.topic + "/device/" + nodeID(d.Key) + "/state",
				"command_topic": c.topic + "/device/" + nodeID(d.Key) + "/set",
			}})
		}
	}
	return es
}

func (c *connection) discoveryTopic(e entity) string {
	return c.disc + "/" + e.component + "/" + c.node + "/" + e.id + "/config"
}

// publishAll announces every entity and publishes the current state. With
// force, everything is sent again (a new connection, or Home Assistant just
// restarted); otherwise only what changed.
func (c *connection) publishAll(force bool) {
	if !c.client.Connected() {
		return
	}
	s := c.b.hooks.Snapshot()
	c.mu.Lock()
	defer c.mu.Unlock()
	if force {
		c.published = map[string]bool{}
		c.lastState = nil
	}
	if err := c.send(c.availability(), "online", force); err != nil {
		return
	}
	device := map[string]any{
		"identifiers":  []string{"minos_" + c.node},
		"name":         "Minos",
		"manufacturer": "Minos",
		"model":        "DNS filter",
		"sw_version":   c.b.version,
	}
	if c.b.hooks.UIURL != nil {
		if u := c.b.hooks.UIURL(); u != "" {
			device["configuration_url"] = u
		}
	}
	origin := map[string]any{"name": "Minos", "sw_version": c.b.version, "support_url": "https://github.com/DanDreadless/minos"}
	want := map[string]bool{}
	for _, e := range c.entities(s) {
		t := c.discoveryTopic(e)
		want[t] = true
		if c.published[t] {
			continue
		}
		p := e.payload
		p["unique_id"] = "minos_" + c.node + "_" + e.id
		p["availability_topic"] = c.availability()
		p["device"] = device
		p["origin"] = origin
		body, _ := json.Marshal(p)
		if err := c.client.Publish(mqtt.Message{Topic: t, Payload: body, Retain: true}); err != nil {
			return
		}
		c.published[t] = true
	}
	// Entities that went away (a device renamed or forgotten, the update
	// check switched off): an empty retained config removes them.
	for t := range c.published {
		if !want[t] {
			_ = c.client.Publish(mqtt.Message{Topic: t, Retain: true})
			delete(c.published, t)
		}
	}

	c.devices = map[string]string{}
	for _, d := range s.Devices {
		c.devices[nodeID(d.Key)] = d.Key
		if c.switches {
			_ = c.send(c.topic+"/device/"+nodeID(d.Key)+"/state", onOff(d.Blocked), force)
		}
	}
	share := 0.0
	if s.QueriesTotal > 0 {
		share = math.Round(1000*float64(s.QueriesBlocked)/float64(s.QueriesTotal)) / 10
	}
	state, _ := json.Marshal(map[string]any{
		"blocking":         onOff(!s.Paused),
		"queries":          s.QueriesTotal,
		"blocked":          s.QueriesBlocked,
		"blocked_share":    share,
		"rules":            s.Rules,
		"upstream_problem": onOff(s.UpstreamProblem),
		"threat":           onOff(s.Threats > 0),
		"broken":           onOff(s.Broken > 0),
	})
	_ = c.send(c.topic+"/state", string(state), force)
	if s.LatestVersion != "" {
		u, _ := json.Marshal(map[string]string{
			"installed_version": c.b.version,
			"latest_version":    s.LatestVersion,
			"release_url":       "https://github.com/DanDreadless/minos/releases/tag/v" + strings.TrimPrefix(s.LatestVersion, "v"),
		})
		_ = c.send(c.topic+"/update", string(u), force)
	}
}

// send publishes a retained state message unless it is unchanged.
func (c *connection) send(topic, payload string, force bool) error {
	if !force && c.lastState[topic] == payload {
		return nil
	}
	if err := c.client.Publish(mqtt.Message{Topic: topic, Payload: []byte(payload), Retain: true}); err != nil {
		return err
	}
	if c.lastState == nil {
		c.lastState = map[string]string{}
	}
	c.lastState[topic] = payload
	return nil
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// handle acts on an incoming message. It runs on the client's reading
// goroutine, so anything slow goes to its own.
func (c *connection) handle(m mqtt.Message) {
	payload := strings.TrimSpace(string(m.Payload))
	switch {
	case m.Topic == c.disc+"/status":
		// Home Assistant restarted: announce everything again. A retained
		// "online" arrives on every connect, which has just announced anyway.
		if payload == "online" && !m.Retain {
			go c.publishAll(true)
		}
	case strings.HasPrefix(m.Topic, c.disc+"/") && strings.HasSuffix(m.Topic, "/config"):
		// A retained announcement from an earlier run: remove it if this run
		// no longer has that entity (a device deleted while Minos was off).
		if m.Retain && len(m.Payload) > 0 {
			go c.dropStale(m.Topic)
		}
	case m.Retain:
		// A command left retained on the broker by mistake must not be
		// replayed on every reconnect (pausing blocking each time).
		slog.Warn("home assistant: ignored a retained command; publish commands without retain", "topic", m.Topic)
	case m.Topic == c.topic+"/blocking/set":
		switch strings.ToUpper(payload) {
		case "ON":
			c.b.hooks.Resume()
		case "OFF":
			c.b.hooks.Pause(0)
		}
		c.b.Changed()
	case m.Topic == c.topic+"/pause/set":
		minutes, err := strconv.Atoi(payload)
		if err != nil || minutes <= 0 || minutes > 7*24*60 {
			slog.Warn("home assistant: pause needs a number of minutes", "payload", payload)
			return
		}
		c.b.hooks.Pause(time.Duration(minutes) * time.Minute)
		c.b.Changed()
	case strings.HasPrefix(m.Topic, c.topic+"/device/") && strings.HasSuffix(m.Topic, "/set"):
		id := strings.TrimSuffix(strings.TrimPrefix(m.Topic, c.topic+"/device/"), "/set")
		c.mu.Lock()
		key, ok := c.devices[id]
		c.mu.Unlock()
		block := strings.ToUpper(payload)
		if !ok || (block != "ON" && block != "OFF") {
			return
		}
		go func() {
			if err := c.b.hooks.SetDeviceBlocked(key, block == "ON"); err != nil {
				slog.Warn("home assistant: device block failed", "device", key, "err", err)
			}
			c.b.Changed()
		}()
	}
}

// dropStale clears a retained announcement this connection did not make.
func (c *connection) dropStale(topic string) {
	// Give the announcements made on connect a moment to land first.
	time.Sleep(2 * time.Second)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.published[topic] {
		_ = c.client.Publish(mqtt.Message{Topic: topic, Retain: true})
	}
}

// retire removes everything this connection announced, so Home Assistant
// forgets the entities (the integration was switched off or moved).
func (c *connection) retire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for t := range c.published {
		_ = c.client.Publish(mqtt.Message{Topic: t, Retain: true})
	}
	for t := range c.lastState {
		_ = c.client.Publish(mqtt.Message{Topic: t, Retain: true})
	}
	c.published = map[string]bool{}
}

// String describes the bridge for logs.
func (s Status) String() string {
	switch {
	case !s.Enabled:
		return "off"
	case s.Connected:
		return "connected"
	case s.Error != "":
		return fmt.Sprintf("not connected: %s", s.Error)
	default:
		return "connecting"
	}
}
