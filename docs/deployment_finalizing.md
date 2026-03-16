# Pauza Server — End-to-End Deployment Guide

Complete instructions for deploying the Pauza API server on a fresh Ubuntu VPS.

**Stack:** Go API + Traefik (reverse proxy & automatic TLS) + PostgreSQL 16 + Redis 7
**Orchestration:** Docker Compose
**CI/CD:** GitHub Actions → GHCR → SSH deploy
**Registry:** `ghcr.io/pauzaa/pauza-server`

This repository supports two production topologies:

- **Bundled Traefik**: Pauza runs its own `traefik` container on ports `80` and `443`.
- **Shared Traefik**: Pauza joins an existing external Docker network with a pre-existing Traefik instance already bound to `80` and `443`.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Server Initial Setup](#2-server-initial-setup)
3. [Install Docker](#3-install-docker)
4. [DNS Configuration](#4-dns-configuration)
5. [Clone & Configure the Project](#5-clone--configure-the-project)
6. [Configure Environment Variables](#6-configure-environment-variables)
7. [Authenticate with GHCR](#7-authenticate-with-ghcr)
8. [First Deploy (Manual)](#8-first-deploy-manual)
9. [Verify the Deployment](#9-verify-the-deployment)
10. [Set Up GitHub Actions Secrets](#10-set-up-github-actions-secrets)
11. [CI/CD Pipeline Overview](#11-cicd-pipeline-overview)
12. [TLS Certificate Management](#12-tls-certificate-management)
13. [Routine Operations](#13-routine-operations)
14. [Backups](#14-backups)
15. [Log Rotation](#15-log-rotation)
16. [Troubleshooting](#16-troubleshooting)

---

## 1. Prerequisites

Before you begin, make sure you have:

- A **domain name** pointed at your server (e.g. `api.pauza.dev`)
- An **Ubuntu 22.04+ VPS** with at least 1 GB RAM and 20 GB disk
- **Root or sudo access** to the server
- A **GitHub account** with access to the `pauzaa/pauza-server` repository
- The following credentials ready:
  - PostgreSQL password (generate a strong random string)
  - Redis password (generate a strong random string)
  - JWT secret (minimum 32 bytes, random)
  - Admin seed password
  - Resend SMTP API key (for transactional email)
  - RevenueCat API key and webhook secret
  - Firebase service account JSON (for push notifications)
  - OpenAI or Gemini API key (for AI analysis endpoints; optional)

Generate secure passwords:

```bash
# Run locally or on the server — generates a 32-char random string
openssl rand -base64 32
```

---

## 2. Server Initial Setup

SSH into your server:

```bash
ssh root@YOUR_SERVER_IP
```

### 2.1 Update the system

```bash
apt update && apt upgrade -y
```

### 2.2 Create a deploy user

Do not run Docker as root. Create a dedicated user:

```bash
adduser deploy
usermod -aG sudo deploy
```

### 2.3 Set up SSH key authentication for the deploy user

On your **local machine**, generate a dedicated deploy key:

```bash
ssh-keygen -t ed25519 -C "pauza-deploy" -f ~/.ssh/pauza_deploy_key
```

Copy the public key to the server:

```bash
ssh-copy-id -i ~/.ssh/pauza_deploy_key.pub deploy@YOUR_SERVER_IP
```

Test that it works:

```bash
ssh -i ~/.ssh/pauza_deploy_key deploy@YOUR_SERVER_IP
```

### 2.4 Harden SSH (optional but recommended)

Edit `/etc/ssh/sshd_config`:

```bash
sudo nano /etc/ssh/sshd_config
```

Set:

```
PermitRootLogin no
PasswordAuthentication no
```

Restart SSH:

```bash
sudo systemctl restart sshd
```

### 2.5 Configure the firewall

```bash
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status
```

Expected output:

```
Status: active

To                         Action      From
--                         ------      ----
OpenSSH                    ALLOW       Anywhere
80/tcp                     ALLOW       Anywhere
443/tcp                    ALLOW       Anywhere
```

---

## 3. Install Docker

Run all commands as the `deploy` user.

```bash
# Remove any old Docker packages
sudo apt remove -y docker docker-engine docker.io containerd runc 2>/dev/null

# Install dependencies
sudo apt update
sudo apt install -y ca-certificates curl gnupg

# Add Docker GPG key
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

# Add the Docker repository
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# Install Docker Engine and Compose plugin
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Add deploy user to docker group (avoids needing sudo for docker commands)
sudo usermod -aG docker deploy
```

**Log out and back in** for the group change to take effect:

```bash
exit
ssh -i ~/.ssh/pauza_deploy_key deploy@YOUR_SERVER_IP
```

Verify:

```bash
docker --version
docker compose version
```

---

## 4. DNS Configuration

Go to your DNS provider and create an **A record**:

| Type | Name | Value | TTL |
|------|------|-------|-----|
| A | `api` (or `api.pauza.dev`) | `YOUR_SERVER_IP` | 300 |

Verify propagation:

```bash
dig +short api.pauza.dev
# Should return YOUR_SERVER_IP
```

Wait for DNS to propagate before proceeding. Let's Encrypt validation will fail if DNS is not pointing to your server.

---

## 5. Clone & Configure the Project

```bash
# Choose a deployment directory
sudo mkdir -p /opt/pauza-server
sudo chown deploy:deploy /opt/pauza-server
cd /opt/pauza-server

# Clone the repository
git clone https://github.com/pauzaa/pauza-server.git .
```

### 5.1 Choose your deployment topology

Use one of these approaches:

- **Bundled Traefik**: Use `docker-compose.prod.yml` exactly as-is. This is the default path in the rest of the guide.
- **Shared Traefik**: Add `docker-compose.prod.shared-traefik.yml` to every production Compose command. This keeps Pauza's `traefik` service disabled and connects the API to your existing external `public` network.

The shared Traefik mode expects:

- An existing external Docker network named `public`
- An existing Traefik instance watching that network
- `PUBLIC_DOMAIN` DNS already pointed at the server

---

## 6. Configure Environment Variables

### 6.1 Create the production env file

```bash
cp .env.prod.example .env.prod
```

### 6.2 Edit every placeholder value

```bash
nano .env.prod
```

Here is a reference for every variable — replace all `replace_me*` values:

```dotenv
PORT=8080
LOG_LEVEL=info
TRUSTED_PROXIES=172.30.0.0/24

# If deploying behind an existing shared Traefik instance, replace the value
# above with the shared proxy network subnet instead, for example:
# TRUSTED_PROXIES=172.18.0.0/16

# Docker image tag to deploy. CI publishes both `latest` and `sha-<commit>`
# tags. Prefer `sha-<commit>` in production for deterministic deploys.
IMAGE_TAG=latest

# Your actual domain (must match DNS) — used by Traefik for routing and ACME
PUBLIC_DOMAIN=api.pauza.dev
LETSENCRYPT_EMAIL=admin@pauza.dev

# PostgreSQL — use a strong generated password
POSTGRES_USER=pauza
POSTGRES_PASSWORD=<GENERATED_PASSWORD>
POSTGRES_DB=pauza
DATABASE_URL=postgres://pauza:<GENERATED_PASSWORD>@db:5432/pauza?sslmode=disable

# JWT — must be at least 32 bytes, random
JWT_SECRET=<GENERATED_SECRET>
JWT_ACCESS_TOKEN_TTL=15m
JWT_REFRESH_TOKEN_TTL=720h

# SMTP via Resend
SMTP_HOST=smtp.resend.com
SMTP_PORT=587
SMTP_USERNAME=resend
SMTP_PASSWORD=<YOUR_RESEND_API_KEY>
SMTP_FROM=noreply@mail.pauza.dev
SMTP_TIMEOUT=30s
SMTP_TLS_POLICY=mandatory

# Admin account (used by cmd/seed-admin, not applied during migration)
ADMIN_SEED_USERNAME=admin
ADMIN_SEED_PASSWORD=<STRONG_ADMIN_PASSWORD>

# RevenueCat
REVENUECAT_API_KEY=<YOUR_REVENUECAT_KEY>
REVENUECAT_WEBHOOK_SECRET=<YOUR_WEBHOOK_SECRET>

# Firebase push notifications (paste the full JSON on one line)
FIREBASE_SERVICE_ACCOUNT_JSON={"type":"service_account","project_id":"..."}

# Redis — use a strong generated password
REDIS_PASSWORD=<GENERATED_PASSWORD>
REDIS_URL=redis://:<GENERATED_PASSWORD>@redis:6379/0

# Photo storage
PHOTO_STORAGE_DIR=/var/lib/pauza/photos
PHOTO_PUBLIC_BASE_URL=https://api.pauza.dev/photos

# AI analysis (leave AI_PROVIDER empty to disable AI endpoints)
AI_PROVIDER=openai
OPENAI_API_KEY=<YOUR_OPENAI_API_KEY>
# GEMINI_API_KEY=
# AI_MODEL=
AI_RATE_LIMIT=10
AI_RATE_WINDOW=1h

# Cleanup intervals
CLEANUP_INTERVAL=1h
OTP_RETENTION_PERIOD=24h
REFRESH_TOKEN_REVOKED_RETENTION=168h
```

**Important:** The `<GENERATED_PASSWORD>` in `REDIS_PASSWORD` and in `REDIS_URL` must be the same value. Same for `POSTGRES_PASSWORD` and the password in `DATABASE_URL`.

### 6.3 Lock down permissions

```bash
chmod 600 .env.prod
```

---

## 7. Authenticate with GHCR

The production compose file pulls the API image from `ghcr.io/pauzaa/pauza-server`. You need to authenticate Docker with GitHub Container Registry on the server.

If the package is public, anonymous pulls work and this step can be skipped.

### 7.1 Create a GitHub Personal Access Token (PAT)

1. Go to **GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)**
2. Click **Generate new token (classic)**
3. Give it a name like `pauza-server-deploy`
4. Select scope: **`read:packages`**
5. Generate and copy the token

### 7.2 Log in to GHCR on the server

```bash
echo "<YOUR_GITHUB_PAT>" | docker login ghcr.io -u <YOUR_GITHUB_USERNAME> --password-stdin
```

Verify:

```bash
docker pull ghcr.io/pauzaa/pauza-server:${IMAGE_TAG:-latest}
```

If the image has not been pushed yet (first-time setup), this will fail — that is expected. The image will be available after the first CI run on `main`.

---

## 8. First Deploy (Manual)

The first deployment must be done manually because:
- The database needs to be initialized
- You need to verify everything works before enabling CI/CD

If you are using an existing host-level Traefik instance on an external `public`
network, append `-f docker-compose.prod.shared-traefik.yml` to every
`docker compose` command in sections 8.1 and 8.2 below. In that mode, Pauza's
own `traefik` service is disabled, so `docker compose up -d` starts only `api`,
`db`, and `redis`.

### 8.1 If CI has already pushed an image

If the CI pipeline has already run and pushed an image to GHCR:

```bash
cd /opt/pauza-server

# Pull all images
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml pull

# Start database and Redis first
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d db redis

# Wait for them to become healthy (~15 seconds)
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml ps

# Run database migrations
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate

# Start all services
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### 8.2 If no image exists yet (build locally on server)

If you need to deploy before CI pushes an image:

```bash
cd /opt/pauza-server

# Build the image locally
docker build -t ghcr.io/pauzaa/pauza-server:${IMAGE_TAG:-latest} .

# Start database and Redis first
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d db redis

# Wait for healthy status
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml ps

# Run migrations
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate

# Start all services
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### 8.3 Seed the admin account

The admin user is **not** created by migrations. You must run `cmd/seed-admin` separately. This binary is not included in the Docker image, so build and run it from the cloned repo on the server (requires Go installed):

```bash
cd /opt/pauza-server
set -a; source .env.prod; set +a
go run ./cmd/seed-admin
```

If Go is not installed on the server, you can build the binary locally and copy it:

```bash
# On your local machine
CGO_ENABLED=0 GOOS=linux go build -o seed-admin ./cmd/seed-admin
scp seed-admin deploy@YOUR_SERVER_IP:/opt/pauza-server/

# On the server
cd /opt/pauza-server
set -a; source .env.prod; set +a
./seed-admin
```

This only needs to run once (or whenever you need to reset the admin password).

### 8.4 What happens on first start

1. **db** and **redis** start and become healthy
2. **api** starts, connects to db and redis, exposes port 8080 on localhost
3. **traefik** starts, discovers the api service via Docker labels
4. Traefik automatically requests a TLS certificate from Let's Encrypt via HTTP-01 challenge
5. HTTPS is live once the certificate is issued (typically within seconds)

Watch the logs:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f traefik
```

You should see Traefik successfully obtain the certificate via ACME.

---

## 9. Verify the Deployment

### 9.1 Check all services are running

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
```

All services should show `Up (healthy)` or `Up`:

```
NAME          STATUS
api           Up (healthy)
traefik       Up
db            Up (healthy)
redis         Up (healthy)
```

### 9.2 Test the health endpoint

```bash
# From the server itself
curl http://localhost:8080/live

# From anywhere (after TLS is active)
curl -I https://api.pauza.dev/live
```

Expected: HTTP `200 OK`.

### 9.3 Verify TLS and security headers

```bash
curl -I https://api.pauza.dev/live
```

Expected headers in the response:

```
HTTP/2 200
strict-transport-security: max-age=63072000; includeSubDomains
x-content-type-options: nosniff
x-frame-options: DENY
```

### 9.4 Check logs for errors

```bash
# All services
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs --tail=50

# Specific service
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs api --tail=50
```

---

## 10. Set Up GitHub Actions Secrets

The CI/CD pipeline needs four secrets to deploy automatically. These must be configured in the GitHub repository settings.

### 10.1 Generate a deploy SSH key pair

On your **local machine** (or anywhere secure):

```bash
ssh-keygen -t ed25519 -C "github-actions-deploy" -f ./github_deploy_key -N ""
```

This creates two files:
- `github_deploy_key` — the **private** key (goes into GitHub secrets)
- `github_deploy_key.pub` — the **public** key (goes on the server)

### 10.2 Add the public key to the server

```bash
# Copy the public key content
cat github_deploy_key.pub
```

SSH into the server as the `deploy` user and add the key:

```bash
ssh deploy@YOUR_SERVER_IP

# Append the public key
echo "PUBLIC_KEY_CONTENT_HERE" >> ~/.ssh/authorized_keys

# Verify permissions
chmod 700 ~/.ssh
chmod 600 ~/.ssh/authorized_keys
```

### 10.3 Add secrets to GitHub

Go to your repository: **Settings → Secrets and variables → Actions → New repository secret**

Add the following five secrets:

| Secret Name | Value | How to Get It |
|-------------|-------|---------------|
| `DEPLOY_HOST` | Your server IP or hostname | e.g. `203.0.113.42` or `api.pauza.dev` |
| `DEPLOY_USER` | `deploy` | The SSH username created in step 2.2 |
| `DEPLOY_SSH_KEY` | Contents of `github_deploy_key` | `cat github_deploy_key` — copy the **entire** private key including `-----BEGIN/END-----` lines |
| `DEPLOY_PATH` | `/opt/pauza-server` | Absolute path to the project directory on the server |
| `DEPLOY_MODE` | `bundled` or `shared-traefik` | Use `shared-traefik` when the host already has its own Traefik on the external `public` network |

To add each secret:

1. Click **New repository secret**
2. Enter the **Name** exactly as shown above
3. Paste the **Value**
4. Click **Add secret**

### 10.4 Clean up the key pair from your local machine

```bash
rm github_deploy_key github_deploy_key.pub
```

### 10.5 Verify secrets are set

Go to **Settings → Secrets and variables → Actions**. You should see all five secrets listed (values are hidden):

```
DEPLOY_HOST     Updated just now
DEPLOY_MODE     Updated just now
DEPLOY_PATH     Updated just now
DEPLOY_SSH_KEY  Updated just now
DEPLOY_USER     Updated just now
```

**Note:** The `GITHUB_TOKEN` used for GHCR authentication is provided automatically by GitHub Actions — no extra secret is needed for pushing Docker images.

---

## 11. CI/CD Pipeline Overview

The file `.github/workflows/ci.yml` defines the full pipeline. Here is what happens on every push to `main`:

```
push to main
     │
     ├──► unit tests (go vet + go test)
     │
     ├──► integration tests (with real Postgres + Redis)
     │
     └──► (both pass)
              │
              ▼
        build-and-push
          - Builds Docker image
          - Pushes to ghcr.io/pauzaa/pauza-server:latest
          - Pushes to ghcr.io/pauzaa/pauza-server:sha-<commit>
              │
              ▼
          deploy
          - SSHes into production server
          - Sets IMAGE_TAG=sha-<commit>
          - Pulls the new image
          - Starts db + redis
          - Runs database migrations
          - Restarts api (+ traefik only in bundled mode)
```

On **pull requests**, only the unit and integration test jobs run (no build/push/deploy).

### Triggering a deploy

Any push to `main` that passes tests will automatically deploy. This includes:

- Merging a pull request into `main`
- Pushing directly to `main`

### Monitoring a deploy

Go to the repository **Actions** tab to see the pipeline status. Each run shows all four jobs and their logs.

---

## 12. TLS Certificate Management

TLS certificates are managed automatically by **Traefik** using the ACME protocol (Let's Encrypt).

### How it works

- On **first start**: Traefik requests a new certificate from Let's Encrypt via HTTP-01 challenge
- **Automatic renewal**: Traefik monitors certificate expiry and renews automatically (Let's Encrypt certs expire after 90 days, Traefik renews well before expiry)
- Certificates are stored in the `traefik-certs` Docker volume as `acme.json`

### Check certificate status

```bash
# View Traefik logs for ACME activity
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs traefik | grep -i acme
```

### Force certificate renewal

Traefik handles renewals automatically. If you need to force a fresh certificate (e.g. after a domain change), remove the ACME storage and restart:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml down traefik
docker volume rm $(docker volume ls -q | grep traefik-certs)
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d traefik
```

---

## 13. Routine Operations

### View logs

```bash
cd /opt/pauza-server

# All services, follow mode
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f

# Specific service, last 100 lines
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs api --tail=100

# Shorthand alias — persist it in your shell profile:
echo 'alias dc="docker compose -f docker-compose.yml -f docker-compose.prod.yml"' >> ~/.bashrc
source ~/.bashrc
# Then: dc logs -f api
```

### Restart a service

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml restart api
```

### Stop everything

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml down
```

### Stop and remove volumes (destroys all data)

```bash
# DANGER: This deletes the database, Redis data, uploaded photos, and TLS certificates
docker compose -f docker-compose.yml -f docker-compose.prod.yml down -v
```

### Run migrations manually

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate
```

### Pull and restart with the latest image (manual deploy)

```bash
cd /opt/pauza-server
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml pull api
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d db redis
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate
docker compose --env-file .env.prod -f docker-compose.yml -f docker-compose.prod.yml up -d api traefik
```

If you are using the shared Traefik topology, append `-f docker-compose.prod.shared-traefik.yml`
to each command above and start only `api` in the final step.

### Update the repo on the server (for compose/config changes)

```bash
cd /opt/pauza-server
git pull origin main
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### Connect to the database

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec db \
  psql -U pauza -d pauza
```

### Connect to Redis

```bash
# Source the env file first so $REDIS_PASSWORD is available to the shell
set -a; source .env.prod; set +a
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec redis \
  redis-cli -a "$REDIS_PASSWORD"
```

### Check disk usage

```bash
docker system df
```

### Clean up old images

```bash
docker image prune -af
```

---

## 14. Backups

### 14.1 PostgreSQL

Dump the database on a schedule (e.g. daily cron) and copy the file off-server:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec db \
  pg_dump -U pauza -d pauza --format=custom -f /tmp/pauza_backup.dump

docker cp "$(docker compose -f docker-compose.yml -f docker-compose.prod.yml ps -q db)":/tmp/pauza_backup.dump \
  /opt/pauza-server/backups/pauza_$(date +%Y%m%d).dump
```

To restore:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec -T db \
  pg_restore -U pauza -d pauza --clean --if-exists < /opt/pauza-server/backups/pauza_YYYYMMDD.dump
```

### 14.2 Photo uploads

The `photodata` Docker volume holds user-uploaded profile photos. Back it up with:

```bash
docker run --rm -v pauza-server_photodata:/data -v /opt/pauza-server/backups:/backup \
  alpine tar czf /backup/photos_$(date +%Y%m%d).tar.gz -C /data .
```

### 14.3 Redis

Redis data (rate-limit counters) is ephemeral and does not need backup. The server recovers gracefully after a Redis data loss.

---

## 15. Log Rotation

Docker container logs grow unbounded by default. Configure a size cap by adding a top-level `x-logging` anchor to `docker-compose.yml` or per-service `logging` blocks in the prod override:

```yaml
# Example: add to docker-compose.prod.yml under the api service
    logging:
      driver: json-file
      options:
        max-size: "20m"
        max-file: "5"
```

Apply the same to `traefik`, `db`, and `redis` to prevent disk exhaustion on long-running servers.

---

## 16. Troubleshooting

### Traefik fails to get a certificate

**Symptoms:** HTTPS not working, browser shows certificate error.

**Check:**

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs traefik
```

**Common causes:**
- DNS not pointing to the server — verify with `dig +short api.pauza.dev`
- Port 80 blocked by firewall — verify with `sudo ufw status`
- `PUBLIC_DOMAIN` in `.env.prod` doesn't match your DNS record
- `LETSENCRYPT_EMAIL` not set in `.env.prod`
- Let's Encrypt rate limit hit — check logs for rate limit errors (wait 1 hour and retry)

### API container keeps restarting

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs api --tail=50
```

**Common causes:**
- Database not ready — check `db` service health
- Wrong `DATABASE_URL` — password mismatch between `POSTGRES_PASSWORD` and URL
- Wrong `REDIS_URL` — password mismatch between `REDIS_PASSWORD` and URL

### Redis authentication errors

**Symptoms:** `NOAUTH Authentication required` or `ERR invalid password` in api logs.

**Fix:** Ensure `REDIS_PASSWORD` in `.env.prod` matches the password in `REDIS_URL`:

```dotenv
REDIS_PASSWORD=mysecretpassword
REDIS_URL=redis://:mysecretpassword@redis:6379/0
```

### "Permission denied" on Docker commands

The `deploy` user must be in the `docker` group:

```bash
sudo usermod -aG docker deploy
# Log out and back in
```

### Deploy job fails with SSH error

**Check:**
- `DEPLOY_HOST` secret is correct (IP or hostname)
- `DEPLOY_USER` secret matches the user on the server
- `DEPLOY_SSH_KEY` contains the **full** private key (including `-----BEGIN` and `-----END` lines)
- The corresponding public key is in `~/.ssh/authorized_keys` on the server
- Port 22 is open: `sudo ufw status`

Test from your local machine with the same key:

```bash
ssh -i github_deploy_key deploy@YOUR_SERVER_IP "echo ok"
```

### Image pull fails on the server

If `docker pull ghcr.io/pauzaa/pauza-server:latest` fails:

```bash
# Re-authenticate with GHCR
echo "<YOUR_GITHUB_PAT>" | docker login ghcr.io -u <YOUR_GITHUB_USERNAME> --password-stdin
```

Make sure the PAT has `read:packages` scope and the package visibility matches (public or you have access).

### High disk usage from Docker

```bash
# See what's using space
docker system df

# Remove unused images, containers, and build cache
docker system prune -af

# Remove unused volumes (CAREFUL: won't touch named volumes in use)
docker volume prune -f
```

### Migrating from nginx+certbot to Traefik

If deploying on a server that previously ran the nginx+certbot stack, the existing `letsencrypt` Docker volume contains certbot's directory structure (not Traefik's `acme.json`). Remove it before the first Traefik deploy:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml down
docker volume rm $(docker volume ls -q | grep traefik-certs)
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```
