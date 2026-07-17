# STEP 4 — Frontend `web/index.html` (wave 1)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) sections 0.1/0.2/0.6 (binding).
Owns: `web/index.html` only — ONE fully self-contained file: no external requests of any
kind (no CDN, fonts, images), inline SVG icons, no cookies, minimal code. Load the
frontend-design skill guidance mentally but the visual target is fixed: google.com-like
minimalism.

## Layout (Google-style)

- Centered "OnceVault" wordmark above the input (system font stack, large, like the Google logo position).
- Pill input row, horizontally+vertically centered: `<input type="password">` labeled/placeholder "Secret"; fully rounded (border-radius 9999px); `max-width: 700px`; min. 90px horizontal margin to window borders; it shrinks on narrow screens.
- Inside the pill, right side: eye toggle button (SVG), then a rounded native `<select>` duration dropdown fitted into the pill: options 1h,4h,8h,1d,2d,5d,7d → values 1,4,8,24,48,120,168, default 1d (24). No label.
- Below: button `Encrypt [AES-256-GCM]` styled like the "Google Search" button.
- Output panel below (hidden by default): width matches the input row; `min-height` = 2 text rows; grows vertically with content (pre-wrap, break-all for links); top-right inside: copy icon + close icon.
- Top-right corner of the window: theme cycler icon (system→dark→light) and help icon.
- Help icon opens a native `<dialog>`: product blurb from contract 0.1 + duration options, read-once semantics, color legend, note that HTTPS (secure context) is required for Web Crypto.

## Behavior

- Autofill/store prevention: `autocomplete="off"` on form + `autocomplete="new-password"` on input, randomized `name` attr (set from JS at load), `data-lpignore="true"`, `data-1p-ignore`, `data-bwignore`, `spellcheck="false"`; native form submit prevented.
- Eye toggle: swap input `type` password↔text + swap SVG (eye/eye-off).
- ENTER in input triggers encrypt **only if** `input.value` non-empty; button click likewise no-ops on empty.
- Encrypt flow (contract 0.6): generateKey AES-GCM-256 → 12-byte IV via getRandomValues → subtle.encrypt of `TextEncoder`-encoded value → `POST /api/secrets` `{"secret": b64std(ct), "iv": b64std(iv), "duration": selectedHours}` → 201: show `${location.origin}/?guid=${guid}#${b64url_nopad(rawKey)}` in **yellow** state; non-2xx: show server `error` string in **red** state. Clear the input after successful encryption.
- Decrypt-on-load: if `location.search` has `guid` matching the UUIDv4 regex AND `location.hash` holds a plausible key (43–44 base64/base64url chars): `GET /api/secrets/{guid}` (GUID ONLY — the fragment must never appear in any request URL/body/header), import raw key (decode base64url; fall back to standard base64), subtle.decrypt → **green** state; any failure (HTTP error → its `error` string; decrypt exception → "decryption failed — wrong or corrupted key") → **red** state. Then `history.replaceState` to strip `?guid` and the fragment from the address bar.
- Output states as CSS classes: `.out-encrypt` yellow bg/dark-yellow text, `.out-decrypt` green/dark-green, `.out-error` red/dark-red — each with a dark-theme variant.
- Copy icon: `navigator.clipboard.writeText(outputText)` with brief visual confirmation. Close icon: clear+hide output, clear input, reset URL (replaceState).
- Theme cycler: cycles `system → dark → light`; sets `data-theme` on `<html>`; CSS custom properties with `prefers-color-scheme` as the system default; persisted in `localStorage("oncevault-theme")` — the ONLY storage the page uses.

## Constraints
Vanilla JS (one inline `<script>`), one inline `<style>`; no framework, no build step, no
external URL anywhere in the file. Works standalone when opened via the Go server later
(step 6 embeds it; a static file server suffices for manual testing now).

## Re-verify before finishing
Invariants in [PLAN.md](PLAN.md) — especially: plaintext/key never in any request;
`grep -ci "http" web/index.html` shows no external URLs; file passes a smoke test in a
local browser (encryption path can be tested against a mock or by checking the request
payload shape in devtools).
