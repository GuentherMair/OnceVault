# STEP 2 — SQL Stores: sqlite, mysql, postgres (wave 1)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) sections 0.3/0.4 (binding).
Owns: `store/sqlite.go`, `store/mysql.go`, `store/postgres.go`, `store/sqlite_test.go`.
`store/store.go` (interface, errors, Open dispatch) and `store/redis.go` are owned by
others — do NOT modify them; replace only the three stub files. go.mod/go.sum frozen.

## Common requirements

- Schemas/statements exactly per contract 0.4; auto-migrate (`CREATE TABLE IF NOT EXISTS…`) on construction.
- `Put`: transactional insert; map driver-specific unique-violation to `store.ErrDuplicate` (sqlite: `SQLITE_CONSTRAINT`; mysql: error 1062; postgres: SQLSTATE 23505).
- `TakeOnce`: atomic fetch+delete, `isexpired` computed **inside the SQL** (contract 0.4); expired row → delete + `ErrExpired`; no row → `ErrNotFound`.
- `PurgeExpired`/`Vacuum` per 0.4. `database/sql` with conservative pool settings (sqlite: `SetMaxOpenConns(1)` to avoid SQLITE_BUSY on the file DB).
- Drivers: `modernc.org/sqlite` (name "sqlite"), `github.com/go-sql-driver/mysql`, `github.com/jackc/pgx/v5/stdlib` (name "pgx").

## sqlite specifics
- DSN = file path or `:memory:`; enable WAL + busy_timeout pragmas for file DBs.
- `DELETE … RETURNING` requires modernc's bundled sqlite ≥3.35 — present.

## Tests (sqlite in-memory; these are the canonical Store-behavior tests)
- Put→TakeOnce round-trip; second TakeOnce → ErrNotFound.
- Expired row (expires in past) → ErrExpired once, then ErrNotFound.
- Duplicate guid → ErrDuplicate.
- PurgeExpired deletes only expired rows, returns count.
- Concurrency: 20 goroutines TakeOnce same guid → exactly 1 success, 19 ErrNotFound.
- Vacuum runs without error.

## Re-verify before finishing
Invariants in [PLAN.md](PLAN.md); `go build ./store && go vet ./store && go test ./store` green (redis stub may still be in place — that's fine, don't touch it); no new deps.
