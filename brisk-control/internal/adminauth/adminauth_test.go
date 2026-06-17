package adminauth

import (
	"testing"
	"time"
)

func TestPasswordHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if h == "correct horse battery staple" || len(h) < 40 {
		t.Fatalf("hash looks wrong: %q", h)
	}
	if !VerifyPassword("correct horse battery staple", h) {
		t.Fatal("correct password should verify")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("wrong password must NOT verify")
	}
	if VerifyPassword("x", "not-a-valid-hash") {
		t.Fatal("malformed hash must not verify")
	}
}

func TestAdminTokenShape(t *testing.T) {
	tok, err := NewAdminToken()
	if err != nil {
		t.Fatal(err)
	}
	if !IsAdminToken(tok) {
		t.Fatalf("admin token should be recognized: %q", tok)
	}
	if IsAdminToken("brisk_agenttokenstyle") {
		t.Fatal("agent-style token must not be seen as admin token")
	}
	if len(AdminPrefix(tok)) != AdminPrefixLen {
		t.Fatalf("prefix len = %d, want %d", len(AdminPrefix(tok)), AdminPrefixLen)
	}
}

func TestLoginLimiter(t *testing.T) {
	now := time.Now()
	l := NewLoginLimiter(3, time.Minute, 10*time.Minute)
	key := "1.2.3.4|admin@example.com"

	if !l.Allowed(key, now) {
		t.Fatal("fresh key should be allowed")
	}
	l.Fail(key, now)
	l.Fail(key, now)
	if locked := l.Fail(key, now); !locked { // 3rd failure -> locked
		t.Fatal("should be locked after max failures")
	}
	if l.Allowed(key, now) {
		t.Fatal("locked key must not be allowed")
	}
	if !l.Allowed(key, now.Add(11*time.Minute)) {
		t.Fatal("should be allowed again after lockout elapses")
	}
	l.Reset(key)
	if !l.Allowed(key, now) {
		t.Fatal("reset key should be allowed")
	}
}
