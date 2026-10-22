package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func newTestHandlers(token string) *handlers {
	return &handlers{d: Deps{AdminToken: token}, failures: newFailureLimiter()}
}

func callAdmin(h *handlers, auth, remote string) *httptest.ResponseRecorder {
	called := false
	wrapped := h.admin(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	wrapped(rec, req)
	if rec.Code == http.StatusOK && !called {
		panic("next handler was not invoked on 200")
	}
	return rec
}

const attackerAddr = "203.0.113.9:1111"

func TestAdminAcceptsCorrectToken(t *testing.T) {
	h := newTestHandlers("secret")
	if rec := callAdmin(h, "Bearer secret", attackerAddr); rec.Code != http.StatusOK {
		t.Fatalf("correct token: got %d, want 200", rec.Code)
	}
}

func TestAdminRejectsMissingOrWrongToken(t *testing.T) {
	h := newTestHandlers("secret")
	for _, auth := range []string{"", "Bearer wrong", "Basic secret", "secret", "Bearer secret extra"} {
		if rec := callAdmin(h, auth, attackerAddr); rec.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: got %d, want 401", auth, rec.Code)
		}
	}
}

func TestAdminThrottlesRepeatedFailures(t *testing.T) {
	h := newTestHandlers("secret")
	for i := 0; i < adminFailLimit; i++ {
		if rec := callAdmin(h, "Bearer wrong", attackerAddr); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: got %d, want 401", i+1, rec.Code)
		}
	}
	// The ban applies even to the correct token until it expires.
	if rec := callAdmin(h, "Bearer secret", attackerAddr); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures with correct token: got %d, want 429", adminFailLimit, rec.Code)
	}
}

func TestAdminThrottleIsPerIP(t *testing.T) {
	h := newTestHandlers("secret")
	for i := 0; i < adminFailLimit; i++ {
		callAdmin(h, "Bearer wrong", attackerAddr)
	}
	if rec := callAdmin(h, "Bearer wrong", "198.51.100.7:2222"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other IP: got %d, want 401 (ban must not leak across IPs)", rec.Code)
	}
}

func TestAdminSuccessResetsFailureCount(t *testing.T) {
	h := newTestHandlers("secret")
	for i := 0; i < adminFailLimit-1; i++ {
		callAdmin(h, "Bearer wrong", attackerAddr)
	}
	if rec := callAdmin(h, "Bearer secret", attackerAddr); rec.Code != http.StatusOK {
		t.Fatalf("legit request: got %d, want 200", rec.Code)
	}
	for i := 0; i < adminFailLimit-1; i++ {
		if rec := callAdmin(h, "Bearer wrong", attackerAddr); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d after reset: got %d, want 401", i+1, rec.Code)
		}
	}
}

func TestAdminBanExpires(t *testing.T) {
	h := newTestHandlers("secret")
	now := time.Now()
	for i := 0; i < adminFailLimit; i++ {
		h.failures.recordFailure("203.0.113.9", now)
	}
	if h.failures.allowed("203.0.113.9", now.Add(adminBanDuration-time.Second)) {
		t.Fatal("IP should still be banned before expiry")
	}
	if !h.failures.allowed("203.0.113.9", now.Add(adminBanDuration+time.Second)) {
		t.Fatal("IP should be allowed after ban expiry")
	}
}

func TestRegisterSkipsAdminRoutesWithoutToken(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Deps{})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/refresh", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("without token: got %d, want 404 (routes must stay unmounted)", rec.Code)
	}

	mux = http.NewServeMux()
	Register(mux, Deps{AdminToken: "secret"})
	req = httptest.NewRequest(http.MethodPost, "/api/admin/refresh", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("with token: got %d, want 401", rec.Code)
	}
}

func TestClientIPTrustsProxyHeaderOnlyFromLoopback(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		realIP     string
		want       string
	}{
		{"loopback peer with real ip", "127.0.0.1:54321", "203.0.113.9", "203.0.113.9"},
		{"loopback peer without header", "127.0.0.1:54321", "", "127.0.0.1"},
		{"loopback peer with garbage header", "127.0.0.1:54321", "not-an-ip", "127.0.0.1"},
		{"direct peer ignores header", "10.0.0.5:1000", "203.0.113.9", "10.0.0.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			if got := ClientIP(req); got != tc.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLikeEscaperNeutralizesWildcards(t *testing.T) {
	cases := map[string]string{
		"100%":       `100\%`,
		"a_b":        `a\_b`,
		`back\slash`: `back\\slash`,
		"plain text": "plain text",
	}
	for in, want := range cases {
		if got := likeEscaper.Replace(in); got != want {
			t.Fatalf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFailureLimiterPrunesStaleEntries(t *testing.T) {
	limiter := newFailureLimiter()
	stale := time.Now().Add(-2 * adminFailWindow)
	for i := 0; i < adminTrackedMax+50; i++ {
		limiter.recordFailure("203.0.113."+strconv.Itoa(i%256)+".9", stale)
	}
	// Pruning runs once the cap is reached, so the stale flood never lets the
	// map keep growing with it.
	if len(limiter.seen) >= adminTrackedMax {
		t.Fatalf("map kept %d stale entries, want pruned below %d", len(limiter.seen), adminTrackedMax)
	}
	limiter2 := newFailureLimiter()
	now := time.Now()
	limiter2.recordFailure("203.0.113.9", now)
	limiter2.recordSuccess("203.0.113.9")
	if !limiter2.allowed("203.0.113.9", now) {
		t.Fatal("success must clear the failure state")
	}
}
