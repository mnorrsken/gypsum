package wiki

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowBurst(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     1,
		burst:    5,
		interval: time.Second,
	}

	// First burst requests should all be allowed.
	for i := range 5 {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}

	// Next request should be denied (burst exhausted, no time elapsed).
	if rl.Allow("1.2.3.4") {
		t.Fatal("request after burst should be denied")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     10,
		burst:    10,
		interval: time.Millisecond,
	}

	// Exhaust the burst.
	for range 10 {
		rl.Allow("1.2.3.4")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("should be exhausted")
	}

	// Wait for refill.
	time.Sleep(5 * time.Millisecond)

	// Should have refilled some tokens.
	if !rl.Allow("1.2.3.4") {
		t.Fatal("should be allowed after refill interval")
	}
}

func TestRateLimiterSeparateIPs(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     1,
		burst:    2,
		interval: time.Second,
	}

	// Exhaust IP A.
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")
	if rl.Allow("10.0.0.1") {
		t.Fatal("IP A should be exhausted")
	}

	// IP B should still have its own burst.
	if !rl.Allow("10.0.0.2") {
		t.Fatal("IP B should be allowed independently")
	}
}

func TestRateLimiterRefillCapsAtBurst(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     100,
		burst:    3,
		interval: time.Millisecond,
	}

	// Use one token.
	rl.Allow("1.1.1.1")

	// Wait long enough for a huge refill.
	time.Sleep(10 * time.Millisecond)

	// Should be capped at burst, so 3 allowed and 4th denied.
	for i := range 3 {
		if !rl.Allow("1.1.1.1") {
			t.Fatalf("request %d should be allowed after refill", i+1)
		}
	}
	if rl.Allow("1.1.1.1") {
		t.Fatal("should be capped at burst after refill")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     1,
		burst:    2,
		interval: time.Minute,
	}

	handler := RateLimit(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First two requests should pass.
	for i := range 2 {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, w.Code)
		}
	}

	// Third request should be rate-limited.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited request: status = %d, want 429", w.Code)
	}
}

func TestRateLimitFuncMiddleware(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     1,
		burst:    1,
		interval: time.Minute,
	}

	called := false
	handler := RateLimitFunc(rl, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// First request passes.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	handler(w, r)
	if !called {
		t.Fatal("handler should have been called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Second request blocked.
	called = false
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.1:5555"
	handler(w2, r2)
	if called {
		t.Fatal("handler should NOT have been called when rate limited")
	}
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w2.Code)
	}
}

func TestRateLimitMiddlewareNoPort(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     1,
		burst:    1,
		interval: time.Minute,
	}

	handler := RateLimit(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// RemoteAddr without a port (edge case).
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5"
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Same IP should be limited.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.5"
	handler.ServeHTTP(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w2.Code)
	}
}

func TestRateLimiterNewVisitorGetsFullBurst(t *testing.T) {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     1,
		burst:    10,
		interval: time.Hour,
	}

	// A brand new IP should get full burst capacity.
	for i := range 10 {
		if !rl.Allow("new-ip") {
			t.Fatalf("new visitor: request %d should be allowed", i+1)
		}
	}
	if rl.Allow("new-ip") {
		t.Fatal("new visitor: should be denied after burst")
	}
}
