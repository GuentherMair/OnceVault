# STEP 6 — Cleanup, Embed, Documentation, E2E (wave 3 — requires all previous steps)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md); all code from waves 1–2 integrated and green.
Owns: `main.go` cleanup branch + go:embed wiring, `config.example.yaml`,
`config.example.json`, `README.md`, `INSTALL.md`, `CLAUDE.md`, E2E verification.

## Code

- `//go:embed web/index.html` in main.go (or a tiny `web.go`); pass bytes to `server.New`; remove the step-5 placeholder.
- `cleanup` subcommand: load config → `store.Open` → `PurgeExpired` (log count) → `Vacuum` → close. Exit 0 on success, 1 with stderr detail on failure. Redis: both are no-ops — log that TTL handles it.

## config.example.yaml / .json
Full annotated example per contract 0.5. YAML comments must include the
`max_secret_bytes` pros/cons (1 KiB strict abuse-protection / 16 KiB balanced default /
64 KiB permissive with higher storage-abuse ceiling) and DSN examples for all 4 drivers.
JSON file mirrors the YAML values (JSON has no comments — point to the YAML in README).

## README.md
Short and to the point: product blurb (contract 0.1), how it works in 4 bullets
(browser-side AES-256-GCM, fragment key, read-once, expiry), the zero-knowledge
guarantee, screenshot placeholder optional. Finish with an **"Interested?"** section
pointing to [INSTALL.md](INSTALL.md). Note: intro text is the same content as the
frontend help dialog — keep them consistent.

## INSTALL.md
1. **Build**: go build (Go ≥1.22), resulting single binary.
2. **Configure**: config file walk-through, key-by-key, both formats.
3. **Databases** — one sub-section each with setup scripts:
   - sqlite: directory/permissions, DSN, no server needed.
   - redis: minimal redis.conf notes (requirepass, maxmemory-policy noeviction), DSN; document that expiry is native TTL and the "expired but unretrieved" (410) distinction does not exist on redis.
   - mysql: SQL script — CREATE DATABASE oncevault, CREATE USER + GRANT, the contract 0.4 CREATE TABLE; DSN.
   - postgres: SQL script — CREATE DATABASE/ROLE/GRANT + contract 0.4 CREATE TABLE; DSN.
4. **Run as a service (Debian/Ubuntu)**: systemd unit (dedicated `oncevault` user, `ProtectSystem=strict`, `ReadWritePaths` for the sqlite dir, Restart=on-failure), install steps, `systemctl enable --now`.
5. **Cron cleanup**: `*/15 * * * * oncevault /usr/local/bin/oncevault -config /etc/oncevault/config.yaml cleanup`.
6. **Reverse proxy** (TLS termination; Web Crypto requires a secure context):
   - nginx: server block with TLS + `proxy_pass http://127.0.0.1:8420`, `X-Forwarded-For` setup matching `trusted_proxies`.
   - apache: VirtualHost with `ProxyPass`/`ProxyPassReverse` + `RemoteIPHeader` notes.

## CLAUDE.md
Thin: one-paragraph purpose, architecture map (the tree from docs/PLAN.md), the 4
security invariants, build/test commands, pointer to `docs/` for everything else.

## E2E verification (scripted, sqlite)
Build binary → start with temp sqlite config → via curl:
POST valid (Go-generated AES-256-GCM-compatible ciphertext) → 201; GET once → 200 and
ciphertext round-trip-decrypts in Go with the kept key; GET again → 404; expired fixture
(direct DB insert with past expires) → 410; malformed guid → 404; unknown route → 404;
oversize body → 400; duration 3 → 400 exact string; burst past a small configured limit
→ 429; `cleanup` subcommand runs clean. Then manual browser checklist from
[PLAN.md](PLAN.md).

## Re-verify before finishing
All 4 security invariants; `go build ./... && go vet ./... && go test ./...` green;
`curl` of every undefined route → 404; README help-dialog consistency.
