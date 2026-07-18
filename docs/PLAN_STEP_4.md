# STEP 4 — Frontend `web/index.html` (wave 1)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) sections 0.1/0.2/0.6 (binding).
Owns: `web/index.html` only — ONE fully self-contained file: no external requests of any
kind (no CDN, fonts, images), inline SVG icons, no cookies, minimal code. Load the
frontend-design skill guidance mentally but the visual target is fixed: google.com-like
minimalism.

## Layout (Google-style)

- Centered "OnceVault" wordmark above the input (system font stack, large, like the Google logo position).
- Pill input row, horizontally+vertically centered: a `<textarea rows="1">` with placeholder "Secret", one line tall by default and multi-line capable (see Behavior); rounded with a **fixed** border-radius of 24px = half the single-line pill height (9999px would balloon as the field grows and curl the border into the text); pill uses `min-height: 48px` so it can grow; `max-width: 700px`; min. 90px horizontal margin to window borders; it shrinks on narrow screens.
- Inside the pill, right side: eye toggle button (SVG), then a rounded native `<select>` duration dropdown fitted into the pill: options 1h,4h,8h,1d,2d,5d,7d → values 1,4,8,24,48,120,168, default 1d (24). No label.
- Below: button `Encrypt [AES-256-GCM]` styled like the "Google Search" button.
- Output panel below (hidden by default): width matches the input row; `min-height` = 2 text rows; grows vertically with content (pre-wrap, break-all for links); top-right inside: copy icon + close icon.
- Top-right corner of the window, in this order: language picker (world-grid/globe icon — an invisible native `<select>` stretched over the icon so one tap opens the browser's own option list, endonym labels), theme cycler icon (system→dark→light), and help icon.
- Help icon opens a native `<dialog>`: product blurb from contract 0.1 + duration options, read-once semantics, color legend, note that HTTPS (secure context) is required for Web Crypto.

## Behavior

- Autofill/store prevention: `autocomplete="off"` on the form, `autocomplete="one-time-code"` on the input (the standard hint for a single-use, do-not-save value — explicitly NOT `new-password`/`current-password` which would make Safari offer to save the secret as a password, and more reliable than `off` on `type="password"` since WebKit historically ignores `off` on password fields). Also: `autocapitalize="off"` + `autocorrect="off"` (iOS keyboard autofill/correction), randomized `name` attr (set from JS at load so it can't match a saved-password entry by name pattern), `data-lpignore="true"`, `data-1p-ignore`, `data-bwignore`, `spellcheck="false"`; native form submit prevented. The intent is read-once: the secret MUST NEVER be stored by any browser/extension password manager.
- Masking ("mask mirror" — a textarea has no `type="password"`, and `-webkit-text-security` is unusable because it masks the newline characters too, collapsing multi-line content onto a single line of dots): the textarea always holds the real value but renders it invisible while masked (`color:transparent`, caret kept via `caret-color`); an absolutely positioned `<pre id="mask">` overlay (identical font/line-height/padding, `pointer-events:none`, clipped `overflow:hidden`) mirrors the value with every character except `\n` replaced by `•`, so the dots show the real line structure. While masked BOTH the textarea and the mask use a monospace stack (`ui-monospace,SFMono-Regular,Menlo,Consolas,monospace`) so caret, selection, and wrap points align with the dots column-exact; `.revealed` restores `font-family:inherit` (the only state in which text is actually read) and empties the mask. The placeholder is pinned to the page font via `::placeholder`. Re-render the mask on every input event, on ALT+ENTER, on eye toggle, and on form reset; mirror `scrollTop` textarea→mask on scroll. Amendment 2026-07-18: the formerly hypothetical "disc font" upgrade path is implemented — `tools/gen_maskfont` generates the embedded data-URI font `'OnceVault Disc'` (cmap format 13 → every codepoint one centered disc at a fixed 600/1000em advance), the FIRST font of both the masked textarea and `#mask`. Once JS confirms the font loaded (`document.fonts`), it sets `.discfont` on `<html>`: the masked textarea then shows its OWN fg-colored glyphs — every character is a disc, and text controls paint even the space glyph — so caret/selection/soft-wrap agree with the dots BY IDENTITY, including CJK/emoji, and the mirror is hidden. (Browser verification showed no dot-mirror can fully imitate real text: spaces receive special line-breaking treatment — hanging, wrap opportunities — in every `white-space` mode, so imitation was abandoned for identity.) Without `.discfont` (font blocked or failed) the transparent-text + `#mask` mirror stays active on the shared monospace fallback stack, guarded by the load-time `MASK_CHAR` probe ('•' only when it provably shares the resolved mono advance, else '*') — a missing font can never reveal plaintext. CSP allows `font-src data:` for exactly this one asset.
- Eye toggle: toggles `.revealed` on the textarea and the button (slash icon + title swap), re-renders the mask, and re-runs autosize (mono and page font wrap long lines at different points).
- ENTER (handled on keydown — a textarea never submits natively) triggers encrypt **only if** the value is non-empty; ALT+ENTER or SHIFT+ENTER (amendment 2026-07-18) inserts a newline at the caret via `setRangeText` (fires no input event, so call autosize + mask re-render manually); ignore Enter while `e.isComposing`. Button click likewise no-ops on empty.
- Amendments 2026-07-18 (loose-end review, details in FINETUNING.md): error panels keep the typed secret (only success clears the input); a valid `?guid=` with a missing/invalid fragment shows the localized `errKeyMissing` error without fetching (a keyless visit never consumes the secret); closing the output panel re-masks the input; a tiny inline `<head>` script stamps the stored theme before first paint.
- Auto-grow: on every input event (covers paste) set the textarea height from `scrollHeight`, capped at 15 lines (15 × 24px line-height + 24px vertical padding = 384px); past the cap switch `overflow-y` to `auto`. Reset to the one-line default whenever the output panel replaces the form.
- Encrypt flow (contract 0.6): generateKey AES-GCM-256 → 12-byte IV via getRandomValues → subtle.encrypt of `TextEncoder`-encoded value → `POST /api/secrets` `{"secret": b64std(ct), "iv": b64std(iv), "duration": selectedHours}` → 201: show `${location.origin}/?guid=${guid}#${b64url_nopad(rawKey)}` in **yellow** state; non-2xx: show server `error` string in **red** state.
- Form ↔ output swap: whenever the output panel is shown (encrypt success, decrypt, any error), the form (pill + encrypt button) is FIRST reset (input value cleared) and THEN hidden, with the output panel taking its place. On close, the output's content is cleared, the output hidden, and the form shown again (empty). This keeps plaintext lifetimes minimal — the input never stays on screen alongside the output.
- Decrypt-on-load: if `location.search` has `guid` matching the UUIDv4 regex AND `location.hash` holds a plausible key (43–44 base64/base64url chars): `GET /api/secrets/{guid}` (GUID ONLY — the fragment must never appear in any request URL/body/header), import raw key (decode base64url; fall back to standard base64), subtle.decrypt → **green** state; any failure (HTTP error → its `error` string; decrypt exception → "decryption failed — wrong or corrupted key") → **red** state. Then `history.replaceState` to strip `?guid` and the fragment from the address bar.
- Output states as CSS classes: `.out-encrypt` yellow bg/dark-yellow text, `.out-decrypt` green/dark-green, `.out-error` red/dark-red — each with a dark-theme variant.
- Copy icon: `navigator.clipboard.writeText(outputText)` with brief visual confirmation. Close icon: clear+hide output, clear input, reset URL (replaceState).
- Theme cycler: cycles `system → dark → light`; sets `data-theme` on `<html>`; CSS custom properties with `prefers-color-scheme` as the system default; persisted in `localStorage("oncevault-theme")`.
- i18n (amendment 2026-07-18): 25 languages — English (default AND fallback for any missing key), the 23 other official EU languages (bg cs da de el es et fi fr ga hr hu it lt lv mt nl pl pt ro sk sl sv), and Mandarin (zh). One plain per-language string object in `I18N` (UTF-8); startup language = persisted `localStorage("oncevault-lang")` if valid, else the first `navigator.languages` primary subtag with a table, else `en`. All UI strings (placeholder, buttons, titles/aria-labels, duration labels, client error messages, the entire help dialog) come from the table; static dialog copy is tagged `data-i18n`/`data-i18n-html` (the latter for own static strings with `<strong>`/`<code>` markup only). The exact contract §0.2 server error strings are mapped client-side to localized equivalents (`SRV_KEYS`); unknown server strings display verbatim. Selecting a language persists it, re-applies everything, sets `<html lang>`, and returns focus to the input. localStorage keys `oncevault-theme` + `oncevault-lang` are the ONLY storage the page uses.
- Focus management: after completing any action (eye toggle, duration dropdown change, theme cycle, close icon, help dialog close), focus MUST return to the secret input field so the user can keep typing without re-clicking.

## Constraints
Vanilla JS (one inline `<script>`), one inline `<style>`; no framework, no build step, no
external URL that the page LOADS anything from (no CDN/fonts/images/fetch targets).
Allowed exceptions:
- `<link rel="icon" href="/favicon.ico">` (line 9 of `index.html`) — same-origin, served by
  `GET /favicon.ico` added in the 2026-07-17 amendment to `PLAN_STEP_0.md §0.2`.
- The help-dialog license line links `https://github.com/GuentherMair/OnceVault`
  (`target="_blank" rel="noopener noreferrer"`) — a user-click navigation, never
  fetched by the page itself.
Works standalone when
opened via the Go server later (step 6 embeds it; a static file server suffices for
manual testing now).

## Re-verify before finishing
Invariants in [PLAN.md](PLAN.md) — especially: plaintext/key never in any request;
`grep -ci "http" web/index.html` finds no external URLs besides the GitHub help-dialog
link above; file passes a smoke test in a local browser (encryption path can be tested
against a mock or by checking the request payload shape in devtools).
