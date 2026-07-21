# OnceVault — the message will self-destruct once read.

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
distinguish "expired unread" (410 on the SQL backends) from "already retrieved" — both
simply report the secret as gone (404).

## Interested?

Single Go binary, one self-contained HTML page, your choice of sqlite, redis, MySQL,
or PostgreSQL. See [INSTALL.md](INSTALL.md) for build, configuration, database setup,
systemd, cron cleanup, and reverse-proxy examples.

Build the binary with at least one driver tag, e.g. `-tags driver_sqlite` or
`-tags driver_all`; see [INSTALL.md §7](INSTALL.md#7-build-tags--binary-size)
for the full tag set and binary sizes.

## Backend structure

```text
OnceVault/
|-- main.go                    Entry point for `serve` and `cleanup`. Loads
|                              configuration, opens the selected store, embeds web
|                              assets, starts HTTP, and handles graceful shutdown.
|-- go.mod                     Declares the module, Go version, database drivers,
|                              and YAML dependency.
|-- go.sum                     Stores checksums for direct and transitive Go
|                              dependencies.
|
|-- config.example.yaml        Documented YAML example for HTTP, database, secret
|                              size, proxy, and rate-limit settings.
|-- config.example.json        JSON equivalent of the runtime configuration example.
|
|-- config/
|   |-- config.go              Defines and loads the JSON or YAML configuration.
|   |                          Applies defaults, validates values, parses CIDRs, and
|   |                          resolves per-IP rate-limit rules.
|   \-- config_test.go         Tests loading, defaults, validation, CIDR parsing,
|                              warnings, and longest-prefix rate-limit matching.
|
|-- server/
|   |-- server.go              Implements strict routing, middleware, API handlers,
|   |                          error mapping, GUID generation, and expired-secret
|   |                          cleanup.
|   |-- ratelimit.go           Implements fixed one-minute limits keyed by IPv4
|   |                          address or IPv6 /64 prefix, with stale-entry pruning.
|   |-- server_test.go         Tests routes, validation, create/take behavior,
|   |                          middleware, errors, recovery, and cleanup throttling.
|   \-- ratelimit_test.go      Tests windows, IPv6 bucketing, CIDR overrides,
|                              blocked clients, trusted proxies, and shared budgets.
|
|-- store/
|   |-- store.go               Defines the Store interface, sentinel errors, driver
|   |                          registry, and build-tag-dependent driver selection.
|   |-- sqlite.go              Implements SQLite storage with atomic delete-and-return
|   |                          retrieval, database-side expiry, purge, and vacuum.
|   |-- sqlite_test.go         Tests SQLite round trips, read-once atomicity, expiry,
|   |                          duplicate detection, purging, and vacuuming.
|   |-- mysql.go               Implements MySQL storage with InnoDB transactions and
|   |                          SELECT FOR UPDATE followed by deletion.
|   |-- postgres.go            Implements PostgreSQL storage with DELETE RETURNING for
|   |                          atomic retrieval and deletion.
|   \-- redis.go               Implements Redis storage with native TTLs, SETNX, and
|                              atomic GETDEL; requires Redis 6.2 or later.
|
|-- tools/
|   |-- gen_favicon/
|   |   \-- main.go            Deterministically generates the multi-resolution
|   |                          favicon using only the Go standard library.
|   \-- gen_maskfont/
|       \-- main.go            Deterministically generates the minimal TrueType mask
|                              font used by the frontend.
|
\-- web/
    |-- index.html             Embedded frontend asset; contents omitted here.
    |-- favicon.ico            Generated icon embedded in the binary and served at
    |                          /favicon.ico.
    \-- maskfont.ttf           Generated masking font incorporated into the frontend.
```

Storage implementations are selected at compile time with the `driver_sqlite`,
`driver_mysql`, `driver_postgres`, `driver_redis`, or `driver_all` build tags.

## Provenance

OnceVault was built entirely with LLM tooling (Anthropic Claude and MiniMax models) —
from a human-written specification, through a staged multi-agent build plan, to review
and iteration. The full original request and step plans are preserved in
[docs/](docs/INITIAL_REQUEST.md) and then refined through [GRILLING](docs/GRILLING.md)
and [FINETUNING](docs/FINETUNING.md).

All non-English UI translations are machine-generated and have not been reviewed by
native speakers — corrections are very welcome.

## License

[MIT](LICENSE) — Copyright (c) 2026 Günther Mair.
