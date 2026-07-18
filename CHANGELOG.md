# Changelog

All notable changes to OnceVault are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.4] - 2026-07-18

### Added

- Keyboard shortcuts, active while the input field is focused:
  - `F1` opens the help dialog (universal Help key, no browser conflict).
  - `Cmd/Ctrl-G` generates a random secret in the input (same action as the
    Generate button); held-key repeat is suppressed to avoid overwriting.
  - `Cmd/Ctrl-Shift-C` copies the full input contents to the clipboard
    (independent of any text selection; uses the same check-mark feedback as
    the in-pill copy button).
  - `Cmd/Ctrl-Shift-T` cycles the theme through system → dark → light → system
    (same action as the theme button); overrides the browser's reopen-closed-
    tab shortcut on the same combo.
- The modifier key is `metaKey || ctrlKey`, so macOS users press `Cmd` and
  everyone else presses `Ctrl` — no per-platform fork needed.
- The existing Generate, in-pill Copy, and Theme button handlers were lifted
  into named functions (`doGenerate`, `copyInputAll`, `cycleTheme`) so the
  click and key bindings call the exact same code path.

## [1.0.3] - 2026-07-18

### Added

- In-pill copy button (first icon, front-left) that copies the typed secret,
  with the same check-mark feedback as the output panel's copy action.
- "Generate secret" button next to Encrypt, producing a strong random secret
  directly in the input field; localized in all 25 languages.
- Embedded "OnceVault Disc" mask font (`tools/gen_maskfont`, stdlib-only,
  deterministic, 968 bytes as a data: URI): every Unicode codepoint renders as
  one centered disc at a single fixed advance.

### Changed

- The input pill now adapts its layout: when the secret no longer fits on one
  row between the icons (or contains newlines), the textarea expands to a
  full-width row and copy/eye/duration move to a second row — deleting back to
  a fitting single line restores the compact pill; the second row keeps the
  same distance from the pill's bottom border as in the one-line layout.
- Masking now works "by identity": once the disc font is confirmed loaded, the
  masked textarea shows its own glyphs — every character (spaces included) is
  a disc, so caret, selection, and soft-wrap positions always match the dots
  exactly, for any content, at any width (this also removes the former
  CJK/emoji double-width edge case). If the font cannot load, the previous
  dot-mirror overlay remains as the fallback, so a missing font can never
  reveal plaintext.
- Content-Security-Policy additionally allows `font-src data:` for exactly
  this one embedded asset; the page remains free of external requests.

## [1.0.2] - 2026-07-18

### Added

- Localized "link incomplete — decryption key missing" error when a share link
  arrives without its `#fragment` (mail clients strip fragments); no request is
  made, so a keyless visit never consumes the secret.
- Security headers served by the binary: `X-Content-Type-Options: nosniff` on
  every response; a strict `Content-Security-Policy` and `X-Frame-Options: DENY`
  on the page.
- Redis backend refuses servers older than 6.2 at startup (retrieval needs
  `GETDEL`); requirement documented in INSTALL.md.
- The favicon generator is now part of the repository
  (`tools/gen_favicon`, stdlib-only, deterministic); `web/favicon.ico`
  regenerated from it.
- SHIFT+ENTER inserts a newline, same as ALT+ENTER.

### Changed

- An error panel (network failure, rate limit, backend error) no longer clears
  the typed secret — only a successful encrypt/decrypt does.
- Retrieval (`GET /api/secrets/{guid}`) now shares the POST rate-limit budget
  (contract amendment 2026-07-18), closing a DB-load amplification vector.
- The rate limiter buckets IPv6 clients by /64, so one subscriber cannot mint
  unlimited limiter entries by rotating addresses.
- MySQL `TakeOnce` runs at READ COMMITTED, avoiding gap-lock deadlocks when
  absent GUIDs are probed concurrently.
- `max_secret_bytes` is now capped at 16 MiB at config load (the server buffers
  up to ~4/3 of it per request); `listen` is validated as host:port at load; a
  match-everything `trusted_proxies` prefix (`/0`) logs a loud spoofing warning.
- A request body exceeding the size cap now reports the contract's
  "secret exceeds maximum allowed size" instead of "invalid JSON request body".
- `Referrer-Policy: no-referrer` is served on every response (previously only a
  meta tag).
- The share URL (GUID and key fragment) is stripped from the address bar before
  the retrieval request fires, not after it completes.
- Graceful shutdown now waits for an in-flight opportunistic purge before
  closing the store (no more spurious purge warnings on clean shutdowns).
- SQLite duplicate detection matches the primary-key/unique extended error
  codes precisely instead of every constraint class.
- **API wording change**: the 404 retrieval error string is now
  `secret expired or was already retrieved` (formerly "…was already burned") —
  part of removing the burn metaphor product-wide (UI, docs, and wire strings;
  frontend and backend ship together in one binary, so no compatibility skew).
- Every new input cycle starts masked: closing the output panel resets the eye
  toggle.
- The stored theme is applied before first paint (no wrong-theme flash on load).
- README notes that all non-English translations are machine-generated.

## [1.0.1] - 2026-07-18

### Changed

- Wordmark: the "Once" in "OnceVault" is now grey with a left-to-right fade-in
  opacity gradient.
- Duration dropdown labels use a thin readable spacing ("1 h" instead of "1h").
- Documentation updates and cleanup: docs now reflect the as-built state
  (build-tag driver registry, favicon route, Go 1.26.5, systemd unit variant for
  serverless-storage backends); the post-initial-request additions moved from
  `docs/INITIAL_REQUEST.md` into the new `docs/FINETUNING.md`.

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

[1.0.4]: https://github.com/GuentherMair/OnceVault/releases/tag/v1.0.4
[1.0.3]: https://github.com/GuentherMair/OnceVault/releases/tag/v1.0.3
[1.0.2]: https://github.com/GuentherMair/OnceVault/releases/tag/v1.0.2
[1.0.1]: https://github.com/GuentherMair/OnceVault/releases/tag/v1.0.1
[1.0.0]: https://github.com/GuentherMair/OnceVault/releases/tag/v1.0.0
