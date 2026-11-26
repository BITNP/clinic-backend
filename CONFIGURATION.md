# Configuration

All runtime and deployment settings for `clinic-backend` in one place. Most
deployment settings are environment variables; Docker Compose reads them from
`.env` (template: `.env.example`). Database-managed config items receive their
initial values from `runtime_settings.json`, which must sit beside `.env`.
Defaults below come from Go code, that external JSON file, or a compose file as
noted.

- API behavior that depends on these settings is documented in [API.md](API.md).
- Deployment procedures are in [README.md](README.md).
- RustFS operations are in [deploy/rustfs/README.md](deploy/rustfs/README.md).

## Database-managed runtime settings

The backend embeds `internal/config/service/data/template.json`, which defines
the reserved groups and item metadata. Message-gateway items also carry
built-in fallback values. At every startup the backend reads
`runtime_settings.json` from its working directory, beside `.env`, validates
the reserved items, and uses those values only when creating missing database
rows. Existing values are preserved, while reserved display names, types,
groups, ordering, required flags, write-only flags, and role levels are
synchronized from the template. The database becomes authoritative for values
immediately after initialization.

`runtime_settings.json` is an external deployment file and is gitignored. Start
from [`runtime_settings.example.json`](runtime_settings.example.json), keep the
real file at the same filesystem level as `.env`, and mount it read-only at
`/app/runtime_settings.json` in containers. Missing files, unknown items,
missing items without a built-in default, and invalid values stop startup with
an error.

```json
{
  "SYNC_POLL_INTERVAL": "10s",
  "STAFF_VERSION": "0",
  "TAGGER_LLM_API_KEY": "",
  "TAGGER_LLM_BASE_URL": "",
  "TAGGER_LLM_MODEL": "",
  "TAGGER_AGENT_POOL_SIZE": "4",
  "TAGGER_CONTEXT_TOKENS": "32000",
  "TAGGER_TIMEOUT": "30s",
  "RECORD_ACTION_WINDOW": "5m",
  "RECORD_PHOTO_MAX_COUNT": "9",
  "RECORD_PHOTO_MAX_SIZE_BYTES": "10485760",
  "SERVICE_DATE_REMINDER_ENABLED": "false",
  "SERVICE_DATE_REMINDER_LEAD_TIMES": "5h,3h",
  "SERVICE_DATE_REMINDER_TEMPLATE": "[电脑诊所]{{.Date}} {{.StartTime}} {{.Room}} 的值班还有{{.PendingCount}}个待审核工单，请及时处理。",
  "MESSAGE_GATEWAY_URL": "http://127.0.0.1:8081",
  "MESSAGE_GATEWAY_ACCOUNT": "qq",
  "MESSAGE_GATEWAY_ACCOUNT_TOKEN": "",
  "MESSAGE_GATEWAY_TIMEOUT": "5s"
}
```

During the first upgrade that introduces an item, a non-empty legacy
environment value with the same name may initialize that missing row. Once the
row exists, the database is authoritative and the environment value is ignored.
Changes made through `/api/admin/config` apply after a successful save.
Operations already in progress may finish with their previous settings. Browser
tabs refresh their sync interval periodically.

