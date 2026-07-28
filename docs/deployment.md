# Deployment guide

Two containers, one SQLite volume, one reverse proxy. That is the whole
production topology.

## 1. Prepare the host

Docker 24+ with the Compose plugin, and a DNS record pointing at the host.

```bash
git clone https://github.com/sangamdrive/sangamdrive.git
cd sangamdrive
cp .env.example .env
```

## 2. Generate secrets

```bash
openssl rand -base64 32   # -> ENCRYPTION_KEY
openssl rand -base64 32   # -> SESSION_SECRET
```

> **Back up `ENCRYPTION_KEY` before you go live.** Every stored refresh token is
> sealed with it. Lose it and every user must reconnect every Google account.
> Rotating it has the same effect.

## 3. Configure

Same-origin is the recommended setup — the web app on `/`, the API on `/api`.
It avoids cross-site cookie problems entirely.

```bash
SANGAM_ENV=production
API_BASE_URL=https://drive.example.com
APP_BASE_URL=https://drive.example.com
CORS_ORIGINS=https://drive.example.com
NEXT_PUBLIC_API_BASE_URL=https://drive.example.com
COOKIE_SECURE=true
TRUST_PROXY=true

GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
ENCRYPTION_KEY=...
SESSION_SECRET=...
```

`NEXT_PUBLIC_API_BASE_URL` is baked into the client bundle at build time. Changing
it requires rebuilding the web image, not just restarting it.

The Google Console redirect URI must be exactly
`https://drive.example.com/api/v1/auth/google/callback`. See
[oauth-setup.md](oauth-setup.md).

## 4. Start

```bash
docker compose -f docker/docker-compose.yml up -d --build
docker compose -f docker/docker-compose.yml ps
curl -fsS http://127.0.0.1:8080/healthz
```

Both services bind to `127.0.0.1` only. The reverse proxy is the sole ingress.

## 5. Reverse proxy

### Nginx

Copy `docker/nginx.conf.example` to `/etc/nginx/sites-available/sangamdrive`,
adjust the hostname and certificate paths, then:

```bash
ln -s /etc/nginx/sites-available/sangamdrive /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

### Traefik

```bash
export SANGAM_HOST=drive.example.com
docker compose \
  -f docker/docker-compose.yml \
  -f docker/docker-compose.traefik.yml \
  up -d
```

Assumes an existing Traefik on an external `proxy` network with a `letsencrypt`
certificate resolver.

### Proxy requirements

Whatever proxy you use, it **must**:

| Requirement                          | Why                                                    |
| ------------------------------------ | ------------------------------------------------------ |
| Request buffering **off**            | Uploads stream through; buffering breaks large files    |
| No request body size limit           | Same                                                    |
| Read/send timeouts ≥ 1 hour          | A multi-gigabyte transfer is a single long request      |
| Forward `X-Forwarded-For` and `-Proto` | Correct client IPs for rate limiting, correct scheme  |

Both example configs already do this.

## Backups

Everything that matters lives in one SQLite file.

```bash
docker compose -f docker/docker-compose.yml exec api \
  sh -c 'wget -qO- http://127.0.0.1:8080/healthz' >/dev/null

docker run --rm \
  -v sangamdrive_api-data:/data:ro \
  -v "$PWD:/backup" \
  alpine tar czf /backup/sangamdrive-$(date +%F).tar.gz -C /data .
```

Because WAL mode is on, copy the `-wal` and `-shm` files alongside the `.db`, or
stop the API first for a guaranteed-consistent snapshot.

**Also back up `.env`.** The database is worthless without `ENCRYPTION_KEY`.

### Restore

```bash
docker compose -f docker/docker-compose.yml down
docker run --rm -v sangamdrive_api-data:/data -v "$PWD:/backup" \
  alpine sh -c 'rm -rf /data/* && tar xzf /backup/sangamdrive-2026-01-01.tar.gz -C /data'
docker compose -f docker/docker-compose.yml up -d
```

## Upgrading

```bash
git pull
docker compose -f docker/docker-compose.yml up -d --build
```

Migrations run automatically at boot and are idempotent. Take a backup first
anyway.

## Operations

```bash
# logs (structured JSON in production)
docker compose -f docker/docker-compose.yml logs -f api

# trace one request end to end
docker compose -f docker/docker-compose.yml logs api | grep '<request-id>'
```

| Probe                    | Meaning                                     |
| ------------------------ | -------------------------------------------- |
| `GET /healthz`           | Process is alive. Never touches the database. |
| `GET /readyz`            | Database reachable; safe to send traffic.     |

Point orchestrator liveness at `/healthz` and readiness at `/readyz`. Pointing
liveness at `/readyz` turns a transient database blip into a restart loop.

## Hardening checklist

- [ ] `SANGAM_ENV=production` and `COOKIE_SECURE=true`
- [ ] HTTPS enforced; HSTS enabled at the proxy
- [ ] `CORS_ORIGINS` lists only your own origin
- [ ] `ENCRYPTION_KEY` and `SESSION_SECRET` are unique to this deployment
- [ ] `.env` is `chmod 600` and excluded from backups that leave the host
- [ ] Container ports bound to `127.0.0.1`
- [ ] Automated, tested, off-host backups
- [ ] `RATE_LIMIT_MAX` tuned to your user count

## Troubleshooting

**Every account shows "Reconnect required" after 7 days** — the OAuth consent
screen is still in Testing mode. See [oauth-setup.md](oauth-setup.md).

**Large uploads fail partway** — the proxy is buffering or has a body size limit.

**Login redirects in a loop** — `COOKIE_SECURE=true` without HTTPS, or
`APP_BASE_URL` not matching the browser's origin.

**`403 csrf_invalid` on every write** — the web and API are on different origins
and the browser is dropping the CSRF cookie. Use the same-origin proxy layout.
