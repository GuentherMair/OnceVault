// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

// Package server implements the OnceVault HTTP API per the frozen contract
// in docs/PLAN_STEP_0.md §0.2: four routes, exact error strings, no-store
// caching, trusted-proxy-aware client IPs, and CIDR blocking on all routes.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"oncevault/config"
	"oncevault/store"
)

// Contract error strings (§0.2) — byte-exact, do not edit.
const (
	msgNotFound    = "not found"
	msgInvalidJSON = "invalid JSON request body"
	msgSecretEmpty = "secret must be non-empty base64"
	msgSecretSize  = "secret exceeds maximum allowed size"
	msgBadIV       = "iv must be base64 encoding exactly 12 bytes"
	msgBadDuration = "duration must be one of: 1, 4, 8, 24, 48, 120, 168"
	msgBlocked     = "access blocked"
	msgRateLimited = "rate limit exceeded, try again later"
	msgBackend     = "unexpected backend failure"
	// msgTaken wording amended 2026-07-18 (contract §0.2 amendment note).
	msgTaken   = "secret expired or was already retrieved"
	msgExpired = "secret expired before it was retrieved"
)

const (
	purgeIntervalSeconds = 60 // opportunistic purge runs at most once per minute
	purgeTimeout         = 30 * time.Second
)

var (
	guidRe         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	validDurations = map[int]bool{1: true, 4: true, 8: true, 24: true, 48: true, 120: true, 168: true}
)

// Server is the OnceVault HTTP handler: panic recovery → client-IP
// resolution → blocked-CIDR check → strict four-route mux.
type Server struct {
	cfg     *config.Config
	st      store.Store
	index   []byte
	favicon []byte
	mux     *http.ServeMux
	limiter *Limiter
	handler http.Handler
	// lastPurge is the unix second of the last purge trigger (CAS-guarded).
	lastPurge atomic.Int64
	// purgeWG tracks in-flight opportunistic purges so shutdown can wait for
	// them before the store is closed.
	purgeWG sync.WaitGroup
}

// New builds the full middleware+mux chain. index is the frontend page bytes,
// favicon the embedded ICO bytes.
func New(cfg *config.Config, st store.Store, index, favicon []byte) *Server {
	s := &Server{cfg: cfg, st: st, index: index, favicon: favicon, limiter: NewLimiter()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	mux.HandleFunc("POST /api/secrets", s.handleCreate)
	mux.HandleFunc("GET /api/secrets/{guid}", s.handleTake)
	s.mux = mux

	var h http.Handler = http.HandlerFunc(s.route)
	h = s.blockMiddleware(h)
	h = s.ipMiddleware(h)
	h = s.recoverMiddleware(h)
	s.handler = h
	return s
}

// ServeHTTP dispatches through the composed middleware chain.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// route serves only the three registered patterns; any request the mux would
// reject (unknown path, method mismatch) collapses to a uniform JSON 404 so
// route/method probing yields no enumeration hints.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if _, pattern := s.mux.Handler(r); pattern == "" {
		writeError(w, http.StatusNotFound, msgNotFound)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// recoverMiddleware is outermost: it stamps Cache-Control on every response
// and converts handler panics into the contract 500 JSON.
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic in handler", "panic", rec, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, msgBackend)
			}
		}()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type ipKey struct{}

// ipMiddleware resolves the client IP once and stashes it in the context.
func (s *Server) ipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.resolveClientIP(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ipKey{}, ip)))
	})
}

// blockMiddleware enforces "-" CIDR rules on ALL routes, before routing.
func (s *Server) blockMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, blocked := s.cfg.LimitFor(clientIP(r.Context())); blocked {
			writeError(w, http.StatusForbidden, msgBlocked)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the resolved client address stashed by ipMiddleware.
func clientIP(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(ipKey{}).(netip.Addr)
	return ip
}

// resolveClientIP implements contract §0.5: the TCP peer is authoritative
// unless it is a trusted proxy, in which case X-Forwarded-For is walked
// right→left and the first untrusted hop wins (all trusted → leftmost;
// any unparsable hop → fall back to the peer).
func (s *Server) resolveClientIP(r *http.Request) netip.Addr {
	peer := parseAddrLoose(r.RemoteAddr)
	if !peer.IsValid() || !inNets(s.cfg.TrustedProxyNets, peer) {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	leftmost := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a := parseAddrLoose(strings.TrimSpace(hops[i]))
		if !a.IsValid() {
			return peer
		}
		if !inNets(s.cfg.TrustedProxyNets, a) {
			return a
		}
		leftmost = a
	}
	return leftmost
}

// parseAddrLoose accepts "ip" or "ip:port" (as in RemoteAddr); zero Addr on failure.
func parseAddrLoose(s string) netip.Addr {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap()
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap()
	}
	return netip.Addr{}
}

