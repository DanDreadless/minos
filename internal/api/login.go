package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"minos/internal/auth"
	"minos/internal/config"
)

// Login for the web interface. With a password set, a request is allowed
// with a valid session cookie (the browser) or the API token (automation,
// the CLI, Home Assistant). Without either configured the interface is
// open, as before — and the dashboard says so.

const sessionCookie = "minos_session"

// loginGuard holds the per-server login state that is not configuration:
// the failed-attempt limiter and a TOTP secret waiting for confirmation.
type loginGuard struct {
	limiter     *auth.Limiter
	mu          sync.Mutex
	pendingTOTP string
}

func newLoginGuard() *loginGuard {
	return &loginGuard{limiter: auth.NewLimiter(5, time.Minute)}
}

// tokenOK reports a request carrying the API token.
func tokenOK(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	got := r.Header.Get("X-Api-Token")
	if got == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimPrefix(h, "Bearer ")
		}
	}
	if got == "" && strings.HasSuffix(r.URL.Path, "/stream") {
		// Browsers cannot set headers on a WebSocket; the stream endpoints
		// accept the token as a query parameter instead.
		got = r.URL.Query().Get("token")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// sessionOK reports a request carrying a valid session cookie.
func sessionOK(r *http.Request, c config.APIConfig) bool {
	if c.PasswordHash == "" {
		return false
	}
	ck, err := r.Cookie(sessionCookie)
	return err == nil && auth.ValidSession(c.SessionKey, ck.Value, time.Now())
}

// sameOrigin refuses cookie-authenticated cross-site requests that change
// things. SameSite=Strict already keeps the cookie off other sites'
// requests in every current browser; this is the second lock.
func sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser, or a same-origin request from an older one
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := s.store.Get().API
		if c.Token == "" && c.PasswordHash == "" {
			next.ServeHTTP(w, withActor(r, "Open access"))
			return
		}
		if tokenOK(r, c.Token) {
			next.ServeHTTP(w, withActor(r, "API token"))
			return
		}
		if sessionOK(r, c) {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			next.ServeHTTP(w, withActor(r, "Password sign-in"))
			return
		}
		if c.PasswordHash != "" {
			writeError(w, http.StatusUnauthorized, "log in to continue")
			return
		}
		writeError(w, http.StatusUnauthorized, "missing or invalid API token")
	})
}

type authStatus struct {
	PasswordSet   bool `json:"password_set"`
	TOTPEnabled   bool `json:"totp_enabled"`
	TokenSet      bool `json:"token_set"`
	Authenticated bool `json:"authenticated"`
}

// handleAuthStatus is reachable without logging in: the UI asks it which
// screen to show.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	c := s.store.Get().API
	open := c.Token == "" && c.PasswordHash == ""
	writeJSON(w, http.StatusOK, authStatus{
		PasswordSet:   c.PasswordHash != "",
		TOTPEnabled:   c.TOTPSecret != "",
		TokenSet:      c.Token != "",
		Authenticated: open || tokenOK(r, c.Token) || sessionOK(r, c),
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(auth.SessionTTL / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return false
	}
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	c := s.store.Get().API
	if c.PasswordHash == "" {
		writeError(w, http.StatusBadRequest, "no password is set")
		return
	}
	ip := clientAddr(r)
	if ok, wait := s.login.limiter.Allowed(ip, time.Now()); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("too many failed attempts; try again in %d seconds", int(wait.Seconds())+1))
		return
	}
	if !auth.CheckPassword(c.PasswordHash, body.Password) {
		s.login.limiter.Failed(ip, time.Now())
		slog.Warn("failed login: wrong password", "client", ip)
		writeError(w, http.StatusUnauthorized, "wrong password")
		return
	}
	if c.TOTPSecret != "" {
		if strings.TrimSpace(body.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "enter the code from your authenticator app", "need_code": true})
			return
		}
		if !auth.ValidTOTP(c.TOTPSecret, body.Code, time.Now()) {
			s.login.limiter.Failed(ip, time.Now())
			slog.Warn("failed login: wrong authenticator code", "client", ip)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "wrong code", "need_code": true})
			return
		}
	}
	session, err := auth.IssueSession(c.SessionKey, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.login.limiter.Succeeded(ip)
	setSessionCookie(w, r, session)
	slog.Info("logged in", "client", ip)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged in"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// handleSetPassword sets or changes the password. Changing it needs the
// current one, even with a valid session; every other session ends.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if c := s.store.Get().API; c.PasswordHash != "" && !auth.CheckPassword(c.PasswordHash, body.Current) {
		writeError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key, err := auth.NewSessionKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.update(r, func(c *config.Config) error {
		c.API.PasswordHash, c.API.SessionKey = hash, key
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session, err := auth.IssueSession(key, time.Now()); err == nil {
		setSessionCookie(w, r, session) // stay logged in here
	}
	slog.Info("web interface password set", "client", clientAddr(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "password set"})
}

// handleRemovePassword turns the login off (the current password is
// required). Authenticator codes go with it.
func (s *Server) handleRemovePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	c := s.store.Get().API
	if c.PasswordHash == "" {
		writeError(w, http.StatusBadRequest, "no password is set")
		return
	}
	if !auth.CheckPassword(c.PasswordHash, body.Current) {
		writeError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}
	if err := s.update(r, func(c *config.Config) error {
		c.API.PasswordHash, c.API.SessionKey, c.API.TOTPSecret = "", "", ""
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clearSessionCookie(w, r)
	slog.Warn("web interface password removed", "client", clientAddr(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "password removed"})
}

// handleSignOutEverywhere replaces the session key: every session, on every
// device, ends — except this one, which gets a fresh cookie.
func (s *Server) handleSignOutEverywhere(w http.ResponseWriter, r *http.Request) {
	if s.store.Get().API.PasswordHash == "" {
		writeError(w, http.StatusBadRequest, "no password is set")
		return
	}
	key, err := auth.NewSessionKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.update(r, func(c *config.Config) error {
		c.API.SessionKey = key
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if session, err := auth.IssueSession(key, time.Now()); err == nil {
		setSessionCookie(w, r, session)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out everywhere else"})
}

// handleTOTPSetup starts turning authenticator codes on: a new secret,
// held until a code from it is confirmed.
func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	if s.store.Get().API.PasswordHash == "" {
		writeError(w, http.StatusBadRequest, "set a password first")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.login.mu.Lock()
	s.login.pendingTOTP = secret
	s.login.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": auth.TOTPURI(secret, "admin")})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.login.mu.Lock()
	secret := s.login.pendingTOTP
	s.login.mu.Unlock()
	if secret == "" {
		writeError(w, http.StatusBadRequest, "start the setup first")
		return
	}
	if !auth.ValidTOTP(secret, body.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "that code does not match — check the time on your phone and try the next one")
		return
	}
	if err := s.update(r, func(c *config.Config) error {
		c.API.TOTPSecret = secret
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.login.mu.Lock()
	s.login.pendingTOTP = ""
	s.login.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "authenticator codes on"})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	c := s.store.Get().API
	if c.PasswordHash == "" || !auth.CheckPassword(c.PasswordHash, body.Current) {
		writeError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}
	if err := s.update(r, func(c *config.Config) error {
		c.API.TOTPSecret = ""
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "authenticator codes off"})
}

// clientAddr is the request's client address without the port.
func clientAddr(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}
