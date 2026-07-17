# STEP 3 — Redis Store (wave 1)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) sections 0.3/0.4 (binding).
Owns: `store/redis.go` only (replace the stub). Do NOT modify store.go or the SQL
files; go.mod/go.sum frozen. No integration tests (no redis in the test environment) —
code must be review-clean.

## Requirements

- `NewRedis(dsn)` — parse via `redis.ParseURL` (`redis://[:password@]host:port/db`), ping on construction.
- Key layout per contract 0.4: `oncevault:{guid}` → JSON `{"secret":"…","iv":"…"}`.
- `Put`: `SET key val EX <expires-now_seconds> NX`; NX-miss → `ErrDuplicate`; computed TTL ≤ 0 → treat as immediate expiry (skip write, return nil — the secret is born dead, GET will 404). TTL derives from the passed `expires` epoch minus current time.
- `TakeOnce`: `GETDEL` (atomic, Redis ≥6.2). `redis.Nil` → `ErrNotFound`. **Never returns ErrExpired** — native TTL means expired keys are simply absent (contract: 410 never occurs on redis; documented in INSTALL.md by step 6).
- `PurgeExpired` → `(0, nil)`; `Vacuum` → `nil` (both no-ops; one invariant comment each explaining native TTL).
- All calls take the passed context; JSON marshal/unmarshal via encoding/json.

## Re-verify before finishing
Invariants in [PLAN.md](PLAN.md); `go build ./store && go vet ./store` green; no new deps; `oncevault:` key prefix exact.
