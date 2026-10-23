package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"minos/internal/auth"
	"minos/internal/config"
)

func do(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.168.1.20:5000"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

func TestLoginFlow(t *testing.T) {
	s, store := newTestServer(t, "")
	r := s.Router()

	// Open until a password is set; setting the first one needs no current.
	if rec := do(t, r, "GET", "/api/status", "", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("open instance: %d", rec.Code)
	}
	rec := do(t, r, "PUT", "/api/auth/password", `{"new":"correct horse"}`, nil, nil)
	if rec.Code != http.StatusOK || cookieFrom(rec) == nil {
		t.Fatalf("set password: %d %s", rec.Code, rec.Body)
	}
	if store.Get().API.PasswordHash == "" || store.Get().API.SessionKey == "" {
		t.Fatal("password or session key not stored")
	}
	setterCookie := cookieFrom(rec)

	// Now locked: no cookie, no entry.
	if rec := do(t, r, "GET", "/api/status", "", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", rec.Code)
	}
	var st authStatus
	_ = json.Unmarshal(do(t, r, "GET", "/api/auth", "", nil, nil).Body.Bytes(), &st)
	if !st.PasswordSet || st.Authenticated {
		t.Errorf("auth status %+v", st)
	}
	if rec := do(t, r, "POST", "/api/auth/login", `{"password":"wrong"}`, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", rec.Code)
	}
	rec = do(t, r, "POST", "/api/auth/login", `{"password":"correct horse"}`, nil, nil)
	c := cookieFrom(rec)
	if rec.Code != http.StatusOK || c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: %d cookie %+v", rec.Code, c)
	}
	if rec := do(t, r, "GET", "/api/status", "", c, nil); rec.Code != http.StatusOK {
		t.Errorf("with the session: %d", rec.Code)
	}
	// A change from another site is refused even with the cookie.
	if rec := do(t, r, "POST", "/api/pause", `{"duration":"5m"}`, c, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", rec.Code)
	}
	if rec := do(t, r, "POST", "/api/pause", `{"duration":"5m"}`, c, map[string]string{"Origin": "http://example.com"}); rec.Code != http.StatusOK {
		t.Errorf("same-origin POST: %d %s", rec.Code, rec.Body)
	}

	// Sign out everywhere ends other sessions; the caller gets a new one.
	rec = do(t, r, "POST", "/api/auth/signout-all", "", c, nil)
	fresh := cookieFrom(rec)
	if rec.Code != http.StatusOK || fresh == nil {
		t.Fatalf("signout-all: %d", rec.Code)
	}
	if rec := do(t, r, "GET", "/api/status", "", setterCookie, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("an old session survived sign-out-everywhere: %d", rec.Code)
	}
	if rec := do(t, r, "GET", "/api/status", "", fresh, nil); rec.Code != http.StatusOK {
		t.Errorf("the fresh session: %d", rec.Code)
	}

	// Changing the password needs the current one.
	if rec := do(t, r, "PUT", "/api/auth/password", `{"current":"nope","new":"another one"}`, fresh, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("change without the current password: %d", rec.Code)
	}

	// TOTP: setup, a wrong code refused, the right one enables it.
	rec = do(t, r, "POST", "/api/auth/totp/setup", "", fresh, nil)
	var setup struct{ Secret, URI string }
	_ = json.Unmarshal(rec.Body.Bytes(), &setup)
	if setup.Secret == "" || !strings.HasPrefix(setup.URI, "otpauth://") {
		t.Fatalf("totp setup: %s", rec.Body)
	}
	if rec := do(t, r, "POST", "/api/auth/totp/enable", `{"code":"000000"}`, fresh, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("wrong confirmation code: %d", rec.Code)
	}
	code := auth.TOTPCode(setup.Secret, time.Now())
	if rec := do(t, r, "POST", "/api/auth/totp/enable", `{"code":"`+code+`"}`, fresh, nil); rec.Code != http.StatusOK {
		t.Fatalf("enable totp: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, r, "POST", "/api/auth/login", `{"password":"correct horse"}`, nil, nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "need_code") {
		t.Errorf("login without the code: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, r, "POST", "/api/auth/login", `{"password":"correct horse","code":"`+auth.TOTPCode(setup.Secret, time.Now())+`"}`, nil, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("login with the code: %d %s", rec.Code, rec.Body)
	}

	// The API token keeps working alongside the password.
	_ = store.Update(func(c *config.Config) error { c.API.Token = "automation"; return nil })
	if rec := do(t, r, "GET", "/api/status", "", nil, map[string]string{"X-Api-Token": "automation"}); rec.Code != http.StatusOK {
		t.Errorf("token with a password set: %d", rec.Code)
	}

	// Removing the password needs it, and clears TOTP with it.
	if rec := do(t, r, "DELETE", "/api/auth/password", `{"current":"correct horse"}`, fresh, nil); rec.Code != http.StatusOK {
		t.Fatalf("remove password: %d %s", rec.Code, rec.Body)
	}
	if a := store.Get().API; a.PasswordHash != "" || a.TOTPSecret != "" || a.SessionKey != "" {
		t.Errorf("after removal: %+v", a)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, _ := newTestServer(t, "")
	r := s.Router()
	do(t, r, "PUT", "/api/auth/password", `{"new":"correct horse"}`, nil, nil)
	for i := 0; i < 5; i++ {
		do(t, r, "POST", "/api/auth/login", `{"password":"guess"}`, nil, nil)
	}
	rec := do(t, r, "POST", "/api/auth/login", `{"password":"correct horse"}`, nil, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("6th attempt: %d (Retry-After %q) — even the right password waits", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestSettingsViewNeverLeaksSecrets(t *testing.T) {
	s, store := newTestServer(t, "")
	_ = store.Update(func(c *config.Config) error {
		c.API.PasswordHash, c.API.SessionKey, c.API.TOTPSecret = "$argon2id$secret", "c2Vzc2lvbg==", "TOTPSECRET"
		c.API.Token = "reader"
		return nil
	})
	rec := do(t, s.Router(), "GET", "/api/config", "", nil, map[string]string{"X-Api-Token": "reader"})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"argon2id", "c2Vzc2lvbg", "TOTPSECRET"} {
		if strings.Contains(body, leak) {
			t.Errorf("/api/config exposes %q", leak)
		}
	}
}
