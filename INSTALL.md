# Installing OnceVault

OnceVault is a single static Go binary plus one config file. This guide covers
building, configuration, database setup for all four backends, running as a systemd
service on Debian/Ubuntu, cron cleanup, and TLS reverse proxying.

## 1. Build

Requires Go >= 1.22 (built and tested with Go 1.26.5). The frontend is embedded into the binary at build time — there is
nothing else to deploy.

The standard command builds **all four storage drivers**; see [§7 Build tags & binary
size](#7-build-tags--binary-size) below for slimmer single-driver builds.

```sh
git clone <your-clone-url> oncevault && cd oncevault
go build -trimpath -ldflags="-s -w" -tags driver_all -o oncevault .
sudo install -m 0755 oncevault /usr/local/bin/oncevault
```

`-trimpath` strips absolute build paths (reproducible builds, slightly smaller), and
`-ldflags="-s -w"` removes the DWARF debug info and symbol table (~35% smaller, no
impact in production). The `-tags driver_all` is required because no drivers are
compiled in by default; see §7.

## 2. Configure

OnceVault reads one config file given via `-config <path>`. The format is chosen by
extension: `.json` is parsed as JSON, `.yaml`/`.yml` as YAML. Both carry the same keys;
[config.example.yaml](config.example.yaml) is the annotated reference,
[config.example.json](config.example.json) mirrors its values.

Create the dedicated system user first (the service in section 4 and the cron job in
section 5 run as it), then install the config with restrictive permissions — the file
may contain a DB password, so nobody besides root and the service user may read it:

```sh
sudo adduser --system --group --home /var/lib/oncevault --shell /usr/sbin/nologin oncevault

sudo mkdir -p /etc/oncevault
sudo cp config.example.yaml /etc/oncevault/config.yaml
sudo chown -R oncevault:oncevault /etc/oncevault
sudo chmod 750 /etc/oncevault
sudo chmod 640 /etc/oncevault/config.yaml
```

Key by key:

| Key | Meaning |
|---|---|
| `listen` | Bind address of the plain-HTTP server, e.g. `127.0.0.1:8420`. Keep it on localhost; TLS terminates at the reverse proxy (section 6). |
| `db.driver` | `sqlite`, `redis`, `mysql`, or `postgres`. |
| `db.dsn` | Driver-specific connection string, see section 3. |
| `max_secret_bytes` | Cap on the decoded ciphertext size. `1024` strict / `16384` balanced default / `65536` permissive — see the pros/cons block in config.example.yaml. |
| `trusted_proxies` | CIDRs (or bare IPs) of reverse proxies whose `X-Forwarded-For` is believed for rate limiting. Must match where your proxy connects from, typically `["127.0.0.1", "::1"]`. |
| `rate_limit.default` | POSTs per minute per client IP when no rule matches. `0` logs a warning and reverts to 6000. |
| `rate_limit.rules` | CIDR-keyed overrides: `"0"` unlimited, `"-"` blocked on all routes, `"n"` POSTs/minute. Longest prefix wins. |

## 3. Databases

sqlite, mysql, and postgres auto-migrate their schema on first start; the SQL scripts
below additionally let you create everything up front with least privilege.

### 3.1 sqlite (default, no server needed)

The DSN is simply a file path. Create a directory the service user can write to (the
directory itself must be writable — sqlite creates WAL/journal side files):

```sh
sudo mkdir -p /var/lib/oncevault
sudo chown oncevault:oncevault /var/lib/oncevault   # user created in section 2
sudo chmod 0750 /var/lib/oncevault
```

```yaml
db:
  driver: "sqlite"
  dsn: "/var/lib/oncevault/oncevault.db"
```

### 3.2 redis

```yaml
db:
  driver: "redis"
  dsn: "redis://:yourpassword@127.0.0.1:6379/0"
```

Minimal `redis.conf` hardening for OnceVault:

```conf
bind 127.0.0.1 -::1
requirepass yourpassword
# Never evict live secrets under memory pressure — fail writes instead:
maxmemory-policy noeviction
```

Notes:
- Expiry is redis-native TTL (`SET ... EX`): expired secrets vanish on their own, and
  both `cleanup` steps (purge and vacuum) are no-ops on this backend.
- Because expired keys are already gone, redis cannot distinguish "expired unread"
  from "already burned": the HTTP API never returns `410 Gone` on redis — recipients
  of an expired link see the generic 404 "expired or was already burned" message.

### 3.3 mysql

```sql
-- as root: mysql -u root -p < setup.sql
CREATE DATABASE oncevault CHARACTER SET utf8mb4;
CREATE USER 'oncevault'@'localhost' IDENTIFIED BY 'change-me';
GRANT SELECT, INSERT, DELETE, CREATE, INDEX ON oncevault.* TO 'oncevault'@'localhost';
-- OPTIMIZE TABLE (the cleanup vacuum step) needs INSERT+SELECT, covered above.

USE oncevault;
CREATE TABLE IF NOT EXISTS secrets (
  guid    CHAR(36) NOT NULL PRIMARY KEY,
  secret  MEDIUMTEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires BIGINT NOT NULL,
  KEY idx_secrets_expires (expires)
) ENGINE=InnoDB;
```

```yaml
db:
  driver: "mysql"
  dsn: "oncevault:change-me@tcp(127.0.0.1:3306)/oncevault"
```

### 3.4 postgres

```sql
-- as the postgres superuser: sudo -u postgres psql -f setup.sql
CREATE ROLE oncevault LOGIN PASSWORD 'change-me';
CREATE DATABASE oncevault OWNER oncevault;

\connect oncevault
SET ROLE oncevault;
CREATE TABLE IF NOT EXISTS secrets (
  guid    CHAR(36) PRIMARY KEY,
  secret  TEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_secrets_expires ON secrets(expires);
```

```yaml
db:
  driver: "postgres"
  dsn: "postgres://oncevault:change-me@127.0.0.1:5432/oncevault"
```

## 4. Run as a service (Debian/Ubuntu, systemd)

The service runs as the `oncevault` system user created in section 2. Install the unit
as `/etc/systemd/system/oncevault.service`:

```ini
[Unit]
Description=OnceVault zero-knowledge secret sharing
After=network.target

[Service]
User=oncevault
Group=oncevault
ExecStart=/usr/local/bin/oncevault -config /etc/oncevault/config.yaml serve
Restart=on-failure
RestartSec=2

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
# sqlite only: the one directory the service may write to
ReadWritePaths=/var/lib/oncevault

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now oncevault
systemctl status oncevault
```

For redis/mysql/postgres backends (no on-disk database), use the same unit but
drop the `ReadWritePaths` line — the service has nothing to write:

```ini
[Unit]
Description=OnceVault zero-knowledge secret sharing
After=network.target

[Service]
User=oncevault
Group=oncevault
ExecStart=/usr/local/bin/oncevault -config /etc/oncevault/config.yaml serve
Restart=on-failure
RestartSec=2

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

## 5. Cron cleanup

Expired secrets are already purged opportunistically while the server handles traffic;
a periodic `cleanup` run additionally vacuums and covers idle periods.
`/etc/cron.d/oncevault`:

```cron
*/15 * * * * oncevault /usr/local/bin/oncevault -config /etc/oncevault/config.yaml cleanup
```

On redis this exits successfully without doing anything (native TTL).

## 6. Reverse proxy (TLS)

The Web Crypto API only exists in a **secure context**: OnceVault must be reached via
HTTPS (or `localhost` while testing), so put it behind a TLS-terminating proxy. The
proxy must send `X-Forwarded-For`, and the address it connects from must be listed in
`trusted_proxies` — with the examples below:

```yaml
trusted_proxies: ["127.0.0.1", "::1"]
```

### nginx

```nginx
server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name vault.example.com;

    ssl_certificate     /etc/letsencrypt/live/vault.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vault.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8420;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
    }
}

