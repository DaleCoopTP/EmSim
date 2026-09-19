package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterAllowsUpToMaxAttemptsThenDenies(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter := NewLoginLimiter(5, time.Minute, func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if !limiter.Allow("dispatcher-1") {
			t.Fatalf("attempt %d unexpectedly denied", i+1)
		}
	}
	if limiter.Allow("dispatcher-1") {
		t.Fatal("6th attempt within the window was allowed")
	}
}

func TestLoginLimiterIsPerLogin(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter := NewLoginLimiter(1, time.Minute, func() time.Time { return now })

	if !limiter.Allow("a") {
		t.Fatal("first attempt for a denied")
	}
	if limiter.Allow("a") {
		t.Fatal("second attempt for a allowed")
	}
	if !limiter.Allow("b") {
		t.Fatal("first attempt for b denied — limiter is not per-login")
	}
}

func TestLoginLimiterResetsOnceWindowElapses(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter := NewLoginLimiter(2, time.Minute, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		if !limiter.Allow("a") {
			t.Fatalf("attempt %d denied", i+1)
		}
	}
	if limiter.Allow("a") {
		t.Fatal("third attempt within the window was allowed")
	}

	now = now.Add(time.Minute) // exactly at the window boundary
	if !limiter.Allow("a") {
		t.Fatal("attempt at the window boundary was denied, want a fresh window")
	}
}

func TestLoginLimiterEvictsExpiredEntries(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter := NewLoginLimiter(1, time.Minute, func() time.Time { return now })

	limiter.Allow("a")
	now = now.Add(2 * time.Minute)
	limiter.Allow("b") // triggers eviction while scanning the map

	limiter.mu.Lock()
	_, stillTracked := limiter.attempts["a"]
	limiter.mu.Unlock()
	if stillTracked {
		t.Fatal("expired login window for 'a' was not evicted")
	}
}

func TestDefaultLoginLimiterMatchesRFC001Limit(t *testing.T) {
	limiter := DefaultLoginLimiter()
	if limiter.maxAttempts != 5 || limiter.window != time.Minute {
		t.Fatalf("DefaultLoginLimiter() = {max=%d window=%s}, want {5 1m0s} (RFC-001 §9: \"5 попыток/мин\")",
			limiter.maxAttempts, limiter.window)
	}
}

func TestLoginLimiterIsSafeForConcurrentUse(t *testing.T) {
	limiter := NewLoginLimiter(1000, time.Minute, nil)
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				limiter.Allow("shared-login")
			}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
