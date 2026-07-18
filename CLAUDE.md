# CLAUDE.md

OnceVault is a zero-knowledge, read-once secret sharing service: the browser encrypts
with AES-256-GCM (Web Crypto), the server stores only ciphertext under a random GUID
with an expiry, the key travels solely in the share link's `#fragment`, and the first
retrieval atomically fetches and deletes the secret. Single Go binary (stdlib HTTP),
one embedded HTML page plus embedded favicon, four storage backends.

## Architecture

```
OnceVault/
├── main.go                   # flags: -config; subcommands: serve (default) | cleanup; go:embed web assets
├── config/config.go          # JSON/YAML load, validation, rate-limit default fallback (0 → warn+6000)
├── store/store.go            # Store interface, sentinel errors, init()-populated registry, Open() dispatch
├── store/sqlite.go           # +build driver_sqlite; modernc.org/sqlite (CGO-free); DELETE…RETURNING w/ DB-side expiry check
├── store/mysql.go            # +build driver_mysql;  go-sql-driver/mysql; SELECT…FOR UPDATE + DELETE in one tx
├── store/postgres.go         # +build driver_postgres; jackc/pgx (stdlib database/sql mode); DELETE…RETURNING
├── store/redis.go            # +build driver_redis; go-redis; SET w/ TTL; atomic GETDEL take
├── server/server.go          # strict mux, panic recovery, store-error → status mapping, throttled purge
├── server/ratelimit.go       # CIDR rules, fixed 1-min window per IP, trusted_proxies resolution
├── web/index.html            # ONE self-contained file (HTML+CSS+JS, inline SVG), go:embed
├── web/favicon.ico           # vault-wheel icon (PNG-in-ICO 16/32/48), generated, go:embed
├── tools/gen_favicon/        # deterministic stdlib-only favicon generator (go run ./tools/gen_favicon)
├── config.example.yaml       # commented, incl. max_secret_bytes pros/cons
├── config.example.json
├── README.md / INSTALL.md / CHANGELOG.md
└── docs/                     # frozen contracts and step plans — read before changing anything
```

## Security invariants — re-verify after every change

1. All encryption/decryption client-side (Web Crypto API, AES-256-GCM, 256-bit key, random 12-byte IV).
2. Plaintext and key never leave the browser; key only ever in the `#fragment`; retrieval GET uses the GUID only.
3. Minimal supply chain: frontend has **zero** external assets/libraries (inline SVG, no CDN); backend deps limited to 4 DB drivers + yaml.v3; UUIDv4 hand-rolled from `crypto/rand`.
4. Only defined routes served — `GET /` (exact), `GET /favicon.ico`, `POST /api/secrets`, `GET /api/secrets/{guid}`; everything else 404.

## Build / test

```sh
go build ./... && go vet ./... && go test -tags driver_all ./... -count=1
# driver_all selects all four storage drivers; without it every test file is
# silently skipped (they are build-tag-gated). See INSTALL.md §7 for slimmer
# per-driver binaries.
gofmt -l .        # must print nothing
go build -tags driver_all -o oncevault . && ./oncevault -config config.yaml   # serve; `cleanup` purges+vacuums
```

Everything else — API contract with exact error strings, config schema, DB schemas,
frontend crypto contract, per-step plans — lives in `docs/` (start at `docs/PLAN.md`
and `docs/PLAN_STEP_0.md`). Those contracts are frozen; do not change them casually.
