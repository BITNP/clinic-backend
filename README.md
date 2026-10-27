# clinic-backend

Backend for the clinic management system, built with Go and Gin.

- Dev: the backend and the admin frontend run as separate compose stacks
  (`docker-compose.yml` in each repo).
- Prod: `docker-compose.prod.yml` deploys both as a single stack, pulling
  pre-built images from GHCR (backend on `:8080`, admin frontend on `:5173`),
  together with Redis and RustFS object storage.

Runtime, deployment, and local-development settings are documented in
[CONFIGURATION.md](CONFIGURATION.md). This README covers the project layout and
deployment workflow.

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
cp .env.example .env   # fill in the required values (see CONFIGURATION.md)
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
# values (CLINIC_API_KEY, S3_*, RustFS credentials, ...). See CONFIGURATION.md
# for the full list. The bucket and application credentials must exist before
# the backend starts.
cp .env.example .env
chmod 600 .env

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

The image tags in the commands above come from `.env`; see
[CONFIGURATION.md](CONFIGURATION.md#production-stack-docker-composeprodyml) for
pinning specific versions.

### Object storage (RustFS)

RustFS stores clinic photos in a private bucket (`clinic-photos` in production,
`clinic-photos-dev` locally). The one-shot `rustfs-init` service creates the
bucket, prefixes, bucket-scoped policy, and dedicated application user; the
backend never uses the RustFS root account.

See [CONFIGURATION.md](CONFIGURATION.md#bundled-rustfs-deployment) for bucket,
endpoint, port, CORS, credential, and image-tag settings. See
[deploy/rustfs/README.md](deploy/rustfs/README.md) for bucket setup,
verification, credential rotation, upgrades, and backup/restore procedures.

The frontend (Caddy on `:5173`) proxies `/api`, `/login`, `/logout` to the
backend over the compose network, so the backend needs no host port. All
service ports and runtime settings are listed in
[CONFIGURATION.md](CONFIGURATION.md#production-stack-docker-composeprodyml).

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
requests. It passes the service through application wiring to the record photo
service.

### Configuration

See [CONFIGURATION.md](CONFIGURATION.md#object-storage-s3) for the S3
environment variables, local and production examples, the startup bucket check,
and RustFS provisioning settings. The [record photo limits](CONFIGURATION.md#records)
are documented there as well.

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

The RustFS tests are opt-in. See
[CONFIGURATION.md](CONFIGURATION.md#storage-integration-tests) for the required
`STORAGE_TEST_*` variables, then run:

```bash
go test ./tests/storage -run TestRustFSIntegration -v
```

## Deploy the frontend

```bash
cd clinic_admin_frontend
cp .env.example .env   # optional, to customize config
docker compose up --build
```

The frontend serves the UI on `:5173` and proxies `/api`, `/login`, `/logout`
to the backend. `BACKEND_UPSTREAM`, `APP_BASE_URL`, and the frontend port
settings are documented in [CONFIGURATION.md](CONFIGURATION.md#admin-frontend).

## Wire them together

For CAS login/logout to redirect back to the frontend, set `APP_BASE_URL` on
the backend to the frontend's address. Because the frontend proxies `/api`,
`/login`, and `/logout`, no CORS setup is needed. See
[CONFIGURATION.md](CONFIGURATION.md#admin-frontend) for the details.

## Run locally (development)

`scripts/dev.sh` loads `.env` and starts the fake CAS server, Redis, local
RustFS (when `S3_ENDPOINT` points at it), and the backend:

```bash
scripts/dev.sh
```

Use `--no-cas`, `--no-redis`, or `--no-storage` when a service is already
running or storage is managed elsewhere. The complete local-development
settings, defaults, and manual steps are in
[CONFIGURATION.md](CONFIGURATION.md#local-development).

Start the frontend separately:

```bash
cd path/to/clinic_admin_frontend
pnpm dev                       # runs on :5173
```

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