| Config item | Default | Description |
| --- | --- | --- |
| `SYNC_POLL_INTERVAL` | `10s` | Recommended client polling interval reported by the public `/api/sync/config` and `/api/admin/sync/config` endpoints; `0s` disables periodic polling. |
| `STAFF_VERSION` | `0` | Raise to force all staff to log in again. Non-negative integer. |
| `RECORD_ACTION_WINDOW` | `5m` | Positive Go duration for how long a staff action stays revertible. |
| `RECORD_PHOTO_MAX_COUNT` | `9` | Positive maximum number of ordered photos attached to one record. |
| `RECORD_PHOTO_MAX_SIZE_BYTES` | `10485760` | Positive maximum size of one uploaded photo in bytes. |
| `SERVICE_DATE_REMINDER_ENABLED` | `false` | Strict boolean controlling the pending-ticket reminder worker. |
| `SERVICE_DATE_REMINDER_LEAD_TIMES` | `5h,3h` | Comma-separated unique positive Go durations using whole seconds, relative to service start. |
| `SERVICE_DATE_REMINDER_TEMPLATE` | built-in Chinese reminder | Non-empty Go text template supporting `Date`, `StartTime`, `Room`, `PendingCount`, and `LeadTime`. |
| `MESSAGE_GATEWAY_URL` | empty for legacy files; local example uses `http://127.0.0.1:8081` | HTTP(S) message-gateway base URL without query or fragment. Required while reminders are enabled. |
| `MESSAGE_GATEWAY_ACCOUNT` | `qq` | Account segment used for pending-ticket reminder delivery. |
| `MESSAGE_GATEWAY_ACCOUNT_TOKEN` | empty | Optional write-only gateway `client_token`; never returned by the API. Required while reminders are enabled and must match the selected account. |
| `MESSAGE_GATEWAY_TIMEOUT` | `5s` | Positive Go duration used as one reminder gateway request timeout. |
| `TAGGER_LLM_API_KEY` | empty | Optional write-only key for the in-process tagger. Its value is never returned by the API. |
| `TAGGER_LLM_BASE_URL` | empty | OpenAI-compatible endpoint; required when the API key is non-empty. |
| `TAGGER_LLM_MODEL` | empty | Model ID; required when the API key is non-empty. |
| `TAGGER_AGENT_POOL_SIZE` | `4` | Long-lived models and sessions per tag set. Minimum `1`. |
| `TAGGER_CONTEXT_TOKENS` | `32000` | Session reset threshold. Must be positive. |
| `TAGGER_TIMEOUT` | `30s` | Positive per-call and pool-wait timeout. |

These names are reserved: their `value_type`, required flag, write-only flag,
role level, and value validation are enforced by the config service. The tagger
settings `TAGGER_LLM_API_KEY`, `TAGGER_LLM_BASE_URL`, `TAGGER_LLM_MODEL`,
`TAGGER_AGENT_POOL_SIZE`, `TAGGER_CONTEXT_TOKENS`, and `TAGGER_TIMEOUT`, plus
the gateway settings `MESSAGE_GATEWAY_URL`, `MESSAGE_GATEWAY_ACCOUNT`,
`MESSAGE_GATEWAY_ACCOUNT_TOKEN`, and `MESSAGE_GATEWAY_TIMEOUT`, are accessible
only to `superadmin`. The remaining reserved settings are accessible to
`admin`. They are seeded into the `system`, `record`, and `reminder` groups.
`MAINTAINER_QQ_IDS` is not part of this backend catalog; it remains a
`clinic-health-monitor` environment setting.

## Backend environment variables

### Core

| Variable | Default | Description |
| --- | --- | --- |
| `CLINIC_API_KEY` | — (required) | Shared secret for API-key client signatures. The backend refuses to start without it. |
| `CLINIC_DB_DRIVER` | `sqlite` | `sqlite` or `postgres`. Any other value stops startup. |
| `CLINIC_DB_DSN` | `clinic.db` | Database connection string. The dev compose default is `postgres://clinic:clinic@db:5432/clinic?sslmode=disable`; the prod default points at `host.containers.internal:5432`. |
| `DB_SLOW_QUERY` | `500ms` | Slow-query log threshold. |
| `CLINIC_TIMEZONE` | `UTC+8` | Timezone for appointment day boundaries and announcement times. |
| `REDIS_ADDR` | — (required) | Redis for sync counters and the revert journal. The backend refuses to start without a reachable Redis. |
| `REDIS_PASSWORD` | empty | Redis password. In the production compose stack it is also applied to the bundled Redis service. |
| `REDIS_DB` | `0` | Redis database number. |
| `TZ` | unset (prod compose: `Asia/Shanghai`) | Container-local timezone; `CLINIC_TIMEZONE` controls application day boundaries. |

### Records

The photo count and per-file byte limit are database-managed
`RECORD_PHOTO_MAX_COUNT` and `RECORD_PHOTO_MAX_SIZE_BYTES` settings above.
Both limits apply to the next request immediately after a successful save.

### Sessions and authentication

| Variable | Default | Description |
| --- | --- | --- |
| `SESSION_COOKIE_NAME` | `sessionid` | Session cookie name. |
| `CSRF_COOKIE_NAME` | `csrf_token` | CSRF cookie name. |
| `SESSION_COOKIE_SECURE` | `false` | Set `Secure` on session cookies. |
| `SESSION_COOKIE_SAMESITE` | `lax` | `none`, `lax`, or `strict`. |
| `SESSION_TTL` | `336h` | Session lifetime. |
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
is reused. Tagging is disabled while the database-managed
`TAGGER_LLM_API_KEY` config item is empty.

