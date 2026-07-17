# Installing OnceVault

OnceVault is a single static Go binary plus one config file. This guide covers
building, configuration, database setup for all four backends, running as a systemd
service on Debian/Ubuntu, cron cleanup, and TLS reverse proxying.

## 1. Build

Requires Go >= 1.22. The frontend is embedded into the binary at build time — there is
nothing else to deploy.

```sh
git clone <your-clone-url> oncevault && cd oncevault
go build -o oncevault .
sudo install -m 0755 oncevault /usr/local/bin/oncevault
```

## 2. Configure

OnceVault reads one config file given via `-config <path>`. The format is chosen by
extension: `.json` is parsed as JSON, `.yaml`/`.yml` as YAML. Both carry the same keys;
[config.example.yaml](config.example.yaml) is the annotated reference,
[config.example.json](config.example.json) mirrors its values.

```sh
sudo mkdir -p /etc/oncevault
sudo cp config.example.yaml /etc/oncevault/config.yaml
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
sudo chown oncevault:oncevault /var/lib/oncevault   # user created in section 4
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

Create a dedicated system user and install the unit:

```sh
sudo adduser --system --group --home /var/lib/oncevault --shell /usr/sbin/nologin oncevault
```

`/etc/systemd/system/oncevault.service`:

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

(For redis/mysql/postgres backends the `ReadWritePaths` line can be dropped.)

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
