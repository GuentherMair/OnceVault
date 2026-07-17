package server

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"
)

// --- Limiter unit tests ----------------------------------------------------

// clockedLimiter returns a limiter with a controllable clock.
func clockedLimiter() (*Limiter, *int64) {
	l := NewLimiter()
	now := new(int64)
	*now = 1_000_000
	l.now = func() int64 { return *now }
	return l, now
}

func TestLimiterWindow(t *testing.T) {
	l, now := clockedLimiter()
	ip := netip.MustParseAddr("192.0.2.1")

	for i := 0; i < 2; i++ {
		if !l.Allow(ip, 2) {
			t.Fatalf("request %d denied, want allowed", i+1)
		}
	}
	if l.Allow(ip, 2) {
		t.Fatal("request 3 allowed, want denied")
	}
	// Still inside the window.
	*now += 30
	if l.Allow(ip, 2) {
		t.Fatal("request inside window allowed, want denied")
	}
	// Window rolls over after 60s from its start.
	*now += 30
	if !l.Allow(ip, 2) {
		t.Fatal("request in fresh window denied, want allowed")
	}
}

func TestLimiterPerIPIsolation(t *testing.T) {
	l, _ := clockedLimiter()
	a := netip.MustParseAddr("192.0.2.1")
	b := netip.MustParseAddr("192.0.2.2")
	if !l.Allow(a, 1) || l.Allow(a, 1) {
		t.Fatal("limit for a not enforced")
	}
	if !l.Allow(b, 1) {
		t.Fatal("b denied by a's window")
	}
}

func TestLimiterUnlimited(t *testing.T) {
	l, _ := clockedLimiter()
	ip := netip.MustParseAddr("192.0.2.1")
	for i := 0; i < 1000; i++ {
		if !l.Allow(ip, 0) {
			t.Fatal("unlimited denied")
		}
	}
	if len(l.windows) != 0 {
		t.Fatalf("unlimited traffic grew the map to %d entries, want 0", len(l.windows))
	}
}