The API key is a write-only config item, so the admin API never returns its
stored value. All tagger settings shown above are database-managed and can be
changed through the system settings page. A successful save builds a complete
candidate tagger generation before the database commit and atomically swaps it
in afterwards. In-flight calls finish on the previous generation. Pool-size
increases start new work immediately; decreases let excess in-flight calls
finish while preventing additional old-limit concurrency.

Changing the API key, endpoint, model, timeout, or context limit rebuilds both
tagger pools without restarting the backend. The backend does not verify
provider connectivity during save, so an incorrectly secured key can still
produce request-time provider errors.

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

`clinic-backend/scripts/dev.sh` starts the gateway automatically. Account tokens
live in `../message-gateway/accounts.json`. NapCat is external to the gateway
stack; start the sample stack separately if you need it.

The reminder connection settings are database config items. The same-named
environment variables are compatibility inputs only: they can seed a missing
row on the first upgraded startup, but later environment changes are ignored.

| Seed variable | Description |
| --- | --- |
| `MESSAGE_GATEWAY_URL` | Gateway base URL. Compose and production commonly use `http://message-gateway:8081`. |
| `MESSAGE_GATEWAY_ACCOUNT` | Gateway account segment, normally `qq`. |
| `MESSAGE_GATEWAY_ACCOUNT_TOKEN` | The selected account's gateway `client_token`. |
| `MESSAGE_GATEWAY_TIMEOUT` | Positive request timeout such as `5s`. |
| `SERVICE_DATE_REMINDER_TEMPLATE` | Optional initial message template with the supported text-template fields. |

There is no global gateway access key. Each gateway account has its own
`client_token` in `message-gateway/accounts.json`; that token is used as the
bearer token for the account's URL. The account's `access_token` is the private
NapCat token and must not be given to clients. Pending-ticket reminders use the
selected database account. `clinic-health-monitor` uses its own client token in
the production stack.

The legacy `NAPCAT_*` and `MESSAGE_*` variables belong to the frozen
`internal/message` copy and must not be set in new deployments. In particular,
keep `NAPCAT_BASE_URL` empty so the in-process manager stays disabled.

The reminder URL, account, write-only account token, timeout, lead times, and
message template are all resolved from the current database snapshot before
each delivery. A successful settings save affects the next send without a
restart. If reminders are enabled, the settings API rejects an empty URL or
token and invalid URL, duration, account, or template values.

The gateway queue is in-memory and assumes one gateway instance per sender/QQ
account. Pending messages are lost on gateway restart. See
`message-gateway/README.md` and `message-gateway/API.md`.

### DingTalk message center

DingTalk notifications use the V2 message-center API through the reusable
`internal/notification/client` package. This path is independent of the QQ
`message-gateway` path. The backend uses it to notify the record's `user`
DingTalk id when a ticket becomes `confirmed`, `rejected`, `referred`, or
`no_show`. The settings are seeded as reserved config items in the
`dingtalk` group. Environment variables with the same names can initialize a
missing item during the first upgrade, but the database value is authoritative
afterward.

