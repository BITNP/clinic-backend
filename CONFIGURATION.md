# Configuration

All runtime and deployment settings for `clinic-backend` in one place. The
backend reads environment variables; Docker Compose reads them from `.env`
(template: `.env.example`). Defaults below come from the Go code unless a
compose file overrides them.

- API behavior that depends on these settings is documented in [API.md](API.md).
- Deployment procedures are in [README.md](README.md).
- RustFS operations are in [deploy/rustfs/README.md](deploy/rustfs/README.md).

## Backend environment variables

### Core

| Variable | Default | Description |
| --- | --- | --- |
| `CLINIC_API_KEY` | — (required) | Shared secret for API-key client signatures. The backend refuses to start without it. |
| `CLINIC_DB_DRIVER` | `sqlite` | `sqlite` or `postgres`. Any other value stops startup. |
| `CLINIC_DB_DSN` | `clinic.db` | Database connection string. The dev compose default is `postgres://clinic:clinic@db:5432/clinic?sslmode=disable`; the prod default points at `host.containers.internal:5432`. |
| `DB_SLOW_QUERY` | `500ms` | Slow-query log threshold. |
| `CLINIC_TIMEZONE` | `UTC+8` | Timezone for appointment day boundaries and announcement times. |
| `SYNC_POLL_INTERVAL` | `10s` | Recommended client polling interval reported by `/sync/config`; `0s` disables periodic polling. Negative values stop startup. |
| `REDIS_ADDR` | — (required) | Redis for sync counters and the revert journal. The backend refuses to start without a reachable Redis. |
| `REDIS_PASSWORD` | empty | Redis password. In the production compose stack it is also applied to the bundled Redis service. |
| `REDIS_DB` | `0` | Redis database number. |
| `TZ` | unset (prod compose: `Asia/Shanghai`) | Container-local timezone; `CLINIC_TIMEZONE` controls application day boundaries. |

### Records

| Variable | Default | Description |
| --- | --- | --- |
| `RECORD_ACTION_WINDOW` | `5m` | How long a staff action stays revertible. |
| `RECORD_PHOTO_MAX_COUNT` | `9` | Maximum number of ordered photos attached to one record. |
| `RECORD_PHOTO_MAX_SIZE_BYTES` | `10485760` (10 MiB) | Maximum size of one uploaded photo in bytes. |

Both photo limits must be positive integers. Invalid values stop startup with a
nonzero exit status.

### Sessions and authentication

| Variable | Default | Description |
| --- | --- | --- |
| `SESSION_COOKIE_NAME` | `sessionid` | Session cookie name. |
| `CSRF_COOKIE_NAME` | `csrf_token` | CSRF cookie name. |
| `SESSION_COOKIE_SECURE` | `false` | Set `Secure` on session cookies. |
| `SESSION_COOKIE_SAMESITE` | `lax` | `none`, `lax`, or `strict`. |
| `SESSION_TTL` | `336h` | Session lifetime. |
| `STAFF_VERSION` | `0` | Raise to force all staff to log in again. Sessions created with an older version are rejected. |
| `APP_BASE_URL` | empty | Public frontend URL used for CAS return URLs. Required when `CAS_SERVER_URL` is set. |
| `CAS_SERVER_URL` | empty | CAS server. When empty, `/login` returns `503`. |
| `CAS_DEFAULT_REDIRECT` | `/manage/` (code) | Fallback post-login path. `.env.example` and the compose files set `/`. |
| `CAS_HTTP_TIMEOUT` | `10s` | CAS request timeout. |
| `CAS_LOGOUT_RETURN_PARAM` | empty | Query parameter name used by the CAS server for the post-logout return URL. |
| `KEYCLOAK_REALM_URL` / `KEYCLOAK_CLIENT_ID` | empty | Enables Keycloak Bearer authentication when both are set. |

### Tagger agent

The tagger runs in-process with Eino. It keeps `TAGGER_AGENT_POOL_SIZE`
long-lived chat models per tag set, one pool for record tags and one for
liquid-metal tags. Each model owns a session preloaded with the system prompt.
Requests are dispatched to an idle session. A session is reset back to its
system prompt when its context reaches `TAGGER_CONTEXT_TOKENS`; the chat model
is reused. Tagging is disabled while `TAGGER_LLM_API_KEY` is empty.

At startup the backend sends one minimal chat completion to the configured
provider and logs whether the connection succeeded. The check has a 10-second
timeout and does not stop startup when it fails; tagging requests keep failing
until the provider is reachable.

