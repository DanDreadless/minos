package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Errorf("hash = %q", h)
	}
	if !CheckPassword(h, "correct horse battery") || CheckPassword(h, "correct horse batterY") || CheckPassword(h, "") {
		t.Error("password check is wrong")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password must differ (salt)")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("a 5-character password was accepted")
	}
	for _, bad := range []string{"", "$argon2id$", "$bcrypt$x$y$z$w", "$argon2id$v=19$m=0,t=0,p=0$AA$AA"} {
		if CheckPassword(bad, "anything") {
			t.Errorf("malformed hash %q accepted a password", bad)
		}
	}
}

func TestSessions(t *testing.T) {
	key, _ := NewSessionKey()
	now := time.Unix(1_800_000_000, 0)
	s, err := IssueSession(key, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSession(key, s, now.Add(time.Hour)) {
		t.Error("a fresh session was refused")
	}
	if ValidSession(key, s, now.Add(SessionTTL+time.Second)) {
		t.Error("an expired session was accepted")
	}
	other, _ := NewSessionKey()
	if ValidSession(other, s, now) {
		t.Error("a session signed with another key was accepted (sign out everywhere must work)")
	}
	tampered := []byte(s)
	tampered[3] ^= 1
	if ValidSession(key, string(tampered), now) {
		t.Error("a tampered session was accepted")
	}
	for _, bad := range []string{"", ".", "abc", "abc.def"} {
		if ValidSession(key, bad, now) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// RFC 6238 Appendix B test vectors (SHA1, 8 digits there; 6 here = the
// low 6 digits).
func TestTOTPVectors(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for _, c := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if !ValidTOTP(secret, c.code, time.Unix(c.unix, 0)) {
			t.Errorf("t=%d: %s refused", c.unix, c.code)
		}
	}
	if ValidTOTP(secret, "000000", time.Unix(59, 0)) || ValidTOTP(secret, "28708", time.Unix(59, 0)) {
		t.Error("a wrong code was accepted")
	}
	// One step of clock drift either way is tolerated, two is not.
	if !ValidTOTP(secret, "287082", time.Unix(59+30, 0)) || ValidTOTP(secret, "287082", time.Unix(59+90, 0)) {
		t.Error("drift window wrong")
	}
	if !strings.HasPrefix(TOTPURI(secret, "admin"), "otpauth://totp/Minos:admin?") {
		t.Errorf("uri = %s", TOTPURI(secret, "admin"))
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("10.0.0.5", now); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
		l.Failed("10.0.0.5", now)
	}
	if ok, wait := l.Allowed("10.0.0.5", now.Add(10*time.Second)); ok || wait <= 0 {
		t.Errorf("4th attempt allowed (%v)", wait)
	}
	if ok, _ := l.Allowed("10.0.0.6", now); !ok {
		t.Error("another address was limited")
	}
	if ok, _ := l.Allowed("10.0.0.5", now.Add(61*time.Second)); !ok {
		t.Error("the window did not expire")
	}
	l.Failed("10.0.0.7", now)
	l.Succeeded("10.0.0.7")
	if len(l.failures) != 0 {
		t.Errorf("failures not cleaned up: %v", l.failures)
	}
}