func inNets(nets []netip.Prefix, a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// indexCSP locks the page down to its own inline script/style and same-origin
// fetch/favicon — the frontend loads nothing external by design (invariant 3),
// so everything else can be denied outright.
const indexCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", indexCSP)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write(s.index)
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Write(s.favicon)
}

type createRequest struct {
	Secret   string `json:"secret"`
	IV       string `json:"iv"`
	Duration int    `json:"duration"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r.Context())
	limit, _ := s.cfg.LimitFor(ip)
	if !s.limiter.Allow(ip, limit) {
		writeError(w, http.StatusTooManyRequests, msgRateLimited)
		return
	}

	// Body cap per §0.2: ceil(max_secret_bytes*4/3) + 1024.
	bodyCap := (int64(s.cfg.MaxSecretBytes)*4+2)/3 + 1024
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bodyCap))
	// A body over the cap can only mean an oversized secret — report it with
	// the contract's size error, not as malformed JSON (amendment 2026-07-18).
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusBadRequest, msgSecretSize)
		return
	}
	var req createRequest
	if err != nil || firstNonSpace(body) != '{' || json.Unmarshal(body, &req) != nil {
		writeError(w, http.StatusBadRequest, msgInvalidJSON)
		return
	}
	secret, err := base64.StdEncoding.DecodeString(req.Secret)
	if err != nil || len(secret) == 0 {
		writeError(w, http.StatusBadRequest, msgSecretEmpty)
		return
	}
	if len(secret) > s.cfg.MaxSecretBytes {
		writeError(w, http.StatusBadRequest, msgSecretSize)
		return
	}
	iv, err := base64.StdEncoding.DecodeString(req.IV)
	if err != nil || len(iv) != 12 {
		writeError(w, http.StatusBadRequest, msgBadIV)
		return
	}
	if !validDurations[req.Duration] {
		writeError(w, http.StatusBadRequest, msgBadDuration)
		return
	}

	expires := time.Now().UTC().Unix() + int64(req.Duration)*3600
	guid, err := newGUID()
	if err == nil {
		// Ciphertext and IV are stored as the received base64 text (§0.4).
		err = s.st.Put(r.Context(), guid, req.Secret, req.IV, expires)
		if errors.Is(err, store.ErrDuplicate) {
			if guid, err = newGUID(); err == nil {
				err = s.st.Put(r.Context(), guid, req.Secret, req.IV, expires)
			}
		}
	}
	if err != nil {
		slog.Error("secret store failed", "error", err)
		writeError(w, http.StatusInternalServerError, msgBackend)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		GUID string `json:"guid"`
	}{guid})
}

func (s *Server) handleTake(w http.ResponseWriter, r *http.Request) {
	// Amendment 2026-07-18: retrieval shares the POST rate-limit budget so the
	// unmetered GET path cannot be used as a free DB-load amplifier.
	ip := clientIP(r.Context())
	limit, _ := s.cfg.LimitFor(ip)
	if !s.limiter.Allow(ip, limit) {
		writeError(w, http.StatusTooManyRequests, msgRateLimited)
		return
	}

	guid := r.PathValue("guid")
	if !guidRe.MatchString(guid) {
		// Short-circuit: no DB hit, and the same 404 as a retrieved secret
		// so malformed guids reveal nothing about the format.
		writeError(w, http.StatusNotFound, msgTaken)
		return
	}
	secret, iv, err := s.st.TakeOnce(r.Context(), guid)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, struct {
			Secret string `json:"secret"`
			IV     string `json:"iv"`
		}{secret, iv})
	case errors.Is(err, store.ErrExpired):
		writeError(w, http.StatusGone, msgExpired)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgTaken)
	default:
		slog.Error("secret take failed", "error", err)
		writeError(w, http.StatusInternalServerError, msgBackend)
	}
	s.maybePurge()
}

// maybePurge fires PurgeExpired asynchronously at most once per minute.
// The CAS on the trigger timestamp guarantees a single winner per window;
// failures are logged and never surfaced to any request.
func (s *Server) maybePurge() {
	now := time.Now().Unix()
	last := s.lastPurge.Load()
	if now-last < purgeIntervalSeconds {
		return
	}
	if !s.lastPurge.CompareAndSwap(last, now) {
		return
	}
	st := s.st
	s.purgeWG.Add(1)
	go func() {
		defer s.purgeWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), purgeTimeout)
		defer cancel()
		if _, err := st.PurgeExpired(ctx); err != nil {
			slog.Warn("opportunistic purge failed", "error", err)
		}
	}()
}

// Wait blocks until any in-flight opportunistic purge has finished. Call it
// after the HTTP server has shut down and before closing the store, so a
// purge started by a late request never hits a closed store.
func (s *Server) Wait() {
	s.purgeWG.Wait()
}

// newGUID hand-rolls a UUIDv4 from crypto/rand with RFC 4122 version/variant bits.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func firstNonSpace(b []byte) byte {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return c
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{msg})
}
