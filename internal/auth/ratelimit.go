package auth

import (
	"sync"
	"time"
)

// LoginLimiter enforces RFC-001 §9's /auth/login rate limit — "5
// попыток/мин на логин" — keyed by the login string, not by IP: a
// classroom's 20–30 РМ can sit behind one NAT/proxy address, so an
// IP-keyed limit would lock out the whole room over one trainee's typos.
// It is in-memory and per-process; the RFC runs a single API process
// (§4.3), so a shared cross-process limiter is not required.
type LoginLimiter struct {
	maxAttempts int
	window      time.Duration
	now         func() time.Time

	mu       sync.Mutex
	attempts map[string]*loginWindow
}

type loginWindow struct {
	count   int
	resetAt time.Time
}

// NewLoginLimiter returns a limiter allowing maxAttempts per window per
// login. now defaults to time.Now when nil; tests inject a fake clock to
// exercise window expiry without sleeping.
func NewLoginLimiter(maxAttempts int, window time.Duration, now func() time.Time) *LoginLimiter {
	if now == nil {
		now = time.Now
	}
	return &LoginLimiter{
		maxAttempts: maxAttempts,
		window:      window,
		now:         now,
		attempts:    make(map[string]*loginWindow),
	}
}

// DefaultLoginLimiter returns the RFC-001 §9 default: 5 attempts/min.
func DefaultLoginLimiter() *LoginLimiter {
	return NewLoginLimiter(5, time.Minute, nil)
}

// Allow reports whether another login attempt for login is permitted right
// now, and counts this call toward the window regardless of whether the
// caller's own credential check goes on to succeed or fail — the RFC
// limits attempts, not failures, so the count must be charged before the
// password is even checked (see service.go's Login, which calls Allow
// first).
func (l *LoginLimiter) Allow(login string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evictExpiredLocked(now)

	w, ok := l.attempts[login]
	if !ok || !now.Before(w.resetAt) {
		l.attempts[login] = &loginWindow{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if w.count >= l.maxAttempts {
		return false
	}
	w.count++
	return true
}

// evictExpiredLocked drops windows whose reset time has passed, so a long-
// lived process does not accumulate one entry per login ever attempted.
// Called with mu held; a full-map scan on every Allow is fine at this
// scale (RFC-001 §11: 20–30 workstations).
func (l *LoginLimiter) evictExpiredLocked(now time.Time) {
	for login, w := range l.attempts {
		if !now.Before(w.resetAt) {
			delete(l.attempts, login)
		}
	}
}
