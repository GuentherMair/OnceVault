# STEP 0 — Frozen Contracts

These contracts are binding for every implementation step. Do not change them without
updating this file and every dependent step. Written by the orchestrator; the store
interface and stubs are pre-committed so wave-1 agents work on disjoint files.

## 0.1 Product blurb (canonical)

Used verbatim (adapted for medium) by the frontend help dialog AND the README intro:

> **OnceVault** — share a secret once, then it burns.
> Your secret is encrypted **in your browser** with AES-256-GCM before anything leaves
> your machine. The server only ever stores the ciphertext under a random ID with an
> expiry you pick (1 hour to 7 days). The decryption key travels **only** in the
> `#fragment` of the share link, which browsers never send to any server — OnceVault
> couldn't read your secret even if it wanted to. The first person to open the link
> gets the secret; the ciphertext is deleted in the same moment. Expired secrets are
> deleted unread.

## 0.2 HTTP API

All responses are `Content-Type: application/json`. Only these routes exist; every other
path/method returns `404 {"error":"not found"}` (or `405` where the mux distinguishes —
prefer plain 404 to avoid enumeration hints).

### `GET /` (exact path only)
Serves the embedded `web/index.html` (`Content-Type: text/html; charset=utf-8`).
`Cache-Control: no-store` on every response (page and API).

### `GET /favicon.ico` (exact path only)
Serves the embedded `web/favicon.ico` byte-identical (`Content-Type: image/x-icon`).
The file is a PNG-in-ICO container (16/32/48 px) showing a white vault wheel (ring +
cross spokes + hub) on a rounded square in the frontend focus blue `#1a73e8`; it is
generated procedurally (stdlib-only Go script, supersampled) and committed. Linked from
`index.html` via `<link rel="icon" href="/favicon.ico">`. Amendment 2026-07-17: this is
the fourth route; it is subject to the same CIDR blocking and `no-store` as all others.

### `POST /api/secrets`
Request body (JSON, size-capped at `ceil(max_secret_bytes*4/3) + 1024` bytes via `http.MaxBytesReader`):
```json
{"secret": "<standard base64>", "iv": "<standard base64>", "duration": 24}
```
Validation order and **exact error strings**:
| Check | Failure → 400 `{"error": ...}` |
|---|---|
| body parses as JSON object | `invalid JSON request body` |
| `secret` std-base64-decodable, decoded len > 0 | `secret must be non-empty base64` |
| decoded secret ≤ `max_secret_bytes` | `secret exceeds maximum allowed size` |
| `iv` std-base64-decodable, decoded len == 12 | `iv must be base64 encoding exactly 12 bytes` |
| `duration` ∈ {1,4,8,24,48,120,168} | `duration must be one of: 1, 4, 8, 24, 48, 120, 168` |

Responses:
- `201 Created` → `{"guid":"<uuidv4>"}`
- `400 Bad Request` → table above
- `403 Forbidden` → `{"error":"access blocked"}` (CIDR rule `"-"` — applies to ALL routes, enforced before routing)
- `429 Too Many Requests` → `{"error":"rate limit exceeded, try again later"}`
- `500 Internal Server Error` → `{"error":"unexpected backend failure"}`

Storage: `expires = now_utc_epoch_seconds + duration*3600`. Insert is transactional;
on GUID unique-collision generate a fresh GUID and retry once.

### `GET /api/secrets/{guid}`
`{guid}` must match `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`;
non-matching guids short-circuit to the 404 response below (no DB hit, no format oracle).

Read-once take: fetch+delete atomically; `isexpired` derived **DB-internally**
(`expires <= now` evaluated by the DB engine, not in Go).

- `200 OK` → `{"secret":"<b64>","iv":"<b64>"}` (found, not expired — row deleted)
- `410 Gone` → `{"error":"secret expired before it was retrieved"}` (found, expired — row deleted) — **never occurs on the redis backend** (native TTL already removed it → 404)
- `404 Not Found` → `{"error":"secret expired or was already burned"}` (no row)
- `500 Internal Server Error` → `{"error":"unexpected backend failure"}` (any other condition; handler is fully panic/exception-proof)

After responding, the handler triggers the throttled opportunistic purge (≤1/min,
async, errors only logged — never surfaced).

## 0.3 Store interface (`store/store.go` — pre-written, do not modify)

```go
package store

var (
    ErrNotFound  = errors.New("secret not found")
    ErrExpired   = errors.New("secret expired")
    ErrDuplicate = errors.New("guid already exists")
)

type Store interface {
    // Put stores a secret; expires is UTC epoch seconds. ErrDuplicate on guid collision.
    Put(ctx context.Context, guid, secret, iv string, expires int64) error
    // TakeOnce atomically fetches and deletes. Returns ErrExpired if the row existed
    // but was past expiry (isexpired derived DB-side), ErrNotFound if absent.
    TakeOnce(ctx context.Context, guid string) (secret, iv string, err error)
    // PurgeExpired deletes all rows with expires <= now (DB-side comparison).
    PurgeExpired(ctx context.Context) (int64, error)
    // Vacuum reclaims space where the backend supports it; no-op otherwise.
    Vacuum(ctx context.Context) error
    Close() error
}

// Open dispatches on cfg driver: "sqlite" | "redis" | "mysql" | "postgres".
func Open(driver, dsn string) (Store, error)
```

Constructors (one per file, replacing the pre-committed stubs):
`NewSQLite(dsn)`, `NewMySQL(dsn)`, `NewPostgres(dsn)`, `NewRedis(dsn)` — each
`(*T, error)`, each `*T` satisfies `Store`.

