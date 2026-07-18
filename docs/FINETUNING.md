# Additions during finetuning (post-initial-request)

The following were added after the original request and are to be treated as binding
requirements:

- the focus should always go back to the input field, whenever any action is completed
- the secret input is a **multi-line** field (a `<textarea>`, not a password `<input>`,
  which cannot hold pasted multi-line content):
  - one text line tall by default, visually identical to the original single-line pill
  - ENTER still starts the encryption procedure; ALT+ENTER inserts an actual newline
  - the field grows with its content (manual newlines or pasted multi-line text) up to
    15 lines, then scrolls
  - the eye icon still toggles masked (default) ↔ revealed content
  - the pill's corner radius stays fixed at the single-line value while the field grows
    (no ballooning fully-rounded corners)
- the app is internationalized: English (default and fallback), all other official EU
  languages, and Mandarin Chinese — 25 languages total:
  - simple per-language string storages in JavaScript, UTF-8 encoded
  - the app starts in the browser's default language (first match wins, else English)
  - the language is selectable via the common "world-grid" (globe) icon, displayed as
    the FIRST of the (then three) icons in the top-right corner of the window
- the binary is built with opt-in storage-backend drivers via Go build tags so the
  shipped artifact only contains the drivers actually used; the per-tag binary sizes
  and build commands need to reduce binary size are documented in `INSTALL.md`
- a fitting favicon is generated and shipped (generator committed as
  `tools/gen_favicon`, stdlib-only, deterministic — `go run ./tools/gen_favicon`
  rewrites `web/favicon.ico`)
- apply a left-to-right opacity gradient to the "Once" part of the "OnceVault" title

Loose-end review decisions (2026-07-18), all binding:

- an error panel (network failure, rate limit, backend error) must NOT clear the
  typed secret — only a successful encrypt/decrypt clears the input
- a share link with a valid `?guid=` but a missing/invalid `#fragment` shows a
  localized "link incomplete — key missing" error (`errKeyMissing`) and performs
  NO fetch, so a keyless visit never consumes the secret
- SHIFT+ENTER inserts a newline exactly like ALT+ENTER (plain ENTER still encrypts)
- every new input cycle starts masked: closing the output panel resets the eye
  toggle to hidden
- the stored theme is stamped on `<html>` by a tiny inline `<head>` script before
  first paint (no wrong-theme flash); the main script keeps owning theme cycling
- backend hardening: retrieval GET shares the POST rate-limit budget; the limiter
  buckets IPv6 by /64; security headers (nosniff, CSP, frame-deny) come from the
  Go binary; MySQL TakeOnce runs at READ COMMITTED; redis backend refuses servers
  older than 6.2 at startup

Security fixes (2026-07-18, second pass — triaged from a parallel LLM audit),
all binding:

- config load fails fast on `max_secret_bytes` > 16 MiB (the POST handler
  buffers ~4/3 of it in memory per request — an unbounded value would be a
  memory-abuse vector) and on a `listen` value that is not `host:port`
- a match-everything `trusted_proxies` prefix (`0.0.0.0/0` / `::/0`) loads but
  logs a loud warning: it lets every client spoof its IP via `X-Forwarded-For`,
  neutralizing block and rate-limit rules
- a request body exceeding the size cap reports the existing frozen string
  `secret exceeds maximum allowed size` (already localized), not "invalid JSON"
- `Referrer-Policy: no-referrer` is served on every response (the meta tag
  alone is not relied upon)
- the share URL (`?guid` and `#key`) is stripped from the address bar BEFORE
  the retrieval fetch fires, closing the shoulder-surfing window while the
  request is in flight
- graceful shutdown waits for any in-flight opportunistic purge
  (`server.Wait()`) before closing the store
- sqlite duplicate detection matches the precise extended constraint codes
  (1555 PRIMARYKEY / 2067 UNIQUE) instead of every SQLITE_CONSTRAINT_* class
- the frontend's `Math.random` autofill-name fallback is comment-flagged as
  non-crypto — never to be copied into key/IV generation
- INSTALL.md documents that the opportunistic purge triggers on retrievals
  only (create-only workloads rely on the cron cleanup)
- audit items verified and rejected, so they are not re-raised: the net/http
  server closes request bodies itself; rate-limit rules are a map and cannot
  contain duplicates; `json.Encoder`'s trailing newline, purge-throttle naming,
  and NTP-backwards purge edge are harmless as-is

Wording (2026-07-18), binding: the burn metaphor is removed product-wide —
help-dialog texts in all 25 languages, documentation, backend identifiers, and
the 404 wire string, which is now `secret expired or was already retrieved`
(contract §0.2 amended accordingly). Only the verbatim historical records
(`INITIAL_REQUEST.md`, `GRILLING.md`) keep their original wording.

Keyboard shortcuts (2026-07-18, v1.0.4), binding: the input field responds to
the following keys while focused; existing button actions must remain
available for mouse / touch users, and the shortcuts must never consume a
native browser shortcut the user has muscle memory for (no Ctrl-N / Ctrl-D /
Ctrl-H / Ctrl-L / Ctrl-C overrides).

- `F1` — open the help dialog
- `Cmd/Ctrl-G` — generate a random secret (same body as the Generate button)
- `Cmd/Ctrl-Shift-C` — copy the full input value to the clipboard (same body
  as the in-pill copy button; selection-copy via native Ctrl/Cmd-C is
  preserved)
- `Cmd/Ctrl-Shift-T` — cycle the theme (same body as the theme button;
  overrides the browser reopen-closed-tab shortcut on the same combo)

The modifier key is `metaKey || ctrlKey` so macOS users press `Cmd` and
everyone else presses `Ctrl`. Generate is suppressed on key-repeat (held key
must not overwrite the input). The `isComposing` IME guard is preserved, so
shortcuts never fire mid-IME.
