# clinic-backend

Backend for the clinic management system, built with Go and Gin.

The backend and the admin frontend are deployed as separate compose stacks:

- `clinic-backend` (this repo): PostgreSQL + backend (Go API on `:8080`), via `docker-compose.yml`
- `clinic_admin_frontend` (separate repo): admin frontend (Caddy on `:5173`), via its own `docker-compose.yml`

The two repos are independent and can live anywhere on disk.

## Deploy the backend

```bash
cd clinic-backend
cp .env.example .env   # optional, to customize config
docker compose up --build   # build locally
```

## Deploy from GitHub Packages (no local build)

On every push to `master`, CI (`.github/workflows/docker-publish.yml`)
builds and publishes the image to
`ghcr.io/potato-yao/clinic-backend` (tag `latest`). Pushing a `v*` tag also
publishes `1.2.3` / `1.2` tags. No local Go toolchain or Docker build is
needed on the target machine:

```bash
# with docker compose — pulls the image instead of building
docker compose up -d --pull always

# or as a one-off container
docker run -d --name clinic-backend -p 8080:8080 \
  -e CLINIC_API_KEY=your-secret \
  ghcr.io/potato-yao/clinic-backend:latest
```

The image must be **public** for anonymous pulls on other machines:
repo → Packages → package settings → *Change visibility*. For a private
image, log in first:

```bash
echo $PAT | docker login ghcr.io -u Potato-Yao --password-stdin
```

- Postgres: `:5432` (user/db/password all `clinic`, persisted in a volume).
- Backend: `:8080`.
- Override any config via `.env` (see `.env.example`) or environment
  variables, e.g. `CLINIC_API_KEY=secret docker compose up --build`.

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
go run main.go                 # runs on :8080

# 3. Start the frontend (separate terminal)
cd path/to/clinic_admin_frontend
pnpm dev                       # runs on :5173
```
