# OnceVault — Central Implementation Plan

Derived from [INITIAL_REQUEST.md](INITIAL_REQUEST.md), refined through [GRILLING.md](GRILLING.md).
Individual build steps: [PLAN_STEP_0](PLAN_STEP_0.md) … [PLAN_STEP_6](PLAN_STEP_6.md).

## What OnceVault is

A zero-knowledge, single-use secret sharing service ("burn on read"). A user pastes a
secret into a minimal Google-style frontend; the secret is encrypted **entirely in the
browser** with AES-256-GCM via the Web Crypto API. Only ciphertext + IV + duration reach
the backend, stored under a random GUID with an expiry. The share link is
`https://HOST/?guid={UUID}#{base64url-key}` — the key lives in the URL fragment, which
browsers never transmit, so the server can never decrypt anything. Retrieval is
read-once: fetch and delete are atomic. Expired entries are purged opportunistically and
via a cron-callable cleanup subcommand.

## Security invariants — re-verify after EVERY step

1. All encryption/decryption client-side (Web Crypto API, AES-256-GCM, 256-bit key, random 12-byte IV).
2. Plaintext and key never leave the browser; key only ever in the `#fragment`; retrieval GET uses the GUID only.
3. Minimal supply chain: frontend has **zero** external assets/libraries (inline SVG icons, no CDN); backend deps limited to 4 DB drivers + yaml.v3; UUIDv4 hand-rolled from `crypto/rand`.
4. Only defined routes served — `GET /` (exact), `GET /favicon.ico`, `POST /api/secrets`, `GET /api/secrets/{guid}`; everything else 404.

## Architecture

```
OnceVault/
├── go.mod                    # module oncevault
├── main.go                   # flags: -config; subcommands: serve (default) | cleanup
├── config/config.go          # JSON/YAML load, validation, rate-limit default fallback (0 → warn+6000)
├── store/store.go            # Store interface, sentinel errors, Open() driver dispatch
├── store/sqlite.go           # modernc.org/sqlite (CGO-free); DELETE…RETURNING w/ DB-side expiry check
├── store/mysql.go            # go-sql-driver/mysql; SELECT…FOR UPDATE + DELETE in one tx
├── store/postgres.go         # jackc/pgx (stdlib database/sql mode); DELETE…RETURNING
├── store/redis.go            # go-redis; SET w/ TTL; atomic GETDEL take
├── server/server.go          # strict mux, panic recovery, store-error → status mapping, throttled purge
├── server/ratelimit.go       # CIDR rules, fixed 1-min window per IP, trusted_proxies resolution
├── web/index.html            # ONE self-contained file (HTML+CSS+JS, inline SVG), go:embed
├── web/favicon.ico           # vault-wheel icon (PNG-in-ICO 16/32/48), generated, go:embed
├── config.example.yaml       # commented, incl. max_secret_bytes pros/cons
├── config.example.json
├── README.md / INSTALL.md / CLAUDE.md
└── docs/                     # this documentation set
```

## Key decisions (binding — see GRILLING.md for rationale)

Go + stdlib HTTP · `/?guid={UUIDv4}#{base64url-key, unpadded}` · JSON/YAML config by
extension · `max_secret_bytes` default 16384 · trusted-proxy-aware client IP ·
`"-"` blocks all routes, numeric limits meter POST only, `default: 0` → warn + 6000/min ·
Redis pure native TTL (410 never occurs on Redis) · purge throttled ≤1/min async ·
cleanup CLI subcommand with per-DB vacuum · plain HTTP behind TLS-terminating reverse
proxy · multi-line masked textarea input (ENTER encrypts, ALT+ENTER newline, auto-grow
to 15 lines then scroll — see PLAN_STEP_4) · i18n: 25 languages (en default/fallback,
all other official EU languages, Mandarin), browser-language start, globe-icon picker,
localStorage persistence · sqlite-only unit tests · semantic status codes
(POST 201/400/403/429 · GET 200/410/404/500) · sparse invariant-focused comments.

## Build steps and waves

| Step | Content | Wave |
|---|---|---|
| [0](PLAN_STEP_0.md) | Frozen contracts: API shapes + codes + exact error strings, Store interface, config schema, DB schemas, product blurb | pre-written by orchestrator |
| [1](PLAN_STEP_1.md) | Scaffold + config package + main.go v1 + tests | 1 (parallel) |
| [2](PLAN_STEP_2.md) | SQL stores: sqlite/mysql/postgres + sqlite tests | 1 (parallel) |
| [3](PLAN_STEP_3.md) | Redis store | 1 (parallel) |
| [4](PLAN_STEP_4.md) | Frontend web/index.html | 1 (parallel) |
| [5](PLAN_STEP_5.md) | HTTP API + rate limiter + handler tests | 2 (needs 1,2) |
| [6](PLAN_STEP_6.md) | Cleanup subcommand, go:embed, docs set, E2E verification | 3 (needs all) |

**Checkpoint protocol:** after each wave the orchestrator integrates, runs
`go build ./... && go vet ./... && go test ./...`, re-verifies the security invariants,
reports to the user and waits for go-ahead.

## Verification

- Unit coverage: config parsing/fallbacks; CIDR limiter (0 / - / n / default / longest-prefix / trusted-proxy); POST validation matrix; GET outcome matrix (200/410/404/500); sqlite read-once atomicity (concurrent TakeOnce → exactly one winner); purge throttle.
- E2E (wave 3): start server with sqlite config → POST WebCrypto-compatible ciphertext, GET once (200), GET again (404), expired fixture (410), unknown route (404), oversize body (400), rate-limit burst (429), `cleanup` run.
- Manual browser checklist: encrypt → copy link → open in second window → green decrypt; second open → red burned error; eye toggle; ENTER-on-empty no-op; multi-line input (paste and ALT+ENTER grow the field, masked dots keep the line structure and align with the caret, 15-line cap scrolls, newlines survive encrypt→decrypt); theme cycle persistence; help dialog; copy/close; responsive shrink; devtools network tab shows no plaintext/key.
