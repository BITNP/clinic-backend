# clinic-backend

Backend for the clinic management system, built with Go and Gin.

- Dev: the backend and the admin frontend run as separate compose stacks
  (`docker-compose.yml` in each repo).
- Prod: `docker-compose.prod.yml` deploys the backend, admin frontend, health
  monitor, Redis, and RustFS as a single stack, pulling pre-built images from
  GHCR (backend on `:8080`, admin frontend on `:5173`).

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
  agent/               # Eino chat models, sessions, and the tagger agent
  app/                 # startup, configuration, database setup, routes, shutdown
  auth/                # handler/, service/, model/, client/
  record/              # handler/, service/, model/
  scheduling/          # handler/, service/, model/
  room/                # handler/, service/, model/
  staff/               # handler/, service/, model/
  announcement/        # small module with model, service, and handler files
  tagging/             # service/, model/; embedded catalogs in service/data/
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
source checkout. It runs the backend, the admin frontend, the health monitor, a
bundled Redis service and RustFS object storage, and connects to the Postgres
already running on the server via `host.containers.internal` (no bundled
database), like the other clinic services. It refuses to start if
`CLINIC_API_KEY`, `MAINTAINER_QQ_IDS`, `MESSAGE_GATEWAY_API_KEY`, the required
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
docker pull ghcr.io/bitnp/clinic-health-monitor:${HEALTH_MONITOR_IMAGE_TAG:-latest}
docker pull redis:7-alpine
docker pull rustfs/rustfs:${RUSTFS_IMAGE_TAG:-latest}
docker pull rustfs/rc:${RUSTFS_CLI_IMAGE_TAG:-v0.1.36}
docker pull mlikiowa/napcat-docker:latest
docker pull ghcr.io/bitnp/message-gateway:${MESSAGE_GATEWAY_IMAGE_TAG:-latest}

docker save -o clinic-images.tar \
  ghcr.io/bitnp/clinic-backend:${BACKEND_IMAGE_TAG:-latest} \
  ghcr.io/bitnp/clinic_admin_frontend:${ADMIN_FRONTEND_IMAGE_TAG:-latest} \
  ghcr.io/bitnp/clinic-health-monitor:${HEALTH_MONITOR_IMAGE_TAG:-latest} \
  redis:7-alpine \
  rustfs/rustfs:${RUSTFS_IMAGE_TAG:-latest} \
  rustfs/rc:${RUSTFS_CLI_IMAGE_TAG:-v0.1.36} \
  mlikiowa/napcat-docker:latest \
  ghcr.io/bitnp/message-gateway:${MESSAGE_GATEWAY_IMAGE_TAG:-latest}

sha256sum clinic-images.tar > clinic-images.tar.sha256

scp clinic-images.tar clinic-images.tar.sha256 docker-compose.prod.yml .env server:
ssh server 'mkdir -p deploy message-gateway'
scp -r deploy/rustfs server:deploy/
scp ../message-gateway/docker-compose.yml \
  ../message-gateway/docker-compose.napcat.yml \
  ../message-gateway/.env server:message-gateway/

# On the server: load the images and start the stacks.
docker load -i clinic-images.tar
cd message-gateway && docker compose up -d --no-build
# Optional sample NapCat stack:
# docker compose -f docker-compose.napcat.yml up -d --no-build
cd .. && docker compose -f docker-compose.prod.yml up -d
```

Before starting, update `message-gateway/.env` for the server: set
`MESSAGE_GATEWAY_API_KEY` and `NAPCAT_BASE_URL`. If you use the sample NapCat
stack, also set `NAPCAT_QUICK_ACCOUNT` and the NapCat data paths (relative
`./napcat/config` and `./napcat/QQ` under the copied `message-gateway`
directory work best). The backend `.env` must contain the same
`MESSAGE_GATEWAY_API_KEY` and `MESSAGE_GATEWAY_URL=http://message-gateway:8081`.
Also set `MAINTAINER_QQ_IDS` there; the health monitor uses those ids for
private QQ alerts and requires a non-empty gateway key.

Do not use `--pull always` or `docker compose pull` for this stack: those bypass
`pull_policy` and would try to fetch images from registries the server cannot
reach. Every bundled service in the production compose file uses
`pull_policy: never` and is served from the loaded tarball.

NapCat is external to the gateway stack, not part of this backend. Deploy the
gateway from `../message-gateway`:

