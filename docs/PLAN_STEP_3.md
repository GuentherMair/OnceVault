# STEP 3 — Redis Store (wave 1)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) sections 0.3/0.4 (binding).
Owns: `store/redis.go` only (replace the stub). Do NOT modify store.go or the SQL
files; go.mod/go.sum frozen. No integration tests (no redis in the test environment) —
code must be review-clean.

## Requirements

- File MUST start with `//go:build driver_redis || driver_all` and register `NewRedis` in `store.registry["redis"]` via an `init()` (see the implementation note in `PLAN_STEP_0.md §0.3`).
- `NewRedis(dsn)` — parse via `redis.ParseURL` (`redis://[:password@]host:port/db`), ping on construction.
- Key layout per contract 0.4: `oncevault:{guid}` → JSON `{"secret":"…","iv":"…"}`.
- `Put`: `SET key val EX <expires-now_seconds> NX`; NX-miss → `ErrDuplicate`; computed TTL ≤ 0 → defensive guard, skip write, return nil (the secret is "born dead", GET will 404). In normal operation this branch is unreachable: the handler validates `duration ∈ {1,4,8,24,48,120,168}` (`server.go:52`) and `expires = time.Now().UTC().Unix() + duration*3600`, so `ttl = expires - time.Now().Unix() ≥ 3600`. The guard remains as a safety net against future validation removal or negative durations from a future API. TTL derives from the passed `expires` epoch minus current time.
- `TakeOnce`: `GETDEL` (atomic, Redis ≥6.2). `redis.Nil` → `ErrNotFound`. **Never returns ErrExpired** — native TTL means expired keys are simply absent (contract: 410 never occurs on redis; documented in INSTALL.md by step 6).
- `PurgeExpired` → `(0, nil)`; `Vacuum` → `nil` (both no-ops; one invariant comment each explaining native TTL).
- All calls take the passed context; JSON marshal/unmarshal via encoding/json.

## Re-verify before finishing
Invariants in [PLAN.md](PLAN.md); `go build ./store && go vet ./store` green; no new deps; `oncevault:` key prefix exact.