| Config item | Default | Access | Description |
| --- | --- | --- | --- |
| `DINGTALK_NOTIFY_ENABLED` | `false` | superadmin | Enables status notifications and reminder discovery. |
| `DINGTALK_BASE_URL` | empty | superadmin | Message-center business base URL; required when notifications are enabled. |
| `DINGTALK_APP_CODE` | empty | superadmin | Authorized message-center application code. |
| `DINGTALK_APP_KEY` | empty | superadmin | Open-platform application key. |
| `DINGTALK_APP_SECRET` | empty | superadmin | Open-platform application secret. Write-only; never returned by the API. |
| `DINGTALK_TIMEOUT` | `10s` | superadmin | Per-request HTTP timeout. |
| `DINGTALK_REDIRECT_ENABLED` | `false` | superadmin | Replaces every message's recipients with `DINGTALK_REDIRECT_RECEIVER`. |
| `DINGTALK_REDIRECT_RECEIVER` | empty | superadmin | The single DingTalk account id used while redirection is enabled. |
| `DINGTALK_REMINDER_LEAD_TIME` | `30m` | admin | How long before the service starts a confirmed ticket becomes eligible for a reminder. |
| `DINGTALK_REMINDER_POLL_INTERVAL` | `1m` | superadmin | Worker polling interval for reminder discovery and delivery. |
| `DINGTALK_NOTIFICATION_TITLE` | `电脑诊所工单状态更新` | admin | DingTalk notification title. |
| `DINGTALK_NOTIFICATION_HEADER` | `网络开拓者协会` | admin | Value rendered as `{{.HEDR}}`. |
| `DINGTALK_NOTIFICATION_QQ_ACCOUNT` | empty | admin | Appeal QQ rendered as `{{.QQAC}}`. |
| `DINGTALK_TEMPLATE_CONFIRMED` | built-in approval example | admin | Template for `confirmed`. |
| `DINGTALK_TEMPLATE_REJECTED` | built-in rejection example | admin | Template for `rejected`. |
| `DINGTALK_TEMPLATE_REFERRED` | built-in referral example | admin | Template for `referred`. |
| `DINGTALK_TEMPLATE_TRANSFER` | built-in transfer example | admin | Template sent when confirmation changes the room. The DingTalk title is `工单转诊`. |
| `DINGTALK_TEMPLATE_NO_SHOW` | built-in no-show example | admin | Template for `no_show`. |
| `DINGTALK_TEMPLATE_REMINDER` | built-in reminder example | admin | Confirmed-ticket pre-shift reminder. |

Enabling notifications requires a valid base URL, app code, app key, and app
secret. Enabling redirection additionally requires exactly one receiver.
Invalid or incomplete candidates are rejected before the active configuration
changes. Message-center and redirect changes apply to the next status message
without restarting the backend.

While `DINGTALK_NOTIFY_ENABLED=false`, status messages are skipped and the
reminder worker does not discover or send reminders. Persisted pending
reminders remain queued and resume after notifications are re-enabled.
Reminder timing and polling changes apply at the worker's next scheduling
cycle.

Available template fields:

| Field | Source |
| --- | --- |
| `HEDR` | `DINGTALK_NOTIFICATION_HEADER` |
| `DATE` | Appointment date and service window, formatted `YYYY-MM-DD的HH:MM-HH:MM`. |
| `ID` | Record id prefixed with `#`. |
| `ROOM` | Booked room name. |
| `LOCA` | Booked room address. |
| `TRRO` | Confirmed room name selected by the acting staff member in `调整场地`. |
| `TRLO` | Confirmed room address resolved from the selected room. |
| `REAS` | Rejection or referral reason. |
| `QQAC` | `DINGTALK_NOTIFICATION_QQ_ACCOUNT`. |

Template example:

```env
DINGTALK_NOTIFICATION_QQ_ACCOUNT=123456789
DINGTALK_TEMPLATE_CONFIRMED=【{{.HEDR}}】工单{{.ID}}已通过，请于{{.DATE}}前往{{.ROOM}}。\n地址：{{.LOCA}}
DINGTALK_TEMPLATE_TRANSFER=【{{.HEDR}}】您于{{.DATE}}的预约被转送至{{.TRRO}}，请在预约时间前往{{.TRRO}}。诊室地址为{{.TRLO}}。\n感谢您对网协工作的理解！
```

When notifications are enabled, the backend scans at
`DINGTALK_REMINDER_POLL_INTERVAL` for confirmed tickets whose service window is
at most `DINGTALK_REMINDER_LEAD_TIME` away and has not started. It persists one
reminder job per record and service window, rechecks eligibility before sending,
and retries transient failures with backoff. Reminder jobs are stored in
`clinic_record_dingtalk_reminder`, so they survive backend restarts.

The client obtains an open-platform token with the configured key and secret,
caches it, and requests a replacement on the first DingTalk operation at or
after 80% of `expires_in`. The V2 API reports `expires_in` in minutes. Calls use
`open-access-token` and `msg-center-app-code`; they return non-200 API `code`
values as errors. After submission, the client queries the message status and
requires a successful `dingTalk` detail with at least one successful recipient
and no failed or unauthorized recipients. Notification delivery is best-effort:
the ticket state remains committed if sending or status verification fails, and
the failure is logged.

For local development, `scripts/dev.sh` starts `fake/fake_dingtalk.go` on
`:9998` by default. Its environment defaults can seed a fresh database, while
an existing installation follows the database-managed DingTalk items. Use
`--no-dingtalk` to skip the fake. To run it manually:

