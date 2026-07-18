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
- a fitting favicon is generated and shipped
- apply a left-to-right opacity gradient to the "Once" part of the "OnceVault" title
