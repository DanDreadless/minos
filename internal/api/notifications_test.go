package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"minos/internal/notify"
)

func TestNotificationSettingsAndTest(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Without a notifier wired the endpoint says so rather than pretending.
	if rec := doJSON(t, r, "POST", "/api/notifications/test", "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no notifier: status = %d", rec.Code)
	}
	s.SetNotifier(notify.New(store))
	if rec := doJSON(t, r, "POST", "/api/notifications/test", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("nothing configured: status = %d, want 400", rec.Code)
	}

	var got atomic.Int32
	sinkSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got.Add(1) }))
	defer sinkSrv.Close()
	body := `{"notifications":{"webhook_url":"` + sinkSrv.URL + `","ui_url":" http://minos.lan:8080 ",` +
		`"quiet_hours":"22:00-07:00","mute":["digest"]}}`
	rec := doJSON(t, r, "PUT", "/api/config", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	var v configView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	n := v.Notifications
	if n.UIURL != "http://minos.lan:8080" || n.QuietHours != "22:00-07:00" || len(n.Mute) != 1 ||
		len(n.Types) == 0 {
		t.Errorf("view = %+v", n)
	}

	rec = doJSON(t, r, "POST", "/api/notifications/test", "", nil)
	var res map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res["webhook"] != "ok" || got.Load() != 1 {
		t.Errorf("test send: %d %v (deliveries %d)", rec.Code, res, got.Load())
	}

	for _, bad := range []string{
		`{"notifications":{"mute":["nonsense"]}}`,
		`{"notifications":{"quiet_hours":"night"}}`,
		`{"notifications":{"ui_url":"minos.lan"}}`,
	} {
		if rec := doJSON(t, r, "PUT", "/api/config", bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, rec.Code)
		}
	}
}