```bash
cd ../message-gateway
docker compose up -d

# Optional sample NapCat stack:
# docker compose -f docker-compose.napcat.yml up -d
```

If you use the sample NapCat stack, it persists its config and QQ login state in
`../message-gateway/napcat/config` and `../message-gateway/napcat/QQ`. After the
first start, open the WebUI through an SSH tunnel and scan the QR code once:

```bash
ssh -L 6099:127.0.0.1:6099 server
# then open http://127.0.0.1:6099/webui
# WebUI token: ../message-gateway/napcat/config/webui.json
```

Later restarts reuse the saved session; set `NAPCAT_QUICK_ACCOUNT` in the
gateway `.env` to auto-login without scanning again. If you use an external
NapCat instead of the sample stack, set `NAPCAT_BASE_URL` to its OneBot
endpoint.

The backend calls the gateway over HTTP. Set `MESSAGE_GATEWAY_URL` and
`MESSAGE_GATEWAY_API_KEY` in the backend `.env` (the default URL is
`http://message-gateway:8081` inside Compose). Do not set `NAPCAT_BASE_URL`;
the legacy in-process manager must remain disabled.

The image tags in the commands above come from `.env`; see
[CONFIGURATION.md](CONFIGURATION.md#production-stack-docker-composeprodyml) for
pinning specific versions, including `HEALTH_MONITOR_IMAGE_TAG`.

The health monitor polls the backend's `/healthz` endpoint and the admin
frontend's `/` endpoint, and sends one private QQ message per outage per target
to each id in `MAINTAINER_QQ_IDS`. Its `MONITOR_*` settings and limitations are
documented in
[CONFIGURATION.md](CONFIGURATION.md#health-monitor-clinic-health-monitor) and
`clinic-health-monitor/README.md`.

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
RustFS (when `S3_ENDPOINT` points at it), `message-gateway`, and the backend.
NapCat is external to the gateway stack; its optional sample stack is in
`../message-gateway/docker-compose.napcat.yml`.

```bash
scripts/dev.sh
```

Use `--no-cas`, `--no-redis`, `--no-storage`, or `--no-message-gateway` when a
service is already running or storage is managed elsewhere. `--no-napcat` is a
deprecated alias for `--no-message-gateway`. For testing without a QQ login, add
`--fake-napcat`; it starts the dev fake OneBot server directly from
`../message-gateway`. The complete
local-development settings, defaults, and manual steps are in
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


### Pending ticket reminders

The backend can remind admins assigned to a service window when tickets remain
pending. It scans every minute and on startup, finds pending tickets by date and
room, and sends a QQ private message through the account-scoped message gateway.
Set the following backend environment variables to enable it:

- `SERVICE_DATE_REMINDER_ENABLED=true` (default `false`).
- `SERVICE_DATE_REMINDER_LEAD_TIMES=5h,3h` (default shown): unique, positive
  Go durations in whole seconds. Each is relative to the service start time.
- `MESSAGE_GATEWAY_URL`: gateway base URL.
- `MESSAGE_GATEWAY_ACCOUNT=qq`: gateway account name (default `qq`).
- `MESSAGE_GATEWAY_ACCOUNT_TOKEN`: the account's gateway `client_token`.
- `MESSAGE_GATEWAY_TIMEOUT=5s` (default shown): request timeout.
- `SERVICE_DATE_REMINDER_TEMPLATE`: optional Go text template with `Date`,
  `StartTime`, `Room`, `PendingCount`, and `LeadTime` fields.

`LeadTime` is the actual time remaining when the message is sent, rounded down
to the minute.

Dates and message times use `CLINIC_TIMEZONE` (default UTC+8). Service start
timestamps are interpreted as UTC instants, as sent by the admin frontend.
PostgreSQL stores these values without a timezone; existing timestamps written
with another offset cannot be distinguished automatically. Only admins
assigned to an enabled matching work schedule are notified. Admins need a QQ
number. A short overlapping scan window tolerates timer drift; long downtime
is not caught up. Jobs are stored in the backend database and failed submissions
are retried with backoff up to eight attempts, stopping before the service starts.
A sent job means that the gateway accepted it into its in-memory queue, not that
the recipient received it. An uncertain gateway response or a crash before the
job is marked sent can cause a duplicate, since the gateway has no idempotency key.