| Variable | Default | Description |
| --- | --- | --- |
| `TAGGER_LLM_API_KEY` | empty | Enables the tagger when non-empty; otherwise new records keep the default `no_pending` tag. |
| `TAGGER_LLM_BASE_URL` | required when enabled | OpenAI-compatible endpoint. |
| `TAGGER_LLM_MODEL` | required when enabled | Model ID. |
| `TAGGER_AGENT_POOL_SIZE` | `4` | Long-lived chat models and sessions per tag set. Minimum `1`. |
| `TAGGER_CONTEXT_TOKENS` | `32000` | Session reset threshold. Must be below the model's context window. |
| `TAGGER_TIMEOUT` | `30s` | Per-call timeout, including waiting for a free session. |

Prompt caching is provider-dependent. The system prompt is a stable prefix for
each session, so providers with prefix caching can reuse it across calls.

### Outbound messaging (message-gateway)

Outbound messaging is handled by the separate `message-gateway` project. The
gateway owns platform adapters (currently NapCat/QQ), per-sender queues, gap
policy, retry behavior, and the HTTP API. The backend does not connect to NapCat
directly.

The preferred deployment is the gateway stack:

```bash
cd message-gateway
docker compose up -d

# Optional sample NapCat stack:
# docker compose -f docker-compose.napcat.yml up -d
```

`clinic-backend/scripts/dev.sh` starts the gateway automatically and exports the
gateway URL to the backend. Account tokens live in
`../message-gateway/accounts.json`. NapCat is external to the gateway stack;
start the sample stack separately if you need it.

| Variable | Default | Description |
| --- | --- | --- |
| `MESSAGE_GATEWAY_URL` | `http://127.0.0.1:8081` for host dev | Gateway HTTP endpoint. Compose uses `http://message-gateway:8081`. |
| `MESSAGE_GATEWAY_TIMEOUT` | `5s` | Per-request client timeout. |

There is no global gateway access key. Each gateway account has its own
`client_token` in `message-gateway/accounts.json`; that token is used as the
bearer token for the account's URL. The account's `access_token` is the private
NapCat token and must not be given to clients. The backend has no business
caller wired to the gateway yet. The first consumer is
`clinic-health-monitor`, which uses its own client token in the production
stack.

The legacy `NAPCAT_*` and `MESSAGE_*` variables belong to the frozen
`internal/message` copy and must not be set in new deployments. In particular,
keep `NAPCAT_BASE_URL` empty so the in-process manager stays disabled.

The gateway queue is in-memory and assumes one gateway instance per sender/QQ
account. Pending messages are lost on gateway restart. See
`message-gateway/README.md` and `message-gateway/API.md`.

### Health monitor (`clinic-health-monitor`)

`clinic-health-monitor` polls the backend's `/healthz` endpoint and the admin
frontend's `/` endpoint, and sends one private QQ alert per outage per target
to the configured maintainers through `message-gateway`. It runs in the
production stack, joins the `message-gateway` external network, and has no host
port.

| Variable | Default | Description |
| --- | --- | --- |
| `HEALTH_MONITOR_IMAGE_TAG` | `latest` | Monitor image tag to deploy. |
| `MAINTAINER_QQ_IDS` | — (required) | Comma-separated QQ user ids that receive alerts. |
| `MONITOR_MESSAGE_GATEWAY_URL` | `http://message-gateway:8081/api/v1/accounts/qq` | Account-scoped OneBot base URL used by the monitor. |
| `MESSAGE_GATEWAY_ACCOUNT_TOKEN` | — (required) | That account's `client_token` from `message-gateway/accounts.json`. |
| `MONITOR_TARGET_URL` | `http://backend:8080/healthz` | Backend health endpoint polled by the monitor. |
| `MONITOR_TARGET_NAME` | `clinic-backend` | Backend target name used in alert text. |
| `MONITOR_FRONTEND_URL` | `http://admin-frontend:80/` in Compose | Optional admin frontend endpoint. Empty disables frontend monitoring. |
| `MONITOR_FRONTEND_NAME` | `clinic-admin-frontend` | Frontend target name used in alert text. |
| `MONITOR_INSTANCE_NAME` | `clinic-prod` | Instance name used in alert text. |
| `MONITOR_INTERVAL` | `30s` | Delay between checks. |
| `MONITOR_TIMEOUT` | `5s` | Per-check HTTP timeout. |
| `MONITOR_FAILURE_THRESHOLD` | `3` | Consecutive failures before an alert. |
| `MONITOR_RECOVERY_THRESHOLD` | `1` | Consecutive successes before recovery. |
| `MONITOR_ALERT_REPEAT_INTERVAL` | `0s` | Repeat interval while down; `0s` disables repeats. |
| `MONITOR_NOTIFY_RECOVERY` | `true` | Send a recovery message after an outage. |