server {
    listen 80;
    listen [::]:80;
    server_name vault.example.com;
    return 301 https://$host$request_uri;
}
```

### apache

```sh
sudo a2enmod ssl proxy proxy_http remoteip
```

```apache
<VirtualHost *:443>
    ServerName vault.example.com

    SSLEngine on
    SSLCertificateFile    /etc/letsencrypt/live/vault.example.com/fullchain.pem
    SSLCertificateKeyFile /etc/letsencrypt/live/vault.example.com/privkey.pem

    ProxyPreserveHost On
    ProxyPass        / http://127.0.0.1:8420/
    ProxyPassReverse / http://127.0.0.1:8420/
    # mod_proxy appends the client address as X-Forwarded-For automatically.
    # RemoteIPHeader is only needed if apache itself sits behind ANOTHER proxy
    # and should resolve the real client from that proxy's header first:
    #   RemoteIPHeader X-Forwarded-For
    #   RemoteIPTrustedProxy 203.0.113.10
</VirtualHost>

<VirtualHost *:80>
    ServerName vault.example.com
    Redirect permanent / https://vault.example.com/
</VirtualHost>
```

## 7. Build tags & binary size

Each storage backend lives in its own file with a build tag, so you can ship a binary
that contains only the drivers you actually use. Tag one or more of:

| Tag | What it includes |
|---|---|
| `driver_sqlite` | CGO-free SQLite via `modernc.org/sqlite` |
| `driver_redis` | Redis via `go-redis` |
| `driver_mysql` | MySQL/MariaDB via `go-sql-driver/mysql` |
| `driver_postgres` | PostgreSQL via `jackc/pgx/v5/stdlib` |
| `driver_all` | All four (equivalent to passing all four tags) |

If you build **without any tag** the binary contains no drivers; at startup you get a
clear error pointing at the missing tag:

```
error="unknown db driver \"sqlite\" (built: <none — rebuild with -tags driver_all or -tags driver_<name>>)"
```

To build only the backend you plan to deploy, e.g. sqlite:

```sh
go build -trimpath -ldflags="-s -w" -tags driver_sqlite -o oncevault .
```

Combine tags for multi-backend hosts: `-tags driver_sqlite,driver_redis`.

Measured sizes on this machine (Go 1.26.5, darwin/arm64, `-trimpath -ldflags="-s -w"`,
last regenerated 2026-07-18):

| `-tags …` | Binary size |
|---|---|
| _no tags_ | 6.0 MB |
| `driver_mysql` | 6.6 MB |
| `driver_redis` | 7.2 MB |
| `driver_sqlite` | 9.9 MB |
| `driver_postgres` | 10.0 MB |
| `driver_sqlite,driver_mysql` | 10.2 MB |
| `driver_mysql,driver_postgres` | 10.4 MB |
| `driver_sqlite,driver_redis` | 11.0 MB |
| `driver_sqlite,driver_postgres` | 13.6 MB |
| `driver_sqlite,driver_mysql,driver_postgres` | 14.0 MB |
| `driver_all` (default) | 15.1 MB |
| _none of the above, no `-s -w`_ | 23.3 MB |

`sqlite` is the largest single driver because `modernc.org/sqlite` is the full SQLite
engine translated to Go. `postgres` is large for a single driver because pgx's stdlib
mode pulls in the protocol stack. `mysql` is the lightest after redis.

If you need to go even smaller, the remaining ~6 MB is the Go runtime + the FIPS
140-3 crypto module that Go 1.24+ always links in. UPX will compress the resulting
binary by another ~50% at the cost of a slightly slower startup and a small RSS
overhead, but a few anti-malware tools flag UPX-packed binaries, so use it only where
you control the deployment host.

Tests under `store/` and `server/` exercise the sqlite backend and are gated by the
same tag, so to run the full suite:

```sh
go test -tags driver_all ./...
```
