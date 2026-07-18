# OnceVault — share a secret once, then discard it.

OnceVault lets you safely share a secret with exactly one recipient — it's encrypted
in your browser, only one recipient can open it, and the link self-destructs the
moment they read it.

> And if someone else got the link you will know: the message will already be gone!

## Under the hood

Your secret is encrypted **in your browser** with AES-256-GCM before anything leaves
your machine. The server only ever stores the ciphertext under a random ID with an
expiry you pick (1 hour to 7 days). The decryption key travels **only** in the
`#fragment` of the share link, which browsers never send to any server — OnceVault
couldn't read your secret even if it wanted to. The first recipient to open the link
gets the secret; the ciphertext is deleted in the same moment. Expired secrets are
deleted unread.

## How it works

- **Browser-side crypto** — the page generates a random 256-bit AES-GCM key and 12-byte
  IV with the Web Crypto API and encrypts locally; only ciphertext, IV, and duration
  are POSTed to the server.
- **Key in the fragment** — the share link is `https://HOST/?guid={UUID}#{key}`; the
  fragment never leaves the browser, and retrieval uses the GUID only.
- **Read-once** — the first GET atomically fetches *and* deletes the ciphertext; every
  later attempt gets an error, never the secret.
- **Expiry** — pick 1 hour to 7 days; whatever is not retrieved in time is purged
  unread (opportunistically while serving, and via a cron-friendly `cleanup` command).

## Zero-knowledge guarantee

The server stores ciphertext it has no key for, and it never receives one: plaintext
and key exist only in the sender's and recipient's browsers. A full database dump plus
complete traffic logs would still not decrypt a single secret.

One backend nuance: the **redis** backend uses native key TTL for expiry, so it cannot
distinguish "expired unread" (410 on the SQL backends) from "already burned" — both
simply report the secret as gone (404).

## Interested?

Single Go binary, one self-contained HTML page, your choice of sqlite, redis, MySQL,
or PostgreSQL. See [INSTALL.md](INSTALL.md) for build, configuration, database setup,
systemd, cron cleanup, and reverse-proxy examples.

Build the binary with at least one driver tag, e.g. `-tags driver_sqlite` or
`-tags driver_all`; see [INSTALL.md §7](INSTALL.md#7-build-tags--binary-size)
for the full tag set and binary sizes.

## Provenance

OnceVault was built entirely with LLM tooling (Anthropic Claude and MiniMax models) —
from a human-written specification, through a staged multi-agent build plan, to review
and iteration. The full original request and step plans are preserved in
[docs/](docs/INITIAL_REQUEST.md) and then refined through [GRILLING](docs/GRILLING.md)
and [FINETUNING](docs/FINETUNING.md).

## License

[MIT](LICENSE) — Copyright (c) 2026 Günther Mair.
