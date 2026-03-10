# Profile Photo Storage

Profile photos are stored on local disk on the deployed machine. The Go API
writes uploaded files into `PHOTO_STORAGE_DIR` and returns URLs rooted at
`PHOTO_PUBLIC_BASE_URL`.

The API does not serve those files directly. A reverse proxy such as Nginx must
publish the same directory at the public `/photos/` path.

## Required deployment contract

- `PHOTO_STORAGE_DIR` is writable by the API process.
- `PHOTO_STORAGE_DIR` is readable by Nginx.
- `PHOTO_PUBLIC_BASE_URL` exactly matches the public Nginx path that serves the
  directory, for example `https://api.example.com/photos`.
- The photo directory must be backed by persistent storage in production.

## Example mapping

- API config:
  - `PHOTO_STORAGE_DIR=/srv/pauza/photos`
  - `PHOTO_PUBLIC_BASE_URL=https://api.example.com/photos`
- Nginx:
  - public route: `/photos/<filename>`
  - local directory: `/srv/pauza/photos/`

Compose usage:

- Development: `docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build` using `.env.dev`
- Production-style app stack: `docker compose -f docker-compose.yml -f docker-compose.prod.yml pull && docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d` using `.env.prod` with local Compose-managed Postgres and Redis
- Production migration step: `docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate`

Nginx config artifacts:

- [deploy/nginx/development.conf](/Users/alisher/University/bisp/pauza-server/deploy/nginx/development.conf) for local Docker Compose development
- [deploy/nginx/production-compose.conf](/Users/alisher/University/bisp/pauza-server/deploy/nginx/production-compose.conf) for Compose-managed production `nginx`
- [deploy/nginx/pauza.conf](/Users/alisher/University/bisp/pauza-server/deploy/nginx/pauza.conf) as a host-ready backend config with placeholder values to replace before installation
