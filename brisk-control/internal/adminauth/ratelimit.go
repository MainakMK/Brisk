package adminauth

import (
	"sync"
	"time"
)

// LoginLimiter blunts brute-force / credential-stuffing on the login endpoint.
// Keyed by a caller-chosen string (e.g. clientIP|email): after MaxFails failures
// within Window, the key is locked for Lockout. In-memory (single instance);
// when the control plane scales out, back this with the DB/Redis.
type LoginLimiter struct {
	mu      sync.Mutex
	fails   map[string]*failState
	max     int
	window  time.Duration
	lockout time.Duration
}

type failState struct {
	count       int
	first       time.Time
	lockedUntil time.Time
}

// NewLoginLimiter builds a limiter: max failures per window, then locked for lockout.
func NewLoginLimiter(max int, window, lockout time.Duration) *LoginLimiter {
	return &LoginLimiter{fails: map[string]*failState{}, max: max, window: window, lockout: lockout}
}

// Allowed reports whether key may attempt a login now (i.e. not currently locked).
func (l *LoginLimiter) Allowed(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.fails[key]
	if s == nil {
		return true
	}
	return now.After(s.lockedUntil) || now.Equal(s.lockedUntil)
}

// Fail records a failed attempt and returns whether the key is now locked. The
// failure window resets if it has elapsed since the first failure.
func (l *LoginLimiter) Fail(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.fails[key]
	if s == nil || now.Sub(s.first) > l.window {
		s = &failState{first: now}
		l.fails[key] = s
	}
	s.count++
	if s.count >= l.max {
		s.lockedUntil = now.Add(l.lockout)
	}
	return now.Before(s.lockedUntil)
}

// Reset clears a key's failure state (call on a successful login).
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
