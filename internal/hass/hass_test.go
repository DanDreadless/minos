package hass

import (
	"testing"
	"time"

	"minos/internal/mqtt"
)

type calls struct {
	paused  []time.Duration
	resumed int
	blocked chan [2]string
}

func testConn(t *testing.T, switches bool) (*connection, *calls) {
	t.Helper()
	k := &calls{blocked: make(chan [2]string, 4)}
	b := &Bridge{poke: make(chan struct{}, 1), version: "1.2.3", hooks: Hooks{
		Pause:  func(d time.Duration) { k.paused = append(k.paused, d) },
		Resume: func() { k.resumed++ },
		SetDeviceBlocked: func(key string, blocked bool) error {
			v := "OFF"
			if blocked {
				v = "ON"
			}
			k.blocked <- [2]string{key, v}
			return nil
		},
	}}
	c := &connection{b: b, disc: "homeassistant", topic: "minos", node: "minos", switches: switches,
		devices: map[string]string{"aa_bb_cc_dd_ee_ff": "aa:bb:cc:dd:ee:ff"}}
	return c, k
}

func TestCommands(t *testing.T) {
	c, k := testConn(t, true)
	msg := func(topic, payload string, retain bool) {
		c.handle(mqtt.Message{Topic: topic, Payload: []byte(payload), Retain: retain})
	}
	msg("minos/blocking/set", "OFF", false)
	msg("minos/blocking/set", "on", false)
	msg("minos/pause/set", "5", false)
	msg("minos/pause/set", "soon", false)    // Not a number: ignored
	msg("minos/pause/set", "-3", false)      // Ignored
	msg("minos/blocking/set", "OFF", true)   // Retained: never replayed
	msg("minos/elsewhere/set", "OFF", false) // Unknown: ignored
	if len(k.paused) != 2 || k.paused[0] != 0 || k.paused[1] != 5*time.Minute || k.resumed != 1 {
		t.Errorf("paused %v resumed %d", k.paused, k.resumed)
	}
	select {
	case <-c.b.poke:
	default:
		t.Error("a command did not ask for state to be republished")
	}

	msg("minos/device/aa_bb_cc_dd_ee_ff/set", "ON", false)
	select {
	case got := <-k.blocked:
		if got != [2]string{"aa:bb:cc:dd:ee:ff", "ON"} {
			t.Errorf("device block %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("device block not applied")
	}
	msg("minos/device/unknown/set", "ON", false)
	msg("minos/device/aa_bb_cc_dd_ee_ff/set", "maybe", false)
	select {
	case got := <-k.blocked:
		t.Errorf("unexpected device change %v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestEntities(t *testing.T) {
	c, _ := testConn(t, true)
	s := Snapshot{Devices: []Device{{Key: "aa:bb:cc:dd:ee:ff", Name: "Kids tablet"}}}
	ids := map[string]string{}
	for _, e := range c.entities(s) {
		ids[e.id] = e.component
		if e.payload["name"] == nil {
			t.Errorf("%s has no name", e.id)
		}
		if topic, ok := e.payload["command_topic"].(string); ok && mqtt.ValidTopic(topic) != nil {
			t.Errorf("%s: bad command topic %q", e.id, topic)
		}
	}
	if ids["blocking"] != "switch" || ids["device_aa_bb_cc_dd_ee_ff"] != "switch" || ids["queries"] != "sensor" {
		t.Errorf("entities %v", ids)
	}
	if _, ok := ids["update"]; ok {
		t.Error("update entity without an update check")
	}
	if got := c.discoveryTopic(entity{component: "switch", id: "blocking"}); got != "homeassistant/switch/minos/blocking/config" {
		t.Errorf("discovery topic %q", got)
	}

	s.LatestVersion = "1.3.0"
	c.switches = false
	ids = map[string]string{}
	for _, e := range c.entities(s) {
		ids[e.id] = e.component
	}
	if ids["update"] != "update" {
		t.Error("no update entity with a known latest version")
	}
	if _, ok := ids["device_aa_bb_cc_dd_ee_ff"]; ok {
		t.Error("device switch with device switches off")
	}
}

func TestNodeID(t *testing.T) {
	for in, want := range map[string]string{"minos": "minos", "Minos-2": "minos_2", "fd00::1": "fd00__1", "aa:BB:cc": "aa_bb_cc"} {
		if got := nodeID(in); got != want {
			t.Errorf("nodeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusString(t *testing.T) {
	for _, tc := range []struct {
		s    Status
		want string
	}{
		{Status{}, "off"},
		{Status{Enabled: true}, "connecting"},
		{Status{Enabled: true, Connected: true}, "connected"},
		{Status{Enabled: true, Error: "refused"}, "not connected: refused"},
	} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("%+v: %q", tc.s, got)
		}
	}
}
