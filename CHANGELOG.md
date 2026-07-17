# Changelog

All notable changes to OnceVault are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-07-18

Initial release.

### Added

**Core — zero-knowledge, read-once secret sharing**
- All encryption and decryption happen client-side in the browser (Web Crypto API,
  AES-256-GCM, 256-bit key, random 12-byte IV); the server only ever sees ciphertext.
- The decryption key travels solely in the share link's `#fragment`
  (`/?guid={UUID}#{base64url-key}`), which browsers never transmit; retrieval uses the
  GUID only.
- Read-once semantics: the first retrieval atomically fetches and deletes the secret;
  every later attempt gets an error, never the secret.
- Selectable expiry from 1 hour to 7 days; expired secrets are purged unread —
  opportunistically while serving (throttled, async) and via a cron-callable `cleanup`
  subcommand with per-backend vacuum.

**Backend (single Go binary, stdlib HTTP)**
- Four routes only — `GET /`, `GET /favicon.ico`, `POST /api/secrets`,
  `GET /api/secrets/{guid}` — everything else answers a uniform JSON 404; panic-proof
  handlers with exact, contract-frozen error strings and semantic status codes
  (201/400/403/404/410/429/500).
- Four storage backends behind build tags for slim binaries (`driver_sqlite`,
  `driver_redis`, `driver_mysql`, `driver_postgres`, `driver_all`): CGO-free SQLite
  (`DELETE…RETURNING`), Redis (native TTL, `SET NX EX` + `GETDEL`), MySQL/MariaDB
  (single-transaction `SELECT…FOR UPDATE` + `DELETE`), PostgreSQL (`DELETE…RETURNING`);
  expiry comparison always happens inside the database engine.
- JSON or YAML configuration (chosen by file extension): listen address, backend DSN,
  `max_secret_bytes` cap, trusted proxies, and CIDR rate-limit rules — per-CIDR
  POST-per-minute limits, `"0"` unlimited, `"-"` blocks all routes,
  longest-prefix match, safe fallback when `default: 0`.
- Trusted-proxy-aware client IP resolution (right-to-left `X-Forwarded-For` walk),
  fixed 1-minute rate-limit windows per IP, `Cache-Control: no-store` on every
  response, hand-rolled UUIDv4 from `crypto/rand`.
- Embedded vault-wheel favicon (PNG-in-ICO 16/32/48 px, procedurally generated,
  served byte-identical from the binary).

**Frontend (one self-contained embedded HTML file)**
- Google-style minimal UI: centered wordmark, pill-shaped input row with in-pill
  duration dropdown, tri-state output panel (yellow share link / green decrypted
  secret / red error) with copy and close actions, system→dark→light theme cycler,
  help dialog.
- Multi-line masked secret input: auto-grows to 15 lines then scrolls, ENTER encrypts,
  ALT+ENTER inserts a newline; masking via a dot-mirror overlay that preserves line
  structure, with caret/selection alignment guaranteed by a monospace masked state;
  eye toggle reveals/hides.
- Internationalization: 25 languages — English (default and fallback), all other
  official EU languages, and Mandarin Chinese; starts in the browser's language,
  switchable via a globe picker (first top-right icon), choice persisted; server
  error strings localized client-side via the frozen contract strings.
- Autofill/password-manager suppression, no cookies, zero external assets or
  requests (inline SVG icons; localStorage only for theme and language choice).

**Deployment & docs**
- `INSTALL.md`: build (incl. per-driver build tags and binary sizes), hardened
  configuration with a dedicated system user and restrictive permissions, database
  setup scripts for all four backends, systemd unit with sandboxing, cron cleanup,
  and nginx/apache TLS reverse-proxy examples.
- Frozen design contracts and step plans preserved under `docs/`
  (`INITIAL_REQUEST.md`, `GRILLING.md`, `PLAN.md`, `PLAN_STEP_0…6.md`).
- Unit tests for config parsing, rate limiting, the full POST/GET validation and
  outcome matrices, read-once atomicity, and purge throttling.

**Licensing**
- MIT License; SPDX headers in all source files; provenance note — built entirely
  with LLM tooling (Anthropic Claude and MiniMax models).

[1.0.0]: https://github.com/GuentherMair/OnceVault/releases/tag/v1.0.0