## 0.4 DB schemas / key layout

Timestamps: UTC epoch seconds, `BIGINT`/`INTEGER`. `secret`/`iv` stored as the received
standard-base64 TEXT (no decode/re-encode).

sqlite (auto-migrated on open):
```sql
CREATE TABLE IF NOT EXISTS secrets (
  guid    TEXT PRIMARY KEY,
  secret  TEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_secrets_expires ON secrets(expires);
```
Take: `DELETE FROM secrets WHERE guid=? RETURNING secret, iv, (expires <= unixepoch())`
Purge: `DELETE FROM secrets WHERE expires <= unixepoch()` · Vacuum: `VACUUM`

postgres (auto-migrated; INSTALL.md also ships a manual script):
```sql
CREATE TABLE IF NOT EXISTS secrets (
  guid    CHAR(36) PRIMARY KEY,
  secret  TEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_secrets_expires ON secrets(expires);
```
Take: `DELETE FROM secrets WHERE guid=$1 RETURNING secret, iv, (expires <= EXTRACT(EPOCH FROM now())::bigint)`
Purge: `… expires <= EXTRACT(EPOCH FROM now())::bigint` · Vacuum: `VACUUM secrets`

mysql (auto-migrated; MEDIUMTEXT so a configured 64 KiB secret (~87 KiB base64) fits):
```sql
CREATE TABLE IF NOT EXISTS secrets (
  guid    CHAR(36) NOT NULL PRIMARY KEY,
  secret  MEDIUMTEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires BIGINT NOT NULL,
  KEY idx_secrets_expires (expires)
) ENGINE=InnoDB;
```
Take (no DELETE…RETURNING): single tx —
`SELECT secret, iv, (expires <= UNIX_TIMESTAMP()) FROM secrets WHERE guid=? FOR UPDATE`
then `DELETE FROM secrets WHERE guid=?`, commit. Rollback on any error.
Purge: `… expires <= UNIX_TIMESTAMP()` · Vacuum: `OPTIMIZE TABLE secrets`

redis: key `oncevault:{guid}`, value = JSON `{"secret":"…","iv":"…"}`,
written with `SET key val EX <duration_seconds> NX` (NX enforces guid uniqueness →
ErrDuplicate). Take: `GETDEL` (atomic). Absent key → ErrNotFound; **ErrExpired is never
returned** (native TTL). PurgeExpired → (0, nil) no-op; Vacuum → no-op.

## 0.5 Config schema

File given via `-config <path>`; `.json` → JSON, `.yaml`/`.yml` → YAML. All keys:

```yaml
listen: "127.0.0.1:8420"        # plain HTTP; TLS terminates at the reverse proxy
db:
  driver: "sqlite"              # sqlite | redis | mysql | postgres
  dsn: "/var/lib/oncevault/oncevault.db"
  # redis:    redis://[:password@]host:6379/0
  # mysql:    user:password@tcp(host:3306)/oncevault
  # postgres: postgres://user:password@host:5432/oncevault
max_secret_bytes: 16384         # decoded ciphertext cap; 1024 strict / 16384 balanced / 65536 permissive
trusted_proxies: []             # CIDRs of reverse proxies allowed to set X-Forwarded-For
rate_limit:
  default: 60                   # POST/min for unmatched addresses; 0 → startup WARNING, revert to 6000
  rules:                        # CIDR → "0" unlimited | "-" blocked (ALL routes) | "n" POST/min
    "10.0.0.0/8": "0"
    "192.0.2.0/24": "-"
```

Validation on load: driver ∈ set; dsn non-empty; max_secret_bytes ≥ 1 (0/absent →
default 16384); every rules key parses as CIDR (a bare IP is normalized to /32 or /128);
every rules value is `"0"`, `"-"`, or a positive integer string; `default` ≥ 0
(`0` → `slog.Warn` + set 6000). Longest-prefix match wins; IPv4 and IPv6 handled.

## 0.6 Frontend↔backend crypto contract

- Key: `crypto.subtle.generateKey({name:"AES-GCM",length:256}, true, ["encrypt","decrypt"])`, exported raw for the fragment.
- IV: `crypto.getRandomValues(new Uint8Array(12))`.
- API body base64: **standard** with padding. URL fragment key: **base64url, unpadded**; the decoder also accepts standard base64 (defensive).
- Share link: `${location.origin}/?guid=${guid}#${b64url_key}`.
- Decrypt-on-load: requires `?guid=` matching the UUID regex AND a fragment of 43–44 base64ish chars; GET uses **GUID only** — the fragment is never part of any request.

## 0.7 Cross-cutting rules

- Go module name: `oncevault`. Go ≥ 1.22 (uses `net/http` method+wildcard mux patterns).
- Allowed dependencies (already in go.mod — do NOT add or upgrade anything, do NOT touch go.mod/go.sum):
  `modernc.org/sqlite`, `github.com/redis/go-redis/v9`, `github.com/go-sql-driver/mysql`,
  `github.com/jackc/pgx/v5` (via `database/sql` stdlib driver), `gopkg.in/yaml.v3`.
- Logging: `log/slog` to stderr. Purge/cleanup failures: `slog.Warn`/`slog.Error` only — never in an API response.
- Comments: sparse, invariant-focused; doc-comments on exported identifiers.
- Frontend: zero external requests of any kind; inline SVG icons; no cookies; localStorage only for the theme choice and (amendment 2026-07-18) the language choice.
