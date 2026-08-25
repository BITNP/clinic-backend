# clinic-backend

New backend for clinic management system powered by Gin, with better architecture, better database table design, better document.

## Run locally

```bash
# under clinic-backend

# 1. Seed sample data (room, service dates, announcement, staff)
go run fake/seed.go

# 2. Start the fake CAS server (now gives admin role)
go run fake/fake_cas.go   # runs on :9999

# 3. Start the backend
export CLINIC_API_KEY=local-dev-key
export CAS_SERVER_URL=http://127.0.0.1:9999
export APP_BASE_URL=http://127.0.0.1:5173
export CAS_DEFAULT_REDIRECT=/
export SESSION_COOKIE_SAMESITE=lax

go run main.go                   # runs on :8080

# 4. Start the frontend (separate terminal)
cd /path/to/clinic_admin_frontend
pnpm dev                         # runs on :5173
```

## Run with Docker

Boots PostgreSQL and the backend together:

```bash
# under clinic-backend
cp .env.example .env   # optional, to customize config
docker compose up --build
```

- Postgres runs on `:5432` (user/db/password all `clinic`, persisted in a volume).
- Backend runs on `:8080` with `CLINIC_DB_DRIVER=postgres`.
- Override config via `.env` (see `.env.example`) or environment variables, e.g.
  `CLINIC_API_KEY=secret docker compose up --build`.

The database schema is created automatically on startup via AutoMigrate. To
load sample data, seed against the Postgres instance with
`go run fake/seed.go` (currently sqlite-only, so run it locally first).
