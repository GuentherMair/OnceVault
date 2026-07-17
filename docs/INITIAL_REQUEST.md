# Initial Request (verbatim, 2026-07-17)

> Stored 1:1 as received; only this header was added.

I want to create a safe and minimalistic password encryption frontend web service, backed by a single-use central burn-server.
The apps name is "OnceVault".

The frontends specs:

- the app name, centered like the "Google" title on google.com
- very plain, no piece of HTML/JavaScript that is not absolutely necessary
- a horizontaly and vertically centered password input field labeled "Secret" with fully rounded edges (like the search input field on google.com), max. 700px wide with 90px margin from the left/right window border (the input field should shrink on narrower screens)
- an dropdown button on the right, inside the input field (also rounded to fit the input field) allowing to select from [1h, 4h, 8h, 1d, 2d, 5d, 7d] and defaulting to 1d (this is the duration, no label needed)
- an "Encrypt [AES-256-GCM]" button below the input field (similar to the "Google search" button on google.com)
- an output field below (hidden by default) should be as wide as the input field + button and with minimal height for 2 text output rows (automatically making space by extending veritically on longer output)
- a "color"-icon in the top right corner of the window allowing to iterate between "system default" (default), "dark mode" and "light mode"
- a "help"-icon allowing to display a dialog expaining all options and behaviour

The expected frontend behaviour:

- the input field should prevent autofill and storing of the secret (all major webbrowsers)
- the input field must allow toggling visualization of the inserted secret through "eye"-icon
- the input field must react to ENTER pressed, but only if there is any content to be encrypted
- the output field is only to be shown after encryption and provides a "copy"-icon and a "close"-icon (reset both, the input and the output field)
- the output field also shows the decrypted secret, if a key was passed in through the URL + the encrypted secret could be retrieved from the backend
- the output field must use output colors matching the current use (encryption: yellow, decryption: green, error: red), with a fitting (darker) color-tone for the font
- hitting the button (or hitting ENTER inside the input field):
  1. generates a random key using window.crypto.subtle.generateKey(...) using "AES-GCM" with length 256
  2. encrypts the encoded secret test using window.crypto.subtle.encrypt(...) together with the AES-256-GCM key and a random initialization vector
  3. contacts the backend API for storage of "secret" (base64-encoded), "iv" (base64-encoded) and "duration" (int in hours)
  4. the output field must show "http(s)://CURRENT_APP_URL/guid={GUID}#${BASE64_ENCODED_KEY}" on success (yellow) and the server-provided error on failure (red)
- on page initialization, the URL is to be verified against presence of GUID and a base64-encoded AES-256-GCM key; if both are present:
  1. the backend API is to be called using ONLY the GUID (this is CRITICAL!)
  2. on success, the secret needs to be decrypted and displayed (green); on failure the error needs to be displayed (red)

Backend specs:

- must serve the frontend on "/"
- must provide a POST "/api/secrets" API where:
  1. input fields must pass sanity checks ("secret" and "iv" must be base64-decodable + contain non-Null/empty values; "duration" must be one of 1, 4, 8, 24, 48, 120 or 168)
  2. a random GUID must be generated for storage
  3. four fields need to be stored: "guid" (unique DB field!), "secret", "iv" and "expires" (expiration timestamp built from current timestamp + duration in hours)
  4. storing must be transactional and the response to the caller must contain the "guid" + DSN 2xx on success or a precise "error" message + DNS 4xx on failure (no GUID)
- must provide a GET "/api/secrets/:guid" API (read-once and delete immediately) where:
  1. fetch and delete by GUID must occur as simultaneously as possible
  2. the fetch must derive a boolean "isexpired" by a DB-internal comparison
  3. the response to the caller must contain:
     3.a) the "secret" and "iv" + 2xx DSN if a non-expired secret was found
     3.b) a "error" about expired, but as of yet unretrieved secret + 4xx DSN if a expired secret was found
     3.c) a "error" about expired or burned secret + 4xx DSN if no secret was found
     3.d) a "error" about unexpected backend failure in any other case (the API must be fully protected from any DB/other backend exceptions)
  4. the server must execute a "delete" operation on all expired entries (expires <= current timestamp); note: this must be protected from any DB/other backend expceptions and may never surface as an error through the API/enduser side (fail silently, log warning/error on backend side only)
- the backend should be configurable (json or yaml) as follows:
  - backend DB choice and configuration for between redis, sqlite, mysql, postgres
  - rate-limitable by array in CIDR notation ("0": unlimited; "-": blocked; "1-n": allowed POST requests per minute)
  - "default" as rate-limit for all unspecified addresses (note: this setting cannot be "0", if "0" is set, then the backend should log a warning on startup and revert to max. 6000 posts per minute)
- the backend should provide a cronjob-callable "cleanup" procedure, which executes a "delete" operation for all "expires <= current timestamp" entries and executes a vacuum (or similar) where appropriate, depending on the choosen DB backend
- only the defined routes may be delivered; all other content must not be exposed

The most important notes and caveats (to be also re-verified after each implementation step):

- all encryption AND decryption MUST happen inside the users browser using its built-in "Web Crypto API" and the AES-256-GCM mechanism
- the plaintext secret MUST NEVER be sent to the backend/server
- where possible avoid pulling in external libraries/dependencies (reducing the supply chain attack vector) and rely on minimal local code as much as possible

Divide the build into steps delegatable to sub-agents. Store each step in a distinct memory file and create a central memory file for firing up parallel implementation.
Stop before doing so, as I want to run the plan through a grilling Q&A session first.

---

## Additions (post-initial-request)

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