Inside Compose, `MONITOR_MESSAGE_GATEWAY_URL` sets the monitor's
`MESSAGE_GATEWAY_URL`; it also uses `MESSAGE_GATEWAY_ACCOUNT_TOKEN` and
`MESSAGE_GATEWAY_TIMEOUT`. `MAINTAINER_QQ_IDS` and a non-empty
`MESSAGE_GATEWAY_ACCOUNT_TOKEN` are required for the production stack to start.

`/healthz` currently pings Redis only; it does not check Postgres or S3. The
frontend `/` probe verifies Caddy serves `index.html`; it does not execute
JavaScript or check the public ingress path. See
`clinic-health-monitor/README.md` for the alert state machine and limitations.

### Object storage (S3)

Storage is a required startup dependency. The backend validates the S3
configuration, initializes a `*storage.Service`, and checks that the configured
bucket is accessible before starting background workers or accepting HTTP
requests.

| Variable | Purpose |
| --- | --- |
| `S3_ENDPOINT` | Browser-facing endpoint used for presigned URLs. Required. |
| `S3_INTERNAL_ENDPOINT` | Optional backend-only endpoint. Empty means use `S3_ENDPOINT`. |
| `S3_BUCKET` | Photo bucket. Local example: `clinic-photos-dev`; production: `clinic-photos`. Required. |
| `S3_REGION` | Signing region, usually `us-east-1`. Required. |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | Dedicated application credentials. Never use root credentials. Required. |
| `S3_PUT_URL_TTL` | Presigned upload lifetime. Defaults to `5m`; must be positive. |
| `S3_GET_URL_TTL` | Presigned download lifetime. Defaults to `10m`; must be positive. |

Local development:

```dotenv
S3_ENDPOINT=http://localhost:9000
S3_INTERNAL_ENDPOINT=
S3_BUCKET=clinic-photos-dev
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
```

Production:

```dotenv
S3_ENDPOINT=https://s3.bitnp.net
S3_INTERNAL_ENDPOINT=
S3_BUCKET=clinic-photos
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

### Bundled RustFS deployment

RustFS stores clinic photos in a private bucket (`clinic-photos` in production,
`clinic-photos-dev` locally). The one-shot `rustfs-init` service creates the
bucket, the `temporary/` and `photos/` prefixes, a bucket-scoped application
policy, and a dedicated application user. The backend never uses the RustFS
root account.

| Variable | Default | Description |
| --- | --- | --- |
| `RUSTFS_IMAGE_TAG` | `latest` | RustFS image tag; override to match the server image. |
| `RUSTFS_CLI_IMAGE_TAG` | `v0.1.36` | RustFS CLI image used by the one-shot provisioner. |
| `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` | — (required for init) | Root credentials, used only for provisioning and break-glass administration. Never passed to the backend. |
| `RUSTFS_LOG_LEVEL` | `info` | RustFS log level. |
| `S3_API_PORT` | `9000` | Host loopback port for the S3 API. |
| `RUSTFS_S3_BIND_ADDRESS` | `127.0.0.1` | Bind address for the host S3 API port. |
| `RUSTFS_CONSOLE_PORT` | `9001` | Host loopback port for the RustFS console. |
| `RUSTFS_CONSOLE_BIND_ADDRESS` | `127.0.0.1` | Bind address for the console port. |
| `S3_CORS_ALLOWED_ORIGINS` | empty (`.env.example`: `http://localhost:5173,http://localhost:5174`) | Comma-separated frontend origins allowed to use signed upload/download URLs. Set the real HTTPS origins in production; never use `*`. |

- Internal S3 API: `http://rustfs:9000` on the compose network.
- Host access: `127.0.0.1:${S3_API_PORT}` on the server, for CLI tooling. No
  public S3 proxy is bundled.
- Console: `127.0.0.1:${RUSTFS_CONSOLE_PORT}` only; reach it with
  `ssh -L 9001:127.0.0.1:9001 server`.
- Set `S3_ENDPOINT` to the browser-facing endpoint when one is available.
  Until then, browser uploads are not exposed.
- Configure the admin and customer frontend origins in
  `S3_CORS_ALLOWED_ORIGINS`. CORS does not make the bucket public.

Access keys must not contain `/`; uppercase letters and digits are
recommended. Generate credentials with:

