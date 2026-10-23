// Package auth is the web interface's login: password hashing (argon2id),
// signed session cookies, optional TOTP codes, and a limit on failed
// attempts. A leaf package — config strings in, decisions out — so the
// security-relevant pieces are testable on their own.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP is defined over HMAC-SHA1; authenticator apps expect it
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: ~100–200 ms per login on a Raspberry Pi 4, 32 MiB.
// Logins are rare; an attacker with the hash pays the same per guess.
const (
	argonTime    = 2
	argonMemory  = 32 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16

	// MinPasswordLength is the shortest password accepted.
	MinPasswordLength = 8
)

// HashPassword returns an argon2id PHC string for password.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("the password must be at least %d characters", MinPasswordLength)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword reports whether password matches a PHC string from
// HashPassword, using the parameters stored in it.
func CheckPassword(phc, password string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewSessionKey returns a fresh base64 session-signing key.
func NewSessionKey() (string, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k), nil
}

// SessionTTL is how long a login lasts.
const SessionTTL = 30 * 24 * time.Hour

// IssueSession returns a session value signed with key, valid until
// now+SessionTTL: base64url(expiry ‖ nonce) "." base64url(HMAC-SHA256).
// Nothing is stored server-side, so a session survives restarts; replacing
// the key ends every session at once.
func IssueSession(key string, now time.Time) (string, error) {
	k, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(k) < 16 {
		return "", errors.New("no session key")
	}
	payload := make([]byte, 8+16)
	binary.BigEndian.PutUint64(payload, uint64(now.Add(SessionTTL).Unix()))
	if _, err := rand.Read(payload[8:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sign(k, payload)), nil
}

// ValidSession reports whether value is a session signed with key and not
// yet expired.
func ValidSession(key, value string, now time.Time) bool {
	k, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(k) < 16 {
		return false
	}
	p, s, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	mac, err2 := base64.RawURLEncoding.DecodeString(s)
	if err1 != nil || err2 != nil || len(payload) != 24 {
		return false
	}
	if !hmac.Equal(mac, sign(k, payload)) {
		return false
	}
	return now.Unix() < int64(binary.BigEndian.Uint64(payload))
}

func sign(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("minos-session-v1\x00"))
	h.Write(payload)
	return h.Sum(nil)
}

// --- TOTP (RFC 6238: 30-second steps, 6 digits, HMAC-SHA1) ---

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh base32 secret.
func NewTOTPSecret() (string, error) {
	k := make([]byte, 20)
	if _, err := rand.Read(k); err != nil {
		return "", err
	}
	return b32.EncodeToString(k), nil
}

// TOTPURI is the otpauth:// link authenticator apps import.
func TOTPURI(secret, account string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", "Minos")
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape("Minos:"+account) + "?" + v.Encode()
}

// ValidTOTP reports whether code is right for secret at now, allowing one
// step either side for clock drift.
func ValidTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return false
	}
	k, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil || len(k) == 0 {
		return false
	}
	step := now.Unix() / 30
	for d := int64(-1); d <= 1; d++ {
		if subtle.ConstantTimeCompare([]byte(totpAt(k, step+d)), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func totpAt(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	h := hmac.New(sha1.New, key)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", v)
}

// --- failed-login limit ---

// Limiter refuses logins from an address after too many failures in a
// window, so a password cannot be guessed at network speed.
type Limiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	max      int
	window   time.Duration
}

// NewLimiter allows max failures per address per window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{failures: map[string][]time.Time{}, max: max, window: window}
}

// Allowed reports whether addr may try now, and if not, how long until it
// may.
func (l *Limiter) Allowed(addr string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.prune(addr, now)
	if len(recent) < l.max {
		return true, 0
	}
	return false, recent[0].Add(l.window).Sub(now)
}

// Failed records a failed attempt from addr.
func (l *Limiter) Failed(addr string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[addr] = append(l.prune(addr, now), now)
}

// Succeeded forgets addr's failures.
func (l *Limiter) Succeeded(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, addr)
}

func (l *Limiter) prune(addr string, now time.Time) []time.Time {
	kept := l.failures[addr][:0]
	for _, t := range l.failures[addr] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, addr)
		return nil
	}
	l.failures[addr] = kept
	return kept
}

// TOTPCode is the code for secret at now (what an authenticator app shows).
func TOTPCode(secret string, now time.Time) string {
	k, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return ""
	}
	return totpAt(k, now.Unix()/30)
}