func TestLimiterPrunesStaleWindows(t *testing.T) {
	l, now := clockedLimiter()
	l.pruneAt = 8
	for i := 0; i < 8; i++ {
		l.Allow(netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", i+1)), 10)
	}
	if len(l.windows) != 8 {
		t.Fatalf("windows = %d, want 8", len(l.windows))
	}
	// All 8 windows go stale; the next access is over threshold and sweeps them.
	*now += 61
	l.Allow(netip.MustParseAddr("198.51.100.1"), 10)
	if len(l.windows) != 1 {
		t.Fatalf("windows after prune = %d, want 1", len(l.windows))
	}
}

// --- HTTP-level rate limiting ----------------------------------------------

func TestRateLimitDefaultExceeded(t *testing.T) {
	cfg := loadCfg(t, map[string]any{"rate_limit": map[string]any{"default": 2}})
	srv := New(cfg, memStore(t), []byte(testIndex))

	body := postBody(validSecret, validIV, 24)
	for i := 0; i < 2; i++ {
		rr := do(t, srv, "POST", "/api/secrets", "203.0.113.9:1000", body, nil)
		if rr.Code != http.StatusCreated {
			t.Fatalf("POST %d = %d, want 201", i+1, rr.Code)
		}
	}
	rr := do(t, srv, "POST", "/api/secrets", "203.0.113.9:1000", body, nil)
	wantError(t, rr, http.StatusTooManyRequests, msgRateLimited)

	// A different IP is unaffected.
	rr = do(t, srv, "POST", "/api/secrets", "203.0.113.10:1000", body, nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("other IP = %d, want 201", rr.Code)
	}
}

func TestRateLimitCountsOnlyPost(t *testing.T) {
	cfg := loadCfg(t, map[string]any{"rate_limit": map[string]any{"default": 1}})
	srv := New(cfg, memStore(t), []byte(testIndex))

	// GETs never consume or hit the POST budget.
	for i := 0; i < 5; i++ {
		if rr := do(t, srv, "GET", "/", "203.0.113.9:1", "", nil); rr.Code != http.StatusOK {
			t.Fatalf("GET / = %d, want 200", rr.Code)
		}
		rr := do(t, srv, "GET", "/api/secrets/01234567-89ab-4cde-8f01-23456789abcd", "203.0.113.9:1", "", nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET secret = %d, want 404", rr.Code)
		}
	}
	rr := do(t, srv, "POST", "/api/secrets", "203.0.113.9:1", postBody(validSecret, validIV, 24), nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201", rr.Code)
	}
	rr = do(t, srv, "POST", "/api/secrets", "203.0.113.9:1", postBody(validSecret, validIV, 24), nil)
	wantError(t, rr, http.StatusTooManyRequests, msgRateLimited)
	// GETs still work after the POST budget is spent.
	if rr := do(t, srv, "GET", "/", "203.0.113.9:1", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("GET / after 429 = %d, want 200", rr.Code)
	}
}

func TestRateLimitUnlimitedCIDR(t *testing.T) {
	cfg := loadCfg(t, map[string]any{"rate_limit": map[string]any{
		"default": 1,
		"rules":   map[string]string{"10.0.0.0/8": "0"},
	}})
	srv := New(cfg, memStore(t), []byte(testIndex))

	for i := 0; i < 5; i++ {
		rr := do(t, srv, "POST", "/api/secrets", "10.1.2.3:1", postBody(validSecret, validIV, 24), nil)
		if rr.Code != http.StatusCreated {
			t.Fatalf("unlimited POST %d = %d, want 201", i+1, rr.Code)
		}
	}
}

func TestBlockedCIDRAllRoutes(t *testing.T) {
	cfg := loadCfg(t, map[string]any{"rate_limit": map[string]any{
		"default": 60,
		"rules":   map[string]string{"192.0.2.0/24": "-"},
	}})
	srv := New(cfg, memStore(t), []byte(testIndex))

	targets := []struct{ method, path, body string }{
		{"GET", "/", ""},
		{"POST", "/api/secrets", postBody(validSecret, validIV, 24)},
		{"GET", "/api/secrets/01234567-89ab-4cde-8f01-23456789abcd", ""},
		{"GET", "/anything/else", ""}, // enforced before routing
	}
	for _, tc := range targets {
		rr := do(t, srv, tc.method, tc.path, "192.0.2.77:9999", tc.body, nil)
		wantError(t, rr, http.StatusForbidden, msgBlocked)
	}

	// An address outside the blocked CIDR passes.
	if rr := do(t, srv, "GET", "/", "198.51.100.1:1", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("unblocked GET / = %d, want 200", rr.Code)
	}
}

func TestLongestPrefixOverride(t *testing.T) {
	cfg := loadCfg(t, map[string]any{"rate_limit": map[string]any{
		"default": 60,
		"rules": map[string]string{
			"10.0.0.0/8":  "1",
			"10.1.0.0/16": "0",
		},
	}})
	srv := New(cfg, memStore(t), []byte(testIndex))
	body := postBody(validSecret, validIV, 24)

	// 10.1.x.x: the more specific /16 "0" (unlimited) wins over the /8 "1".
	for i := 0; i < 3; i++ {
		rr := do(t, srv, "POST", "/api/secrets", "10.1.2.3:1", body, nil)
		if rr.Code != http.StatusCreated {
			t.Fatalf("override POST %d = %d, want 201", i+1, rr.Code)
		}
	}
	// 10.2.x.x: only the /8 matches → limit 1.
	if rr := do(t, srv, "POST", "/api/secrets", "10.2.3.4:1", body, nil); rr.Code != http.StatusCreated {
		t.Fatalf("first /8 POST = %d, want 201", rr.Code)
	}
	rr := do(t, srv, "POST", "/api/secrets", "10.2.3.4:1", body, nil)
	wantError(t, rr, http.StatusTooManyRequests, msgRateLimited)
}

// --- trusted-proxy client IP resolution -------------------------------------

func TestXForwardedForResolution(t *testing.T) {
	newSrv := func(t *testing.T) *Server {
		cfg := loadCfg(t, map[string]any{
			"trusted_proxies": []string{"127.0.0.0/8"},
			"rate_limit": map[string]any{
				"default": 60,
				"rules": map[string]string{
					"203.0.113.0/24": "-",
					"127.0.0.3/32":   "-",
					"127.0.0.1/32":   "-",
				},
			},
		})
		return New(cfg, memStore(t), []byte(testIndex))
	}

	t.Run("XFF honored from trusted peer", func(t *testing.T) {
		rr := do(t, newSrv(t), "GET", "/", "127.0.0.2:5000", "", map[string]string{"X-Forwarded-For": "203.0.113.7"})
		wantError(t, rr, http.StatusForbidden, msgBlocked)
	})

	t.Run("XFF ignored from untrusted peer", func(t *testing.T) {
		rr := do(t, newSrv(t), "GET", "/", "198.51.100.9:5000", "", map[string]string{"X-Forwarded-For": "203.0.113.7"})
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (XFF must be ignored)", rr.Code)
		}
	})

	t.Run("right-to-left walk skips trusted hops", func(t *testing.T) {
		rr := do(t, newSrv(t), "GET", "/", "127.0.0.2:5000", "", map[string]string{"X-Forwarded-For": "203.0.113.7, 127.0.0.2"})
		wantError(t, rr, http.StatusForbidden, msgBlocked)
	})

	t.Run("all hops trusted uses leftmost", func(t *testing.T) {
		rr := do(t, newSrv(t), "GET", "/", "127.0.0.2:5000", "", map[string]string{"X-Forwarded-For": "127.0.0.3, 127.0.0.2"})
		wantError(t, rr, http.StatusForbidden, msgBlocked)
	})

	t.Run("unparsable XFF falls back to peer", func(t *testing.T) {
		// Peer 127.0.0.1 is itself blocked; garbage XFF must not launder it.
		rr := do(t, newSrv(t), "GET", "/", "127.0.0.1:5000", "", map[string]string{"X-Forwarded-For": "garbage"})
		wantError(t, rr, http.StatusForbidden, msgBlocked)
	})

	t.Run("no XFF from trusted peer uses peer", func(t *testing.T) {
		rr := do(t, newSrv(t), "GET", "/", "127.0.0.1:5000", "", nil)
		wantError(t, rr, http.StatusForbidden, msgBlocked)
	})

	t.Run("rate limit keys on resolved client", func(t *testing.T) {
		cfg := loadCfg(t, map[string]any{
			"trusted_proxies": []string{"127.0.0.0/8"},
			"rate_limit":      map[string]any{"default": 1},
		})
		srv := New(cfg, memStore(t), []byte(testIndex))
		body := postBody(validSecret, validIV, 24)

		if rr := do(t, srv, "POST", "/api/secrets", "127.0.0.2:1", body, map[string]string{"X-Forwarded-For": "198.51.100.1"}); rr.Code != http.StatusCreated {
			t.Fatalf("client 1 POST = %d, want 201", rr.Code)
		}
		// Different forwarded client through the same proxy: own budget.
		if rr := do(t, srv, "POST", "/api/secrets", "127.0.0.2:1", body, map[string]string{"X-Forwarded-For": "198.51.100.2"}); rr.Code != http.StatusCreated {
			t.Fatalf("client 2 POST = %d, want 201", rr.Code)
		}
		// Same forwarded client again: budget spent.
		rr := do(t, srv, "POST", "/api/secrets", "127.0.0.2:1", body, map[string]string{"X-Forwarded-For": "198.51.100.1"})
		wantError(t, rr, http.StatusTooManyRequests, msgRateLimited)
	})
}
