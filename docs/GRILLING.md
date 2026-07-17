# Grilling Q&A — Decisions (2026-07-17)

Every open design question was resolved in a one-question-at-a-time grilling session
before implementation started. The chosen answers below are binding for all steps.

| # | Question | Decision |
|---|---|---|
| 1 | Backend language/stack | **Go** — stdlib `net/http`, no framework, single static binary, frontend embedded via `go:embed`. Deps limited to 4 DB drivers + `gopkg.in/yaml.v3`. |
| 2 | Share-link format | **Query form**: `https://HOST/?guid={GUID}#{KEY}` — root route already serves the frontend; JS reads `location.search`; fragment never reaches the server. |
| 3 | GUID format | **UUIDv4** from `crypto/rand`, canonical 36-char form, generated backend-side, unique DB constraint. |
| 4 | Config format | **Both JSON and YAML**, dispatched by file extension; commented `config.example.yaml` + equivalent `config.example.json` shipped. |
| 5 | Cleanup invocation | **CLI subcommand**: `oncevault -config X cleanup` — nothing extra exposed over the network. |
| 6 | Client IP for rate limiting | **Configurable trusted proxies** (`trusted_proxies: [CIDR]`, default empty). Peer inside a trusted CIDR → rightmost non-trusted `X-Forwarded-For` hop; otherwise raw peer address, headers ignored. |
| 7 | Rate-limit scope | CIDR **`"-"` blocks ALL routes (403)**; numeric limits meter **POST /api/secrets only**; `"0"` = unlimited; GET retrieval otherwise unmetered. Longest-prefix match wins. |
| 8 | Max secret size | **16 KiB ciphertext default, configurable** (`max_secret_bytes`); option pros/cons (1 KiB strict / 16 KiB balanced / 64 KiB permissive) documented as comments in the example YAML. Body capped via `http.MaxBytesReader`; IV must decode to exactly 12 bytes. |
| 9 | Redis expiry semantics | **Pure native TTL** (TTL = duration). Case 3.b ("expired, unretrieved") never occurs on Redis — expired keys report as 3.c; documented behavioral difference vs SQL backends. |
| 10 | Opportunistic purge cadence | **Throttled**: async after GET, at most once per minute (atomic timestamp guard); failures logged, never surfaced. Cron cleanup remains the thorough pass. |
| 11 | Input shape | **Single-line `<input type=password>`** pill, per spec; no textarea. |
| 12 | TLS | **Behind reverse proxy** — plain HTTP on configurable `listen:`; nginx/apache terminates TLS. Secure-context requirement (Web Crypto) documented in README + help dialog. |
| 13 | Fragment key encoding | **base64url, unpadded** (43 chars for 32 bytes); decoder defensively accepts standard base64 too. API body base64 stays standard. |
| 14 | Test depth | **Unit tests only**, sqlite in-memory (config, rate limiter, handlers, sqlite store, crypto-format vectors). redis/mysql/postgres verified by code review. |
| 15 | HTTP status codes | Semantic mapping — POST: 201 / 400 validation / 403 blocked / 429 limited. GET: 200 / 410 expired (3.b) / 404 burned-or-expired (3.c) / 500 backend (3.d). |
| 16 | Sub-agent orchestration | **Parallel waves with user checkpoints** between waves. |
| 17 | Documentation | README.md (short, "Interested?" → INSTALL.md, intro recycled as help dialog), INSTALL.md (per-DB setup + SQL scripts, Debian/Ubuntu systemd unit, apache + nginx proxy examples), thin CLAUDE.md pointing to docs/, docs/ holding INITIAL_REQUEST, GRILLING, PLAN, PLAN_STEP_0..6. |
| 18 | Code comments | **Sparse, invariant-focused** — security invariants, atomicity/DB semantics, config fallback rules only; Go doc-comments on exported identifiers. |
