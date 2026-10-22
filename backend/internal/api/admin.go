package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ClientIP resolves the requester's real IP. The service listens on loopback
// only, so every request arrives through the local reverse proxy; when the
// direct peer is loopback the proxy-set X-Real-IP (which nginx overwrites on
// every hop) is trusted, otherwise the peer address itself is used.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(real) != nil {
			return real
		}
	}
	return host
}

// Throttling of admin auth failures: after adminFailLimit rejected tokens
// inside adminFailWindow, the client IP is banned for adminBanDuration.
const (
	adminFailWindow  = 15 * time.Minute
	adminFailLimit   = 5
	adminBanDuration = 15 * time.Minute
	// adminTrackedMax caps how many IPs stay tracked; stale entries are
	// pruned lazily so a rotating-IP flood cannot grow the map unbounded.
	adminTrackedMax = 10000
)

type ipFailureState struct {
	count       int
	windowStart time.Time
	bannedUntil time.Time
}

type failureLimiter struct {
	mu   sync.Mutex
	seen map[string]*ipFailureState
}

func newFailureLimiter() *failureLimiter {
	return &failureLimiter{seen: make(map[string]*ipFailureState)}
}

func (l *failureLimiter) allowed(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.seen[ip]
	return state == nil || now.After(state.bannedUntil)
}

func (l *failureLimiter) recordFailure(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	state := l.seen[ip]
	if state == nil {
		state = &ipFailureState{windowStart: now}
		l.seen[ip] = state
	}
	if now.Sub(state.windowStart) >= adminFailWindow {
		state.count = 0
		state.windowStart = now
	}
	state.count++
	if state.count >= adminFailLimit {
		state.bannedUntil = now.Add(adminBanDuration)
	}
}

func (l *failureLimiter) recordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, ip)
}

func (l *failureLimiter) prune(now time.Time) {
	if len(l.seen) < adminTrackedMax {
		return
	}
	for ip, state := range l.seen {
		if now.After(state.bannedUntil) && now.Sub(state.windowStart) >= adminFailWindow {
			delete(l.seen, ip)
		}
	}
}

// admin gates the admin routes: constant-time Bearer-token comparison plus
// per-IP throttling of failed attempts (brute-force resistance).
func (h *handlers) admin(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		if !h.failures.allowed(ip, time.Now()) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "认证失败次数过多，请稍后再试")
			return
		}
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + h.d.AdminToken
		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
			h.failures.recordFailure(ip, time.Now())
			if h.d.Log != nil {
				h.d.Log.Warn("admin auth failed", "ip", ip, "path", r.URL.Path)
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "需要 Bearer Token")
			return
		}
		h.failures.recordSuccess(ip)
		next(w, r)
	}
}
