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

`docker-compose.prod.yml` is a standalone deploy config — copy it and `.env`
to the server, no source checkout needed. It pulls the pre-built backend and
admin-frontend images from GHCR and connects to the Postgres already running
on the server via `host.containers.internal:5432` (no bundled database), like
the other clinic services. It refuses to start if `CLINIC_API_KEY` is missing:

```bash
cp .env.example .env   # set CLINIC_API_KEY, APP_BASE_URL, CAS_SERVER_URL, ...
# set CLINIC_DB_DSN in .env to override the default
# (postgres://clinic:clinic@host.containers.internal:5432/clinic?sslmode=disable)
docker compose -f docker-compose.prod.yml up -d --pull always
```

The frontend (Caddy on `:5173`) proxies `/api`, `/login`, `/logout` to the
backend over the compose network, so the backend needs no host port.

- Postgres: `:5432` (user/db/password all `clinic`, persisted in a volume).
- Admin frontend: `:5173`.
- Backend: `:8080` (compose-internal; expose a host port if you need to reach
  it directly).
- Override any config via `.env` (see `.env.example`) or environment
  variables, e.g. `CLINIC_API_KEY=secret docker compose up --build`.

To pin specific image versions instead of `latest`:

```bash
echo BACKEND_IMAGE_TAG=1.2.3 >> .env
echo ADMIN_FRONTEND_IMAGE_TAG=1.2.3 >> .env
docker compose -f docker-compose.prod.yml pull && docker compose -f docker-compose.prod.yml up -d
```

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

# 2. Start the backend
export CLINIC_API_KEY=local-dev-key
export CAS_SERVER_URL=http://127.0.0.1:9999
export APP_BASE_URL=http://127.0.0.1:5173
export CAS_DEFAULT_REDIRECT=/
export SESSION_COOKIE_SAMESITE=lax
# Optional: raise to force all staff to log in again; defaults to 0
export STAFF_VERSION=0
go run main.go                 # runs on :8080

# 3. Start the frontend (separate terminal)
cd path/to/clinic_admin_frontend
pnpm dev                       # runs on :5173
```

Sessions whose staff `version` differs from `STAFF_VERSION` are rejected until the staff log in again, so each time you raise `STAFF_VERSION` all staff are forced through the login page once.
