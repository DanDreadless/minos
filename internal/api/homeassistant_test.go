package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"minos/internal/hass"
)

type fakeHA struct{ changed int }

func (f *fakeHA) Status() hass.Status { return hass.Status{Enabled: true, Connected: true} }
func (f *fakeHA) Changed()            { f.changed++ }

func TestHomeAssistantSettings(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// The password is accepted but never served, and the history hides it.
	rec := doJSON(t, r, "PUT", "/api/config",
		`{"home_assistant":{"enabled":true,"broker":"mqtt://192.168.1.10","username":"minos","password":"hunter2-broker"}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "hunter2-broker") {
		t.Error("settings served the broker password")
	}
	var got struct {
		HomeAssistant struct {
			Enabled        bool   `json:"enabled"`
			Broker         string `json:"broker"`
			PasswordSet    bool   `json:"password_set"`
			DeviceSwitches bool   `json:"device_switches"`
		} `json:"home_assistant"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.HomeAssistant.Enabled || got.HomeAssistant.Broker != "mqtt://192.168.1.10" || !got.HomeAssistant.PasswordSet || !got.HomeAssistant.DeviceSwitches {
		t.Errorf("view %+v", got.HomeAssistant)
	}
	if store.Get().HomeAssistant.Password != "hunter2-broker" {
		t.Error("password not stored")
	}
	h := store.History()
	diff := doJSON(t, r, "GET", "/api/settings/history/"+h[0].ID+"/diff", "", nil)
	if strings.Contains(diff.Body.String(), "hunter2-broker") {
		t.Error("the history diff shows the broker password")
	}

	// Leaving the password out keeps it.
	doJSON(t, r, "PUT", "/api/config", `{"home_assistant":{"username":"other"}}`, nil)
	if store.Get().HomeAssistant.Password != "hunter2-broker" {
		t.Error("an update without a password cleared it")
	}

	for _, bad := range []string{
		`{"home_assistant":{"broker":"http://ha.lan"}}`,
		`{"home_assistant":{"broker":"mqtt://user:pw@ha.lan"}}`,
		`{"home_assistant":{"topic_prefix":"minos/#"}}`,
		`{"home_assistant":{"discovery_prefix":"/homeassistant"}}`,
		`{"home_assistant":{"enabled":true,"broker":""}}`,
	} {
		if rec := doJSON(t, r, "PUT", "/api/config", bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s accepted: %d", bad, rec.Code)
		}
	}
}

func TestHomeAssistantStatusAndPause(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()
	if rec := doJSON(t, r, "GET", "/api/home-assistant/status", "", nil); !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("status without a bridge: %s", rec.Body)
	}
	ha := &fakeHA{}
	s.SetHomeAssistant(ha)
	if rec := doJSON(t, r, "GET", "/api/home-assistant/status", "", nil); !strings.Contains(rec.Body.String(), `"connected":true`) {
		t.Errorf("status: %s", rec.Body)
	}
	doJSON(t, r, "POST", "/api/pause", `{"duration":"5m"}`, nil)
	doJSON(t, r, "DELETE", "/api/pause", "", nil)
	if ha.changed != 2 {
		t.Errorf("pause and resume nudged the bridge %d times, want 2", ha.changed)
	}
}
