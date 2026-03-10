# Deploy Instructions

This repository includes a single-host production deployment using Docker
Compose, local Postgres, local Redis, machine-local profile photo storage, and
Nginx on the same backend. HTTPS is enforced with Let's Encrypt certificates
through a Compose-managed Certbot flow that bootstraps the first certificate and
renews it automatically.

## Local Development

The local Docker Compose setup uses these checked-in files plus a copied
runtime env file:

- [docker-compose.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.yml)
- [docker-compose.dev.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.dev.yml)
- [.env.dev.example](/Users/alisher/University/bisp/pauza-server/.env.dev.example)

Create the ignored runtime env file once:

```bash
cp .env.dev.example .env.dev
```

It starts the API, Nginx, Postgres, Redis, and a Mailpit SMTP sink for OTP
emails. After the stack is up, run migrations once for a fresh database:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build -d
docker compose -f docker-compose.yml -f docker-compose.dev.yml run --rm api ./pauza-migrate
```

Mailpit exposes a local inbox UI at `http://localhost:8025`.

The shared Compose base pins the internal Docker network to
`172.30.0.0/24`, and both env templates set `TRUSTED_PROXIES` to that same
CIDR so forwarded client IPs are trusted without opening that trust boundary to
all private address space. If that subnet collides on a specific machine,
change the subnet in [docker-compose.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.yml)
and update `TRUSTED_PROXIES` in the matching env file to the same value.

## Deployment Files

- [docker-compose.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.yml): shared base services
- [docker-compose.prod.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.prod.yml): production overlay
- [.env.prod.example](/Users/alisher/University/bisp/pauza-server/.env.prod.example): production environment template copied to `.env.prod`
- [deploy/nginx/production-bootstrap.conf.template](/Users/alisher/University/bisp/pauza-server/deploy/nginx/production-bootstrap.conf.template): initial HTTP bootstrap config used before certificates exist
- [deploy/nginx/production-tls.conf.template](/Users/alisher/University/bisp/pauza-server/deploy/nginx/production-tls.conf.template): HTTPS config used after certificates exist
- [deploy/nginx/start-prod-nginx.sh](/Users/alisher/University/bisp/pauza-server/deploy/nginx/start-prod-nginx.sh): Nginx startup script that switches between bootstrap and TLS configs automatically
- [deploy/certbot/run-certbot.sh](/Users/alisher/University/bisp/pauza-server/deploy/certbot/run-certbot.sh): Certbot loop that obtains and renews certificates
- [deploy/nginx/pauza.conf](/Users/alisher/University/bisp/pauza-server/deploy/nginx/pauza.conf): host-managed Nginx config if you choose not to run Nginx in Compose

## Placeholders To Replace

In [docker-compose.prod.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.prod.yml):

- `docker.io/your-dockerhub-user/pauza-server:latest`

In [.env.prod.example](/Users/alisher/University/bisp/pauza-server/.env.prod.example):

- `api.example.com`
- `admin@example.com`
- `replace-with-a-32-byte-minimum-production-secret`
- `smtp.example.com`
- `replace_me`
- `noreply@example.com`
- `https://api.example.com/photos`
- any other dummy or example value

The database password now lives only in `.env.prod`, where it must be updated in both `POSTGRES_PASSWORD` and `DATABASE_URL`.

In [deploy/nginx/pauza.conf](/Users/alisher/University/bisp/pauza-server/deploy/nginx/pauza.conf) if you use host-managed Nginx:

- `api.example.com`
- `/srv/pauza/photos`
- `127.0.0.1:8080`
- `/var/log/nginx/pauza.access.log`
- `/var/log/nginx/pauza.error.log`
- `/etc/letsencrypt/live/api.example.com/fullchain.pem`
- `/etc/letsencrypt/live/api.example.com/privkey.pem`

## Assumptions

- Docker and Docker Compose are installed on the backend machine.
- The API image is already published on Docker Hub.
- The API image contains both `./pauza-server` and `./pauza-migrate`.
- The production deployment runs on one machine.
- The domain already points to the backend machine before first startup so the ACME HTTP challenge can succeed.

## Recommended Deploy Steps

1. Copy the project to the backend machine.
2. Edit [docker-compose.prod.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.prod.yml) and replace the placeholder API image with the real Docker Hub image tag you want to deploy.
3. Copy [.env.prod.example](/Users/alisher/University/bisp/pauza-server/.env.prod.example) to `.env.prod`.
4. Edit `.env.prod` and replace all placeholders.
5. Make sure `POSTGRES_PASSWORD` and the password embedded in `DATABASE_URL` match in `.env.prod`.
6. Pull images:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull
```

7. Start Postgres and Redis:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d db redis
```

8. Run migrations:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate
```

9. Start the full stack, including Compose-managed Nginx and Certbot:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

10. Watch Certbot logs until the first certificate is issued:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f certbot
```

11. Verify HTTPS after Certbot has issued the certificate and Nginx has reloaded automatically:

```bash
curl -I https://api.example.com/live
```

## Health Checks After Deploy

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs --tail=100 api
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs --tail=100 nginx
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs --tail=100 certbot
curl -I https://api.example.com/live
curl -I https://api.example.com/ready
```

## Updating To A New Release

1. Change the image tag in [docker-compose.prod.yml](/Users/alisher/University/bisp/pauza-server/docker-compose.prod.yml).
2. Pull the new API image:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull api
```

3. Run migrations:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate
```

4. Restart the application services:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d api nginx
```

5. Certificates renew automatically in the `certbot` service. Verify renewal health with:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs --tail=100 certbot
```

## Optional Host-Managed Nginx

If you do not want Compose-managed Nginx:

1. Install Nginx on the host.
2. Copy [deploy/nginx/pauza.conf](/Users/alisher/University/bisp/pauza-server/deploy/nginx/pauza.conf) to the server.
3. Replace all placeholders in that file.
4. Disable or remove the `nginx` service from the Compose deployment.

In that setup, the API should still listen on `127.0.0.1:8080` and Nginx should
serve `/photos/` from the machine-local photo directory while redirecting HTTP
to HTTPS.
