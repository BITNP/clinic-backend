# clinic-backend

Backend for the clinic management system, built with Go and Gin.

- Dev: the backend and the admin frontend run as separate compose stacks
  (`docker-compose.yml` in each repo).
- Prod: `docker-compose.prod.yml` deploys both as a single stack, pulling
  pre-built images from GHCR (backend on `:8080`, admin frontend on `:5173`),
  together with Redis and RustFS object storage.

## Project organization

Business features live under `internal/`, with customer tickets and staff repair
operations together in `record`, and service dates and work schedules together
in `scheduling`.

```text
main.go                 # small entry point; go run . still works
cmd/benchseed/          # benchmark data generator
internal/
  app/                 # startup, configuration, database setup, routes, shutdown
  auth/                # handler/, service/, model/, client/
  record/              # handler/, service/, model/
  scheduling/          # handler/, service/, model/
  room/                # handler/, service/, model/
  staff/               # handler/, service/, model/
  announcement/        # small module with model, service, and handler files
  tagging/             # service/, model/, client/; embedded catalogs in service/data/
  changes/             # refresh counters, middleware, and Redis storage
  legacy/              # old customer API compatibility
  calendar/            # shared calendar-date rules
  database/            # shared database helpers
  storage/             # S3 client construction and configuration
  httpx/               # shared HTTP parsing and response helpers
  testutil/            # shared test fixtures and request helpers
deploy/rustfs/         # RustFS provisioning and operations guide
```

Tests sit beside their owning packages. Larger modules use subdirectories by
responsibility; when a category grows beyond three production files, move that
category into `handler/`, `service/`, `model/`, or `client/`. See [AGENTS.md](AGENTS.md)
for the file-placement and dependency rules to follow when extending the backend.

## Deploy the backend

```bash
cd clinic-backend
cp .env.example .env   # then fill in the required S3 and RustFS values
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

`docker-compose.prod.yml` is a standalone deploy config — only this file, `.env`,
`deploy/rustfs/` and the packaged image tarball are needed on the server, no
source checkout. It runs the backend, the admin frontend, a bundled Redis
service and RustFS object storage, and connects to the Postgres already running
on the server via `host.containers.internal` (no bundled database), like the
other clinic services. It refuses to start if `CLINIC_API_KEY`, the required
S3 settings, or the RustFS provisioning credentials are missing.

The server cannot pull images from Docker Hub or GHCR, so package the images on
a machine that can and copy the tarball over:

```bash
# On your local machine: create .env from the example and fill in the required
# values (CLINIC_API_KEY, S3_ENDPOINT, S3_BUCKET, S3_REGION, S3_ACCESS_KEY_ID,
# S3_SECRET_ACCESS_KEY, RustFS credentials, ...). The bucket and application
# credentials must exist before the backend starts.
cp .env.example .env
chmod 600 .env
# set CLINIC_DB_DSN in .env to override the default
# (postgres://clinic:clinic@host.containers.internal:5432/clinic?sslmode=disable)

# Load the image tags from .env, then pull and bundle every image used by the
# production compose file.
set -a
source .env
set +a

docker pull ghcr.io/bitnp/clinic-backend:${BACKEND_IMAGE_TAG:-latest}
docker pull ghcr.io/bitnp/clinic_admin_frontend:${ADMIN_FRONTEND_IMAGE_TAG:-latest}
docker pull redis:7-alpine
docker pull rustfs/rustfs:${RUSTFS_IMAGE_TAG:-latest}
docker pull rustfs/rc:${RUSTFS_CLI_IMAGE_TAG:-v0.1.36}

docker save -o clinic-images.tar \
  ghcr.io/bitnp/clinic-backend:${BACKEND_IMAGE_TAG:-latest} \
  ghcr.io/bitnp/clinic_admin_frontend:${ADMIN_FRONTEND_IMAGE_TAG:-latest} \
  redis:7-alpine \
  rustfs/rustfs:${RUSTFS_IMAGE_TAG:-latest} \
  rustfs/rc:${RUSTFS_CLI_IMAGE_TAG:-v0.1.36}

sha256sum clinic-images.tar > clinic-images.tar.sha256

scp clinic-images.tar clinic-images.tar.sha256 docker-compose.prod.yml .env server:
ssh server 'mkdir -p deploy'
scp -r deploy/rustfs server:deploy/

