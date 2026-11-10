package wiki

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// RateLimiter implements a per-IP token bucket rate limiter.
type RateLimiter struct {
	proxies  ProxyTrust // reverse proxies whose X-Forwarded-For is believed
	mu       sync.Mutex
	visitors map[string]*bucket
	rate     int           // tokens added per interval
	burst    int           // max tokens
	interval time.Duration // refill interval
}

type bucket struct {
	tokens   int
	lastSeen time.Time
}

// NewRateLimiter creates a rate limiter that allows rate requests per interval
// with a burst capacity.
func NewRateLimiter(rate, burst int, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     rate,
		burst:    burst,
		interval: interval,
	}
	go rl.cleanup()
	return rl
}

// Allow returns true if the request from the given IP is within the rate limit.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.visitors[ip]
	if !ok {
		rl.visitors[ip] = &bucket{tokens: rl.burst - 1, lastSeen: time.Now()}
		return true
	}

	// Refill tokens based on elapsed time.
	elapsed := time.Since(b.lastSeen)
	refill := int(elapsed / rl.interval) * rl.rate
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.lastSeen = time.Now()
	}

	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// cleanup removes stale entries every 5 minutes.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, b := range rl.visitors {
			if b.lastSeen.Before(cutoff) {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// RateLimit wraps an http.Handler and rejects requests that exceed the limit.
func RateLimit(rl *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(rl.proxies.ClientKey(r)) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimitFunc wraps an http.HandlerFunc with rate limiting.
func RateLimitFunc(rl *RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return RateLimit(rl, next).ServeHTTP
}

// ProxyTrust lists the reverse proxies in front of the wiki. Only a request
// that arrives from one of them has its X-Forwarded-For header believed; the
// zero value trusts none, so the connection's own address is used.
type ProxyTrust []netip.Prefix

// ParseTrustedProxies parses a comma-separated list of addresses and CIDR
// ranges, such as "10.42.0.0/16, 192.168.1.5".
func ParseTrustedProxies(list string) (ProxyTrust, error) {
	var trust ProxyTrust
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			addr, addrErr := netip.ParseAddr(item)
			if addrErr != nil {
				return nil, fmt.Errorf("parsing trusted proxy %q: %w", item, err)
			}
			prefix = netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen())
		}
		trust = append(trust, prefix.Masked())
	}
	return trust, nil
}

func (t ProxyTrust) contains(addr netip.Addr) bool {
	for _, p := range t {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientKey returns the address a request is counted under for rate limits
// and login lockout. It is the client address: taken from the connection, or,
// when that is a trusted proxy, the last X-Forwarded-For entry that is not
// itself a trusted proxy (entries further left can be forged by the client).
// IPv6 clients are counted per /64, since one client usually owns that range.
func (t ProxyTrust) ClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if t.contains(addr) {
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				break
			}
			addr = hop.Unmap()
			if !t.contains(addr) {
				break
			}
		}
	}
	if addr.Is6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}

// Login lockout: loginMaxFails wrong passwords from one address within
// loginWindow lock that address out for loginLockout.
const (
	loginMaxFails = 5
	loginWindow   = 15 * time.Minute
	loginLockout  = 15 * time.Minute
)

// loginGuard counts wrong passwords per client address.
type loginGuard struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
	now     func() time.Time
}

type loginEntry struct {
	fails       int
	first       time.Time // first wrong password of the current window
	lockedUntil time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{entries: make(map[string]*loginEntry), now: time.Now}
}

// Locked returns how long key stays locked out, or 0 when it may try.
func (g *loginGuard) Locked(key string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.entries[key]; ok {
		if wait := e.lockedUntil.Sub(g.now()); wait > 0 {
			return wait
		}
	}
	return 0
}

// Fail records a wrong password and reports whether key is now locked out.
func (g *loginGuard) Fail(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, e := range g.entries {
		if now.After(e.lockedUntil) && now.Sub(e.first) > loginWindow {
			delete(g.entries, k)
		}
	}
	e, ok := g.entries[key]
	if !ok {
		e = &loginEntry{first: now}
		g.entries[key] = e
	}
	e.fails++
	if e.fails < loginMaxFails {
		return false
	}
	e.fails = 0
	e.first = now
	e.lockedUntil = now.Add(loginLockout)
	return true
}

// Reset forgets the wrong passwords of key after a successful login.
func (g *loginGuard) Reset(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, key)
}
