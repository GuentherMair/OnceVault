# STEP 1 — Scaffold & Config (wave 1)

Prereq: [PLAN_STEP_0.md](PLAN_STEP_0.md) (binding contracts — read first).
Owns: `main.go`, `config/config.go`, `config/config_test.go`. Touch nothing else;
go.mod/go.sum are frozen.

## main.go (v1 — must compile standalone with only the config and store packages)

- Flags: `-config <path>` (default `config.yaml`). Positional subcommand: `serve` (default) | `cleanup`.
- Sets up `slog` (text handler, stderr, Info level).
- Loads + validates config (`config.Load(path)`), exits 1 with a clear error on failure.
- `serve`: for now logs "server wiring pending (step 5)" and exits 1 — step 5/6 replace this.
- `cleanup`: for now logs "cleanup wiring pending (step 6)" and exits 1 — step 6 replaces this.

## config package

`config.Load(path string) (*Config, error)` per contract 0.5:
- Extension dispatch: `.json` → `encoding/json`, `.yaml`/`.yml` → `gopkg.in/yaml.v3`; other → error.
- Struct with both `json:` and `yaml:` tags. Defaults: listen `127.0.0.1:8420`, driver required, max_secret_bytes 16384.
- Validation exactly per 0.5, including: `rate_limit.default == 0` → `slog.Warn("rate_limit.default=0 is not allowed, reverting to 6000/min")` + set 6000.
- Parse `rules` into `[]Rule{Net netip.Prefix, Limit int, Blocked bool, Unlimited bool}` sorted/matchable by longest prefix (store parsed form on the Config so the server package consumes it without re-parsing). Bare IPs normalize to /32 (v4) or /128 (v6). `trusted_proxies` likewise parsed to `[]netip.Prefix`.
- Helper: `(*Config) LimitFor(ip netip.Addr) (limit int, blocked bool)` — longest-prefix match over rules, falling back to default. Unlimited → `limit == 0, blocked == false` convention documented on the method.

## Tests (config/config_test.go)

- YAML and JSON round-trip of the full example config.
- Unknown extension, missing file, bad driver, empty dsn → errors.
- default=0 → becomes 6000 (and warning logged).
- Rule parsing: bare IP → /32; invalid CIDR → error; invalid rule value → error.
- LimitFor: longest-prefix beats shorter; unmatched → default; "-" → blocked; "0" → unlimited.

## Re-verify before finishing
Security invariants in [PLAN.md](PLAN.md); `go build ./config ./... && go vet ./... && go test ./config` green; no new dependencies.