# On the server: load the images and start the stack.
docker load -i clinic-images.tar
docker compose -f docker-compose.prod.yml up -d
```

Do not use `--pull always` or `docker compose pull` for this stack: those bypass
`pull_policy` and would try to fetch images from registries the server cannot
reach. Every bundled service in the production compose file uses
`pull_policy: never` and is served from the loaded tarball.

### Object storage (RustFS)

RustFS stores clinic photos in a private bucket (`clinic-photos` in production,
`clinic-photos-dev` locally). The one-shot `rustfs-init` service creates the
bucket, the `temporary/` and `photos/` prefixes, a bucket-scoped application
policy, and a dedicated application user. The backend never uses the RustFS
root account.

- Internal S3 API: `http://rustfs:9000` on the compose network.
- Host access: `127.0.0.1:${S3_API_PORT}` on the server, for CLI tooling. No
  public S3 proxy is bundled.
- Console: `127.0.0.1:${RUSTFS_CONSOLE_PORT}` only; reach it with
  `ssh -L 9001:127.0.0.1:9001 server`.
- Set `S3_ENDPOINT` to the browser-facing endpoint when one is available.
  Until then, browser uploads are not exposed.
- Configure the admin and customer frontend origins in
  `S3_CORS_ALLOWED_ORIGINS`. CORS does not make the bucket public.

See [deploy/rustfs/README.md](deploy/rustfs/README.md) for bucket setup,
verification, credential rotation, upgrades, and backup/restore procedures.

The frontend (Caddy on `:5173`) proxies `/api`, `/login`, `/logout` to the
backend over the compose network, so the backend needs no host port.

- Postgres: `:5432` on the host (`host.containers.internal`; user/db/password
  all `clinic` by default).
- Redis: `:6379` bundled in the stack (compose-internal, persisted in the
  `redisdata` volume; the backend refuses to start without it). Set
  `REDIS_PASSWORD` to require a password.
- RustFS S3 API: `:9000` on the compose network and `127.0.0.1:${S3_API_PORT}`
  on the host (loopback only).
- RustFS console: `127.0.0.1:9001` (SSH tunnel only).
- RustFS data: `rustfsdata` named volume.
- Admin frontend: `:5173`.
- Backend: `:8080` (compose-internal; expose a host port if you need to reach
  it directly).
- Override any config via `.env` (see `.env.example`) or environment
  variables, e.g. `CLINIC_API_KEY=secret docker compose up --build`.

To pin specific image versions instead of `latest`, set `BACKEND_IMAGE_TAG`,
`ADMIN_FRONTEND_IMAGE_TAG`, `RUSTFS_IMAGE_TAG` and `RUSTFS_CLI_IMAGE_TAG` in
`.env`, and use those same tags in the `docker pull` / `docker save` commands
above.

The database schema is created automatically on startup via AutoMigrate. To
load sample data (rooms, service dates, announcements, staff), run the seed
script once against the database:

```bash
# seed.go is currently sqlite-only, so run it locally against a sqlite DB:
go run fake/seed.go
```

## Object storage (backend usage)

Storage is a required startup dependency. The backend validates the S3
configuration, initializes a `*storage.Service`, and checks that the configured
bucket is accessible before starting background workers or accepting HTTP
requests. It passes the service through application wiring for future
consumers.

### Configuration

| Variable | Purpose |
| --- | --- |
| `S3_ENDPOINT` | Browser-facing endpoint used for presigned URLs. Required. |
| `S3_INTERNAL_ENDPOINT` | Optional backend-only endpoint. Empty means use `S3_ENDPOINT`. |
| `S3_BUCKET` | Photo bucket. Local example: `clinic-photos-dev`; production: `clinic-photos`. Required. |
| `S3_REGION` | Signing region, usually `us-east-1`. Required. |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | Dedicated application credentials. Never use root credentials. Required. |
| `S3_PUT_URL_TTL` | Presigned upload lifetime. Defaults to `5m`. |
| `S3_GET_URL_TTL` | Presigned download lifetime. Defaults to `10m`. |

Local development:

```dotenv
S3_ENDPOINT=http://localhost:9000
S3_INTERNAL_ENDPOINT=
S3_BUCKET=clinic-photos-dev
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
```

