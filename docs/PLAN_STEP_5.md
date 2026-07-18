# STEP 5 — HTTP API & Rate Limiter (wave 2 — requires steps 1 and 2 integrated)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) (binding, esp. 0.2/0.5/0.7).
Owns: `server/server.go`, `server/ratelimit.go`, `server/server_test.go`,
`server/ratelimit_test.go`, and the `serve` branch of `main.go`. go.mod/go.sum frozen.

## server/server.go

- `New(cfg *config.Config, st store.Store, index, favicon []byte) *Server` (http.Handler). `index` is the frontend bytes, `favicon` the embedded ICO bytes (both step 6 wires via `//go:embed`; tests pass placeholder byte slices).
- Go 1.22 mux: `GET /{$}` → index (text/html; charset=utf-8), `POST /api/secrets`, `GET /api/secrets/{guid}`. Anything else → `404 {"error":"not found"}` JSON. `Cache-Control: no-store` on every response.
- Middleware order: panic recovery (→ 500 backend-failure JSON, `slog.Error` with stack) → client-IP resolution → blocked-CIDR check (403 on ALL routes) → mux.
- Client IP per contract 0.5: peer from `r.RemoteAddr`; if peer ∈ `trusted_proxies`, walk `X-Forwarded-For` right→left, first hop outside trusted_proxies wins; all trusted → leftmost; unparsable → peer.
- POST handler: `http.MaxBytesReader` cap per 0.2; validation order + exact error strings per 0.2; UUIDv4 via `crypto/rand` (hand-rolled, version/variant bits set); `store.Put`; `ErrDuplicate` → regenerate once, retry; any store failure → 500 contract string (real error only to slog).
- GET handler: guid regex short-circuit → 404 burned-message; `store.TakeOnce` → 200/`ErrExpired`→410/`ErrNotFound`→404/other→500, exact strings per 0.2. After writing the response trigger the throttled purge.
- Throttled purge: atomic unix-timestamp guard (CAS), min 60s interval; runs `store.PurgeExpired` in a goroutine with its own context+timeout; failures → `slog.Warn` only. Never blocks or fails a request.

## server/ratelimit.go

- Fixed 1-minute window per resolved client IP, counting **only POST /api/secrets**.
- `Limiter` with mutex-guarded `map[netip.Addr]{windowStart, count}`; limit resolved via `config.LimitFor` (longest-prefix; unlimited/blocked conventions from step 1). Exceeded → 429 + contract string.
- Lazy pruning of stale windows (on access or when map exceeds a threshold) so memory cannot grow unbounded.

## main.go serve branch
Replace the step-1 placeholder: `store.Open(driver, dsn)` → `server.New(...)` →
`http.Server{Addr: cfg.Listen, Handler: srv}` with reasonable Read/Write timeouts →
graceful shutdown on SIGINT/SIGTERM (context, `Shutdown`, store `Close`). Index bytes:
temporary placeholder variable until step 6 wires go:embed.

## Tests (sqlite in-memory store)

- POST validation matrix: every 400 case with its exact contract error string; success → 201 + valid UUIDv4 guid.
- GET matrix: 200 (and row gone after), 410 via expired fixture row, 404 unknown guid, 404 malformed guid, 500 via a failing fake store.
- Read-once over HTTP: two sequential GETs → 200 then 404.
- Routing: `/x`, `/api`, `/api/secrets/x/y`, `PUT /api/secrets` → 404; `/` exact serves index.
- Rate limiter: n-limit hit → 429; "0" CIDR unlimited; "-" CIDR → 403 on `/` AND both APIs; default fallback; longest-prefix override; X-Forwarded-For honored only from trusted proxy peer, ignored otherwise.
- Panic recovery: handler-forced panic → 500 contract JSON.
- Purge throttle: two immediate GETs trigger ≤1 purge (fake store counts calls).

## Re-verify before finishing
Invariants in [PLAN.md](PLAN.md); `go build ./... && go vet ./... && go test ./...` green;
no new deps; error strings byte-identical to contract 0.2.
