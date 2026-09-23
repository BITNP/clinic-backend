# clinic-backend

Backend for the clinic management system, built with Go and Gin.

- Dev: the backend and the admin frontend run as separate compose stacks
  (`docker-compose.yml` in each repo).
- Prod: `docker-compose.prod.yml` deploys both as a single stack, pulling
  pre-built images from GHCR (backend on `:8080`, admin frontend on `:5173`).

## Deploy the backend

```bash
cd clinic-backend
cp .env.example .env   # optional, to customize config
docker compose up --build   # build locally
```

## Deploy from GitHub Packages (no local build)

On every push to `master`, CI (`.github/workflows/docker-publish.yml`)
builds and publishes the image to
`ghcr.io/bitnp/clinic-backend` (tag `latest`). Pushing a `v*` tag also
publishes `1.2.3` / `1.2` tags. No local Go toolchain or Docker build is
needed on the target machine:

```bash
# with docker compose — pulls the image instead of building
docker compose up -d --pull always

# or as a one-off container
docker run -d --name clinic-backend -p 8080:8080 \
  -e CLINIC_API_KEY=your-secret \
  ghcr.io/bitnp/clinic-backend:latest
```

The image must be **public** for anonymous pulls on other machines:
repo → Packages → package settings → *Change visibility*. For a private
image, log in first:

```bash
echo $PAT | docker login ghcr.io -u Potato-Yao --password-stdin
```

## Deploy to a server

`docker-compose.prod.yml` is a standalone deploy config — only this file, `.env`
and the packaged image tarball are needed on the server, no source checkout. It
runs the backend, the admin frontend and a bundled Redis service, and connects
to the Postgres already running on the server via `host.containers.internal` (no
bundled database), like the other clinic services. It refuses to start if
`CLINIC_API_KEY` is missing.

The server cannot pull the bundled Redis image from Docker Hub, so package the
images on a machine that can and copy the tarball over:

```bash
# On your local machine: create .env from the example and fill in the secrets
# (CLINIC_API_KEY, APP_BASE_URL, CAS_SERVER_URL, ...).
cp .env.example .env
# set CLINIC_DB_DSN in .env to override the default
# (postgres://clinic:clinic@host.containers.internal:5432/clinic?sslmode=disable)

# Pull and bundle all three images into a single tarball.
docker pull ghcr.io/bitnp/clinic-backend:${BACKEND_IMAGE_TAG:-latest}
docker pull ghcr.io/bitnp/clinic_admin_frontend:${ADMIN_FRONTEND_IMAGE_TAG:-latest}
docker pull redis:7-alpine
docker save -o clinic-images.tar \
  ghcr.io/bitnp/clinic-backend:${BACKEND_IMAGE_TAG:-latest} \
  ghcr.io/bitnp/clinic_admin_frontend:${ADMIN_FRONTEND_IMAGE_TAG:-latest} \
  redis:7-alpine

scp clinic-images.tar docker-compose.prod.yml .env server:

# On the server: load the images and start the stack.
docker load -i clinic-images.tar
docker compose -f docker-compose.prod.yml up -d
```

Do not use `--pull always` or `docker compose pull` for this stack: those bypass
`pull_policy` and would try to fetch Redis from Docker Hub, which the server
cannot reach. The bundled Redis service has `pull_policy: never` and is served
from the loaded tarball.

The frontend (Caddy on `:5173`) proxies `/api`, `/login`, `/logout` to the
backend over the compose network, so the backend needs no host port.

- Postgres: `:5432` on the host (`host.containers.internal`; user/db/password
  all `clinic` by default).
- Redis: `:6379` bundled in the stack (compose-internal, persisted in the
  `redisdata` volume; the backend refuses to start without it). Set
  `REDIS_PASSWORD` to require a password.
- Admin frontend: `:5173`.
- Backend: `:8080` (compose-internal; expose a host port if you need to reach
  it directly).
- Override any config via `.env` (see `.env.example`) or environment
  variables, e.g. `CLINIC_API_KEY=secret docker compose up --build`.

To pin specific image versions instead of `latest`, set `BACKEND_IMAGE_TAG` and
`ADMIN_FRONTEND_IMAGE_TAG` in `.env`, and use those same tags in the `docker
pull` / `docker save` commands above.

The database schema is created automatically on startup via AutoMigrate. To
load sample data (rooms, service dates, announcements, staff), run the seed
script once against the database:

```bash
# seed.go is currently sqlite-only, so run it locally against a sqlite DB:
go run fake/seed.go
```

## Deploy the frontend

```bash
cd clinic_admin_frontend
cp .env.example .env   # optional, to customize config
docker compose up --build
```

- Admin frontend: `:5173` — serves the UI and proxies `/api`, `/login`,
  `/logout` to the backend.
- By default it reaches the backend on the host at `:8080`
  (`BACKEND_UPSTREAM=http://host.docker.internal:8080`). If the backend runs on
  another server, set `BACKEND_UPSTREAM` in `.env` to its URL, e.g.
  `BACKEND_UPSTREAM=http://api.example.com:8080`.

## Wire them together

For CAS login/logout to redirect back to the frontend, set `APP_BASE_URL` on
the backend to the frontend's address (default `http://localhost:5173`). The
frontend proxies `/api`, `/login`, `/logout` to the backend, so no CORS setup
is needed — requests stay same-origin from the browser's perspective.

## Run locally (development)

```bash
# 1. Start the fake CAS server (gives admin role)
go run fake/fake_cas.go        # runs on :9999

# 2. Start Redis (required; see docker-compose.yml for the dev service)
docker run --rm -p 6379:6379 redis:7-alpine

# 3. Start the backend
export CLINIC_API_KEY=local-dev-key
export REDIS_ADDR=127.0.0.1:6379
export REDIS_PASSWORD=
export REDIS_DB=0
export CAS_SERVER_URL=http://127.0.0.1:9999
export APP_BASE_URL=http://127.0.0.1:5173
export CAS_DEFAULT_REDIRECT=/
export SESSION_COOKIE_SAMESITE=lax
# Optional: raise to force all staff to log in again; defaults to 0
export STAFF_VERSION=0
go run .                       # runs on :8080

# 4. Start the frontend (separate terminal)
cd path/to/clinic_admin_frontend
pnpm dev                       # runs on :5173
```

Sessions whose staff `version` differs from `STAFF_VERSION` are rejected until the staff log in again, so each time you raise `STAFF_VERSION` all staff are forced through the login page once.

## Data sync counters

The backend keeps one change counter per data group in Redis
(`clinic:sync:<group>`), incremented whenever that group's data changes:

| Group | Key | Changed by |
| --- | --- | --- |
| record | `clinic:sync:record` | admin record actions, customer tickets/wechat, nightly cleanup, record tagger |
| room | `clinic:sync:room` | room create/update/delete |
| work_schedule | `clinic:sync:work_schedule` | work schedule create/update/delete and staff/weekday edits |
| service_date | `clinic:sync:service_date` | service date create/update/delete |
| staff | `clinic:sync:staff` | staff create/update/delete, CAS/Keycloak login upserts |
| announcement | `clinic:sync:announcement` | announcement create/update/delete |

Clients read the current values from `GET /api/admin/sync` (admin/staff session)
or `GET /api/sync` (API-key client), keep the values they last saw, and refetch
their data when a value differs. Counters are best-effort: a Redis failure is
logged and never fails the underlying write.

