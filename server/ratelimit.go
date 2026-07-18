// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

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
// It meters POST /api/secrets and GET /api/secrets/{guid} against one shared
// budget (amendment 2026-07-18); blocking ("-") and unlimited ("0") rules are
// resolved by the caller via config.LimitFor.
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

// limiterKey buckets IPv6 clients by their /64: a single subscriber typically
// controls an entire /64, so per-address windows would let one attacker mint
// unlimited fresh limiter entries by rotating addresses. IPv4 keys stay
// per-address. Rule matching (LimitFor) still sees the full address.
func limiterKey(ip netip.Addr) netip.Addr {
	if !ip.IsValid() || ip.Is4() {
		return ip
	}
	p, err := ip.Prefix(64)
	if err != nil {
		return ip
	}
	return p.Addr()
}

// Allow records one metered request from ip and reports whether it is within
// limit per minute. limit <= 0 means unlimited and records nothing.
func (l *Limiter) Allow(ip netip.Addr, limit int) bool {
	if limit <= 0 {
		return true
	}
	ip = limiterKey(ip)
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