```bash
openssl rand -hex 16 | tr '[:lower:]' '[:upper:]'   # access key
openssl rand -hex 24                                 # secret key
```

## Compose and deployment settings

### Dev stack (`docker-compose.yml`)

| Variable | Default | Description |
| --- | --- | --- |
| `POSTGRES_USER` | `clinic` | Bundled Postgres user. |
| `POSTGRES_PASSWORD` | `clinic` | Bundled Postgres password. |
| `POSTGRES_DB` | `clinic` | Bundled Postgres database. |
| `POSTGRES_PORT` | `5432` | Host port for the bundled Postgres. |

The dev stack bundles Postgres, Redis, RustFS, `rustfs-init`, and the backend.
The backend is on `:8080`; the RustFS S3 API and console are loopback-only on
`:9000` and `:9001`. The message gateway is a separate stack in
`../message-gateway`; `scripts/dev.sh` starts it automatically. NapCat is
external to that stack, with an optional sample in
`../message-gateway/docker-compose.napcat.yml`. For local development without
Compose, see [Local development](#local-development).

### Production stack (`docker-compose.prod.yml`)

`docker-compose.prod.yml` is a standalone deploy config: only this file,
`.env`, `deploy/rustfs/` and the packaged image tarball are needed on the
server. It runs the backend, the admin frontend, the health monitor, a bundled
Redis service, and RustFS object storage, and connects to the Postgres already
running on the server via `host.containers.internal` (no bundled database). The
message gateway is deployed separately from `../message-gateway`; NapCat is
external to that stack, with an optional sample in
`../message-gateway/docker-compose.napcat.yml`. The backend and health monitor
join the `message-gateway` external network. It refuses to start if
`CLINIC_API_KEY`, the required S3 settings, the RustFS provisioning
credentials, `MAINTAINER_QQ_IDS`, or `MESSAGE_GATEWAY_ACCOUNT_TOKEN` are
missing.

| Variable | Default | Description |
| --- | --- | --- |
| `BACKEND_IMAGE_TAG` | `latest` | Backend image tag to deploy. |
| `ADMIN_FRONTEND_IMAGE_TAG` | `latest` | Admin frontend image tag to deploy. |
| `HEALTH_MONITOR_IMAGE_TAG` | `latest` | Health monitor image tag to deploy. |
| `ADMIN_FRONTEND_PORT` | `5173` | Host port for the admin frontend. |
| `BACKEND_UPSTREAM` | `http://backend:8080` | Upstream the frontend proxies `/api`, `/login`, and `/logout` to. |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | `clinic` | Used to build the default `CLINIC_DB_DSN` when it is not set. |

Services and ports on the server:

- Postgres: `:5432` on the host (`host.containers.internal`; user/db/password
  all `clinic` by default).
- Redis: `:6379` bundled in the stack (compose-internal, persisted in the
  `redisdata` volume; the backend refuses to start without it). Set
  `REDIS_PASSWORD` to require a password.
- RustFS S3 API: `:9000` on the compose network and `127.0.0.1:${S3_API_PORT}`
  on the host (loopback only).
- RustFS console: `127.0.0.1:9001` (SSH tunnel only).
- RustFS data: `rustfsdata` named volume.
- Message gateway: `message-gateway:8081` on the external `message-gateway`
  network (deployed separately from `../message-gateway`). NapCat is external to
  that stack; the optional sample owns the QQ connection and the NapCat
  WebUI/OneBot ports.
- Health monitor: `health-monitor:8082` on the compose network (no host port;
  liveness only). It polls `http://backend:8080/healthz` and alerts maintainers
  through the gateway.
- Admin frontend: `:5173`.
- Backend: `:8080` (compose-internal; expose a host port if you need to reach
  it directly).

Image versions can be pinned with `BACKEND_IMAGE_TAG`,
`ADMIN_FRONTEND_IMAGE_TAG`, `HEALTH_MONITOR_IMAGE_TAG`, `RUSTFS_IMAGE_TAG`, and
`RUSTFS_CLI_IMAGE_TAG` in `.env`; use the same tags in the `docker pull` /
`docker save` commands.

Do not use `--pull always` or `docker compose pull` for this stack: those bypass
`pull_policy` and would try to fetch images from registries the server cannot
reach. Every bundled service in the production compose file uses
`pull_policy: never` and is served from the loaded tarball.

Override any setting via `.env` or environment variables, for example
`CLINIC_API_KEY=secret docker compose up --build`.

## Admin frontend

The frontend (Caddy on `:5173`) proxies `/api`, `/login`, and `/logout` to the
backend. Configure it with:

| Variable | Default | Description |
| --- | --- | --- |
| `BACKEND_UPSTREAM` | `http://host.docker.internal:8080` (standalone dev) | Backend URL the frontend proxies to. In the production stack it defaults to `http://backend:8080`. |
| `APP_BASE_URL` (backend) | `http://localhost:5173` (compose default) | Frontend address used for CAS login/logout redirects. Set it on the backend to the frontend's public address. |
| `ADMIN_FRONTEND_PORT` | `5173` | Host port exposed by the production stack. |

For CAS login/logout to redirect back to the frontend, set `APP_BASE_URL` on
the backend to the frontend's address. The frontend proxies `/api`, `/login`,
and `/logout` to the backend, so no CORS setup is needed — requests stay
same-origin from the browser's perspective.

## Local development

`scripts/dev.sh` automates the local stack. It loads `.env` (values there
override existing environment variables) and then starts:

1. the fake CAS server on `:9999`;
2. Redis on `:6379` (container, falling back to `redis-server`);
3. RustFS and `rustfs-init` on `:9000` / `:9001` when `S3_ENDPOINT` points at
   local storage;
4. message-gateway, gateway API on `:8081`;
5. the backend on `:8080`.

NapCat is external to the gateway stack. To use the optional sample, start it
after the gateway network exists:

```bash
(cd ../message-gateway && docker compose -f docker-compose.napcat.yml up -d)
```

To test without a real QQ account, use `scripts/dev.sh --fake-napcat`. It starts
the dev fake OneBot stack from
`../message-gateway/docker-compose.fake-napcat.yml` after the gateway network
exists and stops it on exit.

For a local `S3_ENDPOINT`, it starts RustFS and runs `rustfs-init` before
starting the backend. Use `--no-storage` when storage is already managed
elsewhere or points at an external S3 endpoint. On the first sample-NapCat
start, open `http://127.0.0.1:6099/webui` and scan the QR code (the WebUI token
is in `../message-gateway/napcat/config/webui.json`); later starts reuse the
saved session. `scripts/dev.sh` starts the gateway but does not export a
gateway key; account tokens live in `../message-gateway/accounts.json`. Use
`--no-message-gateway` when a gateway is already running; `--no-napcat` is a
deprecated alias. Use `--fake-napcat` to start the dev fake OneBot stack instead
of a real NapCat. Other flags: `--no-cas` and `--no-redis` when those services
are already running.

Defaults applied by `scripts/dev.sh` when the variables are unset:

| Variable | Default |
| --- | --- |
| `CAS_PORT` | `9999` |
| `REDIS_PORT` | `6379` |
| `BACKEND_PORT` | `8080` |
| `REDIS_CONTAINER` | `clinic-dev-redis` |
| `MESSAGE_GATEWAY_DIR` | `../message-gateway` |
| `MESSAGE_GATEWAY_URL` | `http://127.0.0.1:8081` |
| `CLINIC_API_KEY` | `local-dev-key` |
| `REDIS_ADDR` | `127.0.0.1:6379` |
| `REDIS_PASSWORD` | empty |
| `REDIS_DB` | `0` |
| `CAS_SERVER_URL` | `http://127.0.0.1:9999` |
| `APP_BASE_URL` | `http://127.0.0.1:5173` |
| `CAS_DEFAULT_REDIRECT` | `/` |
| `SESSION_COOKIE_SAMESITE` | `lax` |
| `STAFF_VERSION` | `0` |

Manual setup:

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

# 2c. Start message-gateway. NapCat is external; optionally start the sample
#     stack. Its WebUI is on http://127.0.0.1:6099/webui; scan the QR code
#     there once. The sample data lives in ../message-gateway/napcat.
(cd ../message-gateway && docker compose up -d)
(cd ../message-gateway && docker compose -f docker-compose.napcat.yml up -d)  # optional
# Export this if running the backend outside scripts/dev.sh. Account tokens live
# in ../message-gateway/accounts.json.
export MESSAGE_GATEWAY_URL=http://127.0.0.1:8081

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
# "Object storage (S3)" before starting.
go run .                       # runs on :8080

# 4. Start the frontend (separate terminal)
cd path/to/clinic_admin_frontend
pnpm dev                       # runs on :5173
```

Sessions whose staff `version` differs from `STAFF_VERSION` are rejected until
the staff log in again, so each time you raise `STAFF_VERSION` all staff are
forced through the login page once.

## Storage integration tests

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