```bash
go run fake/fake_dingtalk.go
export DINGTALK_BASE_URL=http://127.0.0.1:9998
export DINGTALK_APP_CODE=local-dev-app
export DINGTALK_APP_KEY=local-dev-key
export DINGTALK_APP_SECRET=local-dev-secret
```

The fake implements token, send, and delivery-status requests. It prints each
received method, path, query, headers, and formatted JSON body to stdout, while
redacting application secrets and access tokens. Sent messages are kept in
memory so their status can be queried until the process exits.

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
| `MULTIMETER_S3_BUCKET` | Bucket containing multimeter executables. Defaults to `clinic-executable`; objects are read from `multimeter/latest/`. |
| `MULTIMETER_S3_ACCESS_KEY_ID` / `MULTIMETER_S3_SECRET_ACCESS_KEY` | Optional read credentials for the executable bucket. When empty, `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` are used. |

Local development:

```dotenv
S3_ENDPOINT=http://localhost:9000
S3_INTERNAL_ENDPOINT=
S3_BUCKET=clinic-photos-dev
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=...
S3_SECRET_ACCESS_KEY=...
MULTIMETER_S3_BUCKET=clinic-executable
MULTIMETER_S3_ACCESS_KEY_ID=...
MULTIMETER_S3_SECRET_ACCESS_KEY=...
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

The public `GET /api/downloads/stress-test-helper/download` endpoint signs
`GetObject` URLs for `s3://clinic-executable/multimeter/latest/`. The configured
executable credentials must allow `s3:GetObject` for that prefix. Dedicated
`MULTIMETER_S3_*` credentials can be used when the photo application user is
scoped only to `clinic-photos`; otherwise the normal `S3_*` credentials are
reused. The executable bucket remains private, and returned URLs expire using
`S3_GET_URL_TTL`.

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

The bundled database uses PostgreSQL 18 and the `pgdata18` volume so its dump
format matches production. A PostgreSQL 16 `pgdata` volume from an older local
stack is not mounted automatically.

### Production stack (`docker-compose.prod.yml`)

`docker-compose.prod.yml` is a standalone deploy config: only this file,
`.env`, `runtime_settings.json`, `deploy/rustfs/` and the packaged image tarball
are needed on the server. It runs the backend, the admin frontend, the health
monitor, a bundled Redis service, and RustFS object storage, and connects to the
Postgres already running on the server via `host.containers.internal` (no
bundled database). The message gateway is deployed separately from
`../message-gateway`; NapCat is external to that stack, with an optional sample in
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

Override any environment-backed setting via `.env` or environment variables,
for example `CLINIC_API_KEY=secret docker compose up --build`. Database-managed
settings are changed through the admin configuration API.

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

`scripts/dev.sh` automates the local stack. It creates `runtime_settings.json`
from the example when missing, loads `.env` (values there override existing
environment variables), and then starts:

1. bundled PostgreSQL on `:5432` and configures the host-run backend to use it;
2. the fake CAS server on `:9999`;
3. the fake DingTalk message center on `:9998`;
4. Redis on `:6379` (container, falling back to `redis-server`);
5. RustFS and `rustfs-init` on `:9000` / `:9001` when `S3_ENDPOINT` points at
   local storage;
6. message-gateway, gateway API on `:8081`;
7. the backend on `:8080`.

The fake DingTalk writes received requests directly to the terminal. If
`DINGTALK_BASE_URL` points at another host, `dev.sh` uses that service instead
of starting the fake. Because DingTalk runtime settings are database-managed,
an existing installation may still target another message center. Use
`--no-dingtalk` when the local fake is not needed.

NapCat is external to the gateway stack. To use the optional sample, start it
after the gateway network exists:

```bash
(cd ../message-gateway && docker compose -f docker-compose.napcat.yml up -d)
```

To test without a real QQ account, use `scripts/dev.sh --fake-napcat`. It starts
the dev fake OneBot server with `go run ./cmd/fake-napcat` from
`../message-gateway` and stops it on exit.

Use `scripts/dev.sh --role staff`, `--role admin`, or `--role superadmin` to
select the fake CAS account presented on login:

| Role | Account | Claims |
| --- | --- | --- |
| `staff` | `manual-staff` | `/clinic` |
| `admin` | `manual-admin` | `/clinic`, `/management` |
| `superadmin` | `manual-superadmin` | `clinic-maintainer` realm, `/clinic` |

