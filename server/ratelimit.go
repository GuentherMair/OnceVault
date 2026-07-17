package server

import (
	"net/netip"
	"sync"
	"time"
)

const (
	windowSeconds = 60
	// pruneThreshold bounds limiter memory: once the map reaches this size,
	// stale windows are swept on the next recorded request.
	pruneThreshold = 4096
)

type window struct {
	start int64 // unix second the current window opened
	count int
}

// Limiter enforces a fixed 1-minute window per resolved client IP.
// It counts only POST /api/secrets; blocking ("-") and unlimited ("0")
// rules are resolved by the caller via config.LimitFor.
type Limiter struct {
	mu      sync.Mutex
	windows map[netip.Addr]window
	now     func() int64 // unix-seconds clock, replaceable in tests
	pruneAt int
}

// NewLimiter returns an empty limiter using the real clock.
func NewLimiter() *Limiter {
	return &Limiter{
		windows: make(map[netip.Addr]window),
		now:     func() int64 { return time.Now().Unix() },
		pruneAt: pruneThreshold,
	}
}

// Allow records one POST attempt from ip and reports whether it is within
// limit per minute. limit <= 0 means unlimited and records nothing.
func (l *Limiter) Allow(ip netip.Addr, limit int) bool {
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.windows) >= l.pruneAt {
		l.prune(now)
	}
	win := l.windows[ip]
	if now-win.start >= windowSeconds {
		win = window{start: now}
	}
	win.count++
	l.windows[ip] = win
	return win.count <= limit
}

// prune drops windows that have already ended. Caller holds mu.
func (l *Limiter) prune(now int64) {
	for ip, win := range l.windows {
		if now-win.start >= windowSeconds {
			delete(l.windows, ip)
		}
	}
}