`S3_INTERNAL_ENDPOINT` is needed when the backend reaches storage through a
different address than browsers. Leave it empty when `S3_ENDPOINT` works for
both. Compose substitutes `http://rustfs:9000` when it is empty; do not set
`http://127.0.0.1:9000` in `.env` if the backend runs in Compose. A host-run
backend can use `http://127.0.0.1:9000` explicitly if needed, but the default
`http://localhost:9000` endpoint already works for local RustFS. Presigned URLs
always use `S3_ENDPOINT`; `S3_INTERNAL_ENDPOINT` only affects backend
operations.

Production:

```dotenv
S3_ENDPOINT=https://s3.bitnp.net
S3_INTERNAL_ENDPOINT=
S3_BUCKET=clinic-photos
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
```

Configuration is validated at startup. The backend then calls `HeadBucket`
through the backend operations endpoint with a 10-second deadline. Startup
fails with a nonzero exit status if the endpoint is unreachable, the bucket is
missing, access is denied, or the check times out. The deployment restart policy
retries the process when storage becomes available. The application credentials
must allow `s3:ListBucket` for the bucket so `HeadBucket` succeeds; the bundled
RustFS policy includes it.

The bucket and application credentials must be provisioned before starting the
backend. In the bundled Compose stack, `backend` waits for `rustfs-init` to
complete successfully. For an external S3 provider, provision the bucket,
bucket policy, and application credentials first.

### Methods

Examples assume a business service received `storageSvc *storage.Service`
through application wiring.

Upload:

```go
etag, err := storageSvc.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/jpeg")
```

`Put` requires an `io.ReadSeeker`, overwrites an existing key, and returns the
object ETag. Callers are responsible for unique-key generation.

Download:

```go
object, err := storageSvc.Get(ctx, key)
if err != nil {
    return err
}
defer object.Body.Close()

data, err := io.ReadAll(object.Body)
```

The returned body is streamed. Always close `object.Body`.

Inspect metadata without downloading:

```go
metadata, err := storageSvc.Head(ctx, key)
```

Metadata contains `Size`, `ContentType`, and the opaque `ETag`. `Get` and
`Head` return `storage.ErrObjectNotFound` for a missing object:

```go
if errors.Is(err, storage.ErrObjectNotFound) {
    // handle a missing object
}
```

Delete:

```go
err := storageSvc.Delete(ctx, key)
```

Deleting an already-absent object succeeds.

Presign an upload for a browser:

```go
signed, err := storageSvc.PresignPut(ctx, key, "image/jpeg")
// Use signed.Method, signed.URL, and signed.RequiredHeaders.
```

Presign a download:

```go
signed, err := storageSvc.PresignGet(ctx, key)
```

Presigning uses `S3_PUT_URL_TTL` / `S3_GET_URL_TTL`. `SignedRequest` also
contains `ExpiresAt`.

Business code must authorize the caller before signing. Presigning only binds
a bucket, key, and lifetime; it does not authenticate users, verify ownership,
or enforce repair permissions. Never sign an arbitrary user-supplied key.
Derive keys from trusted record/photo identifiers and check access first.

### Storage integration tests

The RustFS tests are opt-in. The bucket must already exist, and the test
credentials must be allowed to use the `temporary/` prefix:

```bash
STORAGE_TEST_ENDPOINT=http://127.0.0.1:9000 \
STORAGE_TEST_BUCKET=clinic-photos-dev \
STORAGE_TEST_REGION=us-east-1 \
STORAGE_TEST_ACCESS_KEY_ID=... \
STORAGE_TEST_SECRET_ACCESS_KEY=... \
go test ./tests/storage -run TestRustFSIntegration -v
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

`scripts/dev.sh` automates the steps below. For a local `S3_ENDPOINT`, it starts
RustFS and runs `rustfs-init` before starting the backend. Use
`--no-storage` when storage is already managed elsewhere or points at an
external S3 endpoint.

```bash
# 1. Start the fake CAS server (gives admin role)
go run fake/fake_cas.go        # runs on :9999

# 2. Start Redis (required; see docker-compose.yml for the dev service)
docker run --rm -p 6379:6379 redis:7-alpine

# 2b. Start RustFS and provision the photo bucket, or point S3_* at an existing
#     S3 provider. The backend will not start until the bucket check succeeds.
#     (set RUSTFS_* and S3_* in .env first)
#     S3 API on http://127.0.0.1:9000, console on http://127.0.0.1:9001
docker compose up -d rustfs rustfs-init

# 3. Start the backend. scripts/dev.sh loads .env, including S3_*.
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
# If not using scripts/dev.sh, export the required S3_* settings from
# "Object storage (backend usage)" before starting.
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