The default is `admin`. Restart `scripts/dev.sh` with another role, then log out
and log in again through CAS to use the corresponding account and role.

For a local `S3_ENDPOINT`, it starts RustFS and runs `rustfs-init` before
starting the backend. Use `--no-storage` when storage is already managed
elsewhere or points at an external S3 endpoint. On the first sample-NapCat
start, open `http://127.0.0.1:6099/webui` and scan the QR code (the WebUI token
is in `../message-gateway/napcat/config/webui.json`); later starts reuse the
saved session. `scripts/dev.sh` starts the gateway but does not export a
gateway key; account tokens live in `../message-gateway/accounts.json`. Use
`--no-message-gateway` when a gateway is already running; `--no-napcat` is a
deprecated alias. Use `--fake-napcat` to start the dev fake OneBot server instead
of a real NapCat. Use `--no-postgres` when using an external PostgreSQL database
or SQLite, and `--no-cas` / `--no-redis` when those services are already
running.

Defaults applied by `scripts/dev.sh` when the variables are unset:

| Variable | Default |
| --- | --- |
| `CAS_PORT` | `9999` |
| `CAS_ROLE` | `admin` |
| `POSTGRES_PORT` | `5432` |
| `POSTGRES_USER` | `clinic` |
| `POSTGRES_PASSWORD` | `clinic` |
| `POSTGRES_DB` | `clinic` |
| `DINGTALK_PORT` | `9998` |
| `REDIS_PORT` | `6379` |
| `BACKEND_PORT` | `8080` |
| `REDIS_CONTAINER` | `clinic-dev-redis` |
| `MESSAGE_GATEWAY_DIR` | `../message-gateway` |
| `MESSAGE_GATEWAY_URL` | `http://127.0.0.1:8081` |
| `CLINIC_DB_DRIVER` | `postgres` |
| `CLINIC_DB_DSN` | `postgres://clinic:clinic@127.0.0.1:5432/clinic?sslmode=disable` |
| `CLINIC_API_KEY` | `local-dev-key` |
| `REDIS_ADDR` | `127.0.0.1:6379` |
| `REDIS_PASSWORD` | empty |
| `REDIS_DB` | `0` |
| `CAS_SERVER_URL` | `http://127.0.0.1:9999` |
| `DINGTALK_BASE_URL` | `http://127.0.0.1:9998` |
| `DINGTALK_APP_CODE` | `local-dev-app` |
| `DINGTALK_APP_KEY` | `local-dev-key` |
| `DINGTALK_APP_SECRET` | `local-dev-secret` |
| `APP_BASE_URL` | `http://127.0.0.1:5173` |
| `CAS_DEFAULT_REDIRECT` | `/` |
| `SESSION_COOKIE_SAMESITE` | `lax` |

Manual setup:

```bash
# 1. Start the fake CAS server (defaults to the manual-admin account)
go run fake/fake_cas.go --role staff        # use admin or superadmin as needed

# 1b. Start the fake DingTalk message center and point the backend at it
go run fake/fake_dingtalk.go   # runs on :9998
export DINGTALK_BASE_URL=http://127.0.0.1:9998
export DINGTALK_APP_CODE=local-dev-app
export DINGTALK_APP_KEY=local-dev-key
export DINGTALK_APP_SECRET=local-dev-secret

# 2. Start PostgreSQL and Redis (required; see docker-compose.yml)
docker compose up -d db
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
export CLINIC_DB_DRIVER=postgres
export CLINIC_DB_DSN='postgres://clinic:clinic@127.0.0.1:5432/clinic?sslmode=disable'
export REDIS_ADDR=127.0.0.1:6379
export REDIS_PASSWORD=
export REDIS_DB=0
export CAS_SERVER_URL=http://127.0.0.1:9999
export APP_BASE_URL=http://127.0.0.1:5173
export CAS_DEFAULT_REDIRECT=/
export SESSION_COOKIE_SAMESITE=lax
# If not using scripts/dev.sh, export the required S3_* settings from
# "Object storage (S3)" before starting.
go run .                       # runs on :8080

# 4. Start the frontend (separate terminal)
cd path/to/clinic_admin_frontend
pnpm dev                       # runs on :5173
```

Sessions whose staff `version` differs from the database-managed
`STAFF_VERSION` are rejected until the staff log in again. Raising the config
item forces existing staff sessions through the login page again.

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
