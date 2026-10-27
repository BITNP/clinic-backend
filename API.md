# API Reference

HTTP API for `clinic-backend`. The server listens on `:8080` and speaks JSON. All
request and response bodies are UTF-8 JSON. Routes are registered in
`internal/app/routes.go`; this document is the source of truth for their
externally visible behavior.

In deployment the admin frontend proxies `/api`, `/login`, and `/logout` to the
backend, so browser traffic is same-origin and no CORS handling exists.

```bash
BASE=http://127.0.0.1:8080

# Admin/staff examples assume cookies.txt holds the cookies set by GET /login.
CSRF='<value of the csrf_token cookie>'

# Client examples use the API-key signature described below.
DATE=$(date -u +"%a, %d %b %Y %H:%M:%S GMT")
USERNAME=20230001
KEY=$(printf '%s' "${CLINIC_API_KEY}${USERNAME}${DATE}" | md5sum | cut -d' ' -f1)
```

## Authentication

There are two independent authentication schemes. Every endpoint below states
which one it uses; `/healthz`, `/login`, `/logout`, and
`GET /api/announcement/toc` are public.

### Staff and admin (session or Keycloak)

Admin/staff endpoints accept either:

1. The `sessionid` cookie (name configurable) created by `GET /login`. This is
   the browser flow through university CAS.
2. `Authorization: Bearer <keycloak-jwt>` when Keycloak is configured.

Cookie-authenticated requests that mutate data (`POST`, `PUT`, `PATCH`,
`DELETE`) must also send:

```http
X-CSRF-Token: <value of the csrf_token cookie>
```

Missing or mismatched CSRF tokens receive `403 Forbidden`. Bearer-token requests
do not need a CSRF token.

Staff and admin are separate authorization levels:

- **Staff** endpoints (`RequireStaff`) accept role `staff` or `admin`.
- **Admin** endpoints (`RequireAdmin`) accept role `admin` only.

A session whose staff `version` differs from the configured `STAFF_VERSION` is
rejected with `401` and must log in again.

Sessions are created by `GET /login`, which redirects through CAS and sets both
cookies. `GET /logout` destroys the local session and redirects through CAS
logout.

### API-key clients

Customer-facing endpoints under `/api/` and `/api/wechat/` (except the public
`/api/announcement/toc`) use a signature shared with the DingTalk proxy:

```http
Date:      <RFC1123 timestamp, e.g. Mon, 02 Nov 2026 10:00:00 GMT>
X-API-KEY: md5(CLINIC_API_KEY + username + Date)
?username=<job number of the logged-in customer>
```

- All three values are required; missing ones receive `401`.
- The `Date` header must be within **5 minutes** of server time.
- The signature is MD5 hex of the concatenation, compared in constant time.
- `username` is a **query parameter on every request**, so it must be preserved
  when adding other query parameters.

The verified `username` becomes the record owner; it is never taken from a JSON
body.

## Errors

Most errors share one shape:

```json
{ "error": "human-readable message" }
```

Customer ticket endpoints instead use `{"msg": "..."}` for booking validation
failures on `400`, matching the old mini-program protocol (messages are
Chinese, e.g. `该日期诊所停止营业`). Request-body binding failures use
`{"error": "..."}` even on ticket endpoints.

| Status | Meaning |
| --- | --- |
| `400 Bad Request` | Malformed JSON, missing/invalid field, invalid id/date range, or invalid record state transition |
| `401 Unauthorized` | Missing/invalid session, bearer token, or API-key signature |
| `403 Forbidden` | Authenticated but not allowed (wrong role, missing CSRF token, ticket owned by someone else) |
| `404 Not Found` | Referenced resource does not exist (or a staff user cannot see a disabled schedule) |
| `409 Conflict` | Uniqueness/in-use conflict, second enabled schedule, duplicate TOS, revert unavailable/expired/superseded |
| `500 Internal Server Error` | Unexpected server or database error |
| `502 Bad Gateway` | CAS ticket validation could not reach the CAS server |
| `503 Service Unavailable` | CAS is not configured, or `GET /healthz` cannot reach Redis |

## Conventions

- **Pagination** uses `page` (default `1`) and `pageSize` (default `20`).
  Admin record listings cap `pageSize` at `100`. Invalid or non-positive values
  fall back to defaults.
- Paginated responses look like:

  ```json
  { "items": [], "total": 0, "page": 1, "pageSize": 20 }
  ```

  Staff lists and work-schedule assignment lists return only `{ "items": [...] }`.
- **IDs** are positive integers. Invalid ids return `400`.
- **Dates and times**:
  - Appointment dates in record/ticket views are `YYYY-MM-DD`.
  - Request timestamps for service dates are RFC3339
    (`2026-11-01T09:00:00Z`). Announcements and work schedules also accept
    date-only `YYYY-MM-DD` where noted.
  - Announcement `createdTime`, `lastEditedTime`, `expireDate`, service date
    `date`/`startTime`/`endTime`, work schedule `start_date`/`end_date` and
    weekday times are RFC3339 in responses.
  - Day boundaries use `CLINIC_TIMEZONE` (default `UTC+8`). Legacy `/api/date/*`
    responses are always formatted in UTC+8.
- **Change counters:** successful data-changing requests bump a per-group Redis
  counter that clients poll (see *Sync*). A counter failure never fails the
  write.

## Endpoints

### `GET /healthz`

**Description:** Liveness/readiness check. Public. Pings Redis on every call.
Returns `503` when Redis is unreachable.

**Request:** No body, no parameters.

**Response `200 OK`**

```json
{ "status": "ok", "redis": "up" }
```

**Response `503 Service Unavailable`**

```json
{ "status": "unavailable", "redis": "down" }
```

**curl**

```bash
curl "$BASE/healthz"
```

### `GET /login`

**Description:** Starts or completes CAS login. Without a `ticket` it redirects
(`302`) to the CAS login URL. With a `ticket` it validates the ticket, maps CAS
groups to a staff/admin role, upserts the staff row and work years, creates a
session, sets the `sessionid` (HttpOnly) and `csrf_token` cookies, then
redirects to `next`. Login also bumps the `staff` change counter.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `next` | string | no | Same-origin path to return to after login. Must start with `/` and not `//`; otherwise `CAS_DEFAULT_REDIRECT` is used. |
| `ticket` | string | no | CAS service ticket; provided by the CAS redirect, not by callers. |

**Response `302 Found`** with a `Location` header (CAS login page, or `next`).

**Error responses:** `403` (invalid ticket, no qualifying groups), `500`
(failed to resolve staff or create session), `502` (CAS unreachable), `503`
(CAS not configured).

**curl**

```bash
curl -i "$BASE/login?next=/"
```

### `GET /logout`

**Description:** Deletes the local session if present, clears both cookies, and
redirects (`302`) through CAS logout (or straight to `next` when CAS is not
configured). Public.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `next` | string | no | Same-origin path to return to. Validated like `/login`. |

**Response `302 Found`**

**curl**

```bash
curl -i "$BASE/logout?next=/"
```

### `GET /api/user`

**Description:** Returns the currently authenticated staff member. Accepts a
session cookie or a Keycloak bearer token.

**Request:** No body, no parameters.

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Staff id. |
| `account_id` | string | CAS/Keycloak account id. |
| `realname` | string | Display name. |
| `email` | string | Email address. |
| `role` | string | `staff` or `admin` (`""` if unknown). |

```json
{
  "id": 1,
  "account_id": "20230001",
  "realname": "张三",
  "email": "zhangsan@example.com",
  "role": "staff"
}
```

**Error responses:** `401` (not authenticated), `500` (session error).

**curl**

```bash
curl "$BASE/api/user" -b cookies.txt
```

### `GET /api/users/me`

**Description:** Like `/api/user`, but authentication is optional and the
endpoint always returns `200`. Anonymous callers receive a zero-valued user
(`id` is `0`), which is how the frontend detects "not logged in".

**Response `200 OK`** — same shape as `/api/user`; anonymous:

```json
{ "id": 0, "account_id": "", "realname": "", "email": "", "role": "" }
```

**Error responses:** none expected (`500` only on unexpected errors).

**curl**

```bash
curl "$BASE/api/users/me" -b cookies.txt
```

## Admin: announcements

Announcement object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Announcement id. |
| `title` | string | Title, max 20 characters. |
| `content` | string | Body text. |
| `tag` | string | `normal`, `pinned`, or `tos`. |
| `enabled` | boolean | Staff-facing on/off switch. |
| `brief` | string | Short summary, max 64 characters. |
| `createdTime` | string | RFC3339 creation time. |
| `lastEditedTime` | string | RFC3339 last-edit time. |
| `expireDate` | string | RFC3339 expiration date. `tos` announcements are never filtered out by expiry. |
| `priority` | integer | Higher sorts first. |

Listing order is `priority DESC, created_time DESC`. Mutations bump the
`announcement` change counter.

### `GET /api/admin/announcements`

**Description:** Lists announcements. Staff and admin.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `tag` | string | no | Exact tag filter (`normal`, `pinned`, `tos`). |
| `active` | string | no | `true` returns only enabled announcements that are `tos` or not expired. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated announcement objects.

**Error responses:** `401` (not authenticated), `500`.

**curl**

```bash
curl "$BASE/api/admin/announcements?page=1&pageSize=20" -b cookies.txt
```

### `GET /api/admin/announcements/{id}`

**Description:** Returns one announcement. Staff and admin.

**Request:** No body. `id` is a positive integer.

**Response `200 OK`** — announcement object.

**Error responses:** `400` (invalid id), `401`, `404`.

**curl**

```bash
curl "$BASE/api/admin/announcements/1" -b cookies.txt
```

### `POST /api/admin/announcements`

**Description:** Creates an announcement. Admin only. Bumps the `announcement`
counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `title` | string | yes | Max 20 characters. |
| `content` | string | yes | Body text. |
| `tag` | string | no | `normal` (default), `pinned`, or `tos`. Only one `tos` may exist. |
| `enabled` | boolean | no | Defaults to `true`. |
| `brief` | string | yes | Max 64 characters. |
| `expireDate` | string | yes | `YYYY-MM-DD` or RFC3339; must not be in the past. |
| `priority` | integer | no | Defaults to `0`. |

**Response `201 Created`** — created announcement object.

**Error responses:** `400` (bad body/date), `401`, `403` (admin only / CSRF),
`409` (a `tos` announcement already exists).

**curl**

```bash
curl -X POST "$BASE/api/admin/announcements" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"title":"国庆值班","content":"10 月 1 日暂停服务。","tag":"pinned","enabled":true,"brief":"假期安排","expireDate":"2026-12-31","priority":10}'
```

### `PUT /api/admin/announcements/{id}`

**Description:** Updates an announcement. Admin only. Omitted fields are left
unchanged; bumps the `announcement` counter.

**Request body** — all fields optional, same types and constraints as create.
`expireDate` may be `YYYY-MM-DD` or RFC3339 and must not be in the past.

**Response `200 OK`** — updated announcement object.

**Error responses:** `400`, `401`, `403`, `404`, `409` (second `tos`).

**curl**

```bash
curl -X PUT "$BASE/api/admin/announcements/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":false,"priority":0}'
```

### `DELETE /api/admin/announcements/{id}`

**Description:** Deletes an announcement. Admin only. Bumps the `announcement`
counter.

**Response `204 No Content`**

**Error responses:** `400` (invalid id), `401`, `403`, `404`.

**curl**

```bash
curl -X DELETE "$BASE/api/admin/announcements/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: service dates

Service date object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Service date id. |
| `capacity` | integer | Maximum ticketed appointments. |
| `room_id` | integer \| null | Owning room. |
| `date` | string | RFC3339 date. |
| `startTime` | string | RFC3339 service window start. |
| `endTime` | string | RFC3339 service window end. |
| `title` | string | Short label, max 20 characters. |
| `count` | integer | Ticketed bookings currently counted against capacity. Populated by read endpoints; create/update responses return `0`. |

Mutations bump the `service_date` counter.

### `GET /api/admin/service-dates`

**Description:** Lists upcoming service dates. Staff and admin. When neither
`from` nor `to` is provided the listing starts at today; an explicit range takes
precedence so past dates can be selected.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `room_id` | integer | no | Exact room filter. |
| `room_ids` | string | no | Comma-separated room ids; takes precedence over `room_id`. |
| `from` | string | no | RFC3339 inclusive lower bound. |
| `to` | string | no | RFC3339 inclusive upper bound; must not precede `from`. |
| `available` | string | no | `true` returns only dates with `count < capacity`. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated service date objects, ordered by date and
start time ascending.

**Error responses:** `400` (invalid room id or timestamp, `to` before `from`),
`401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/service-dates?room_id=1&page=1" -b cookies.txt
```

### `GET /api/admin/service-dates/all`

**Description:** Lists all service dates with no "today onward" restriction.
Staff and admin. Supports the same filters as the upcoming listing.

**Response `200 OK`** — paginated service date objects.

**Error responses:** `400`, `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/service-dates/all?available=true" -b cookies.txt
```

### `GET /api/admin/service-dates/{id}`

**Description:** Returns one service date with its booked `count`. Staff and
admin.

**Response `200 OK`** — service date object.

**Error responses:** `400` (invalid id), `401`, `404`.

**curl**

```bash
curl "$BASE/api/admin/service-dates/1" -b cookies.txt
```

### `POST /api/admin/service-dates`

**Description:** Creates a service date. Admin only. Bumps the `service_date`
counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `capacity` | integer | yes | Must be greater than zero. |
| `room_id` | integer | yes | Must reference an existing room. |
| `date` | string | yes | RFC3339 timestamp; the calendar day is used. Must not be in the past. |
| `startTime` | string | yes | RFC3339; must be before `endTime`. |
| `endTime` | string | yes | RFC3339; must fall on the same day as `startTime`. |
| `title` | string | yes | Max 20 characters. |

```json
{
  "capacity": 20,
  "room_id": 1,
  "date": "2026-11-02T00:00:00Z",
  "startTime": "2026-11-02T18:30:00Z",
  "endTime": "2026-11-02T21:00:00Z",
  "title": "周一晚"
}
```

**Response `201 Created`** — service date object.

**Error responses:** `400` (bad body, past date, inverted window, room missing),
`401`, `403`, `500`.

**curl**

```bash
curl -X POST "$BASE/api/admin/service-dates" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"capacity":20,"room_id":1,"date":"2026-11-02T00:00:00Z","startTime":"2026-11-02T18:30:00Z","endTime":"2026-11-02T21:00:00Z","title":"周一晚"}'
```

### `PUT /api/admin/service-dates/{id}`

**Description:** Updates a service date. Admin only. Omitted fields stay
unchanged. The date/time window is validated only when `date`, `startTime`, and
`endTime` are all present. Bumps the `service_date` counter.

**Request body** — all fields optional (same types/constraints as create).

**Response `200 OK`** — updated service date object.

**Error responses:** `400` (invalid window or room), `401`, `403`, `404`,
`409` (room/date changed while records exist, or capacity below current booked
count).

**curl**

```bash
curl -X PUT "$BASE/api/admin/service-dates/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"capacity":25}'
```

### `DELETE /api/admin/service-dates/{id}`

**Description:** Deletes a service date. Admin only. Bumps the `service_date`
counter.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`, `409` (records exist for that
room and date).

**curl**

```bash
curl -X DELETE "$BASE/api/admin/service-dates/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: rooms

Room object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Room id. |
| `name` | string | Unique room/campus name, max 64 characters. |
| `address` | string | Address text, max 256 characters. |
| `enabled` | boolean | Disabled rooms are not bookable. |

Mutations bump the `room` counter.

### `GET /api/admin/rooms`

**Description:** Lists rooms. Staff and admin.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Case-sensitive substring match. |
| `enabled` | string | no | `true` returns only enabled rooms. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated room objects, ordered by id ascending.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/admin/rooms?enabled=true" -b cookies.txt
```

### `GET /api/admin/rooms/{id}`

**Description:** Returns one room. Staff and admin.

**Response `200 OK`** — room object.

**Error responses:** `400`, `401`, `404`.

**curl**

```bash
curl "$BASE/api/admin/rooms/1" -b cookies.txt
```

### `POST /api/admin/rooms`

**Description:** Creates a room. Admin only. Bumps the `room` counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Max 64, unique. |
| `address` | string | yes | Max 256. |
| `enabled` | boolean | no | Defaults to `true`. |

**Response `201 Created`** — room object.

**Error responses:** `400`, `401`, `403`, `409` (name taken).

**curl**

```bash
curl -X POST "$BASE/api/admin/rooms" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"北区医务室","address":"北区 3 号楼 101","enabled":true}'
```

### `PUT /api/admin/rooms/{id}`

**Description:** Updates a room. Admin only. Disabling is refused while the room
has unfinished tickets. Bumps the `room` counter.

**Request body** — `name`, `address`, `enabled`, all optional.

**Response `200 OK`** — updated room object.

**Error responses:** `400`, `401`, `403`, `404`, `409` (name taken, or
unfinished tickets when disabling).

**curl**

```bash
curl -X PUT "$BASE/api/admin/rooms/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"address":"北区 3 号楼 102"}'
```

### `DELETE /api/admin/rooms/{id}`

**Description:** Deletes a room. Admin only. Refused while unfinished tickets,
service dates, or records reference it. Bumps the `room` counter.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`, `409` (unfinished tickets or
room in use).

**curl**

```bash
curl -X DELETE "$BASE/api/admin/rooms/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: records

Record lifecycle states:

| Value | Meaning |
| --- | --- |
| `pending` | Submitted, awaiting approval |
| `confirmed` | Approved |
| `arrived` | Customer checked in |
| `in_progress` | Repair in progress |
| `completed` | Repair finished |
| `rejected` | Rejected |
| `referred` | Referred elsewhere |
| `no_show` | Customer did not arrive |

Admin record view:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Record id. |
| `user` | string | Customer username (job number). |
| `realname` | string | Customer name. |
| `phone_num` | string | Phone number. |
| `status` | string | One of the lifecycle states above. |
| `appointment_time` | string | `YYYY-MM-DD`. |
| `description` | string | Customer problem description. |
| `campus` | string | Room name. |
| `worker_desc` | string | Staff repair notes. |
| `reject_reason` | string | Rejection reason, if rejected. |
| `referral_reason` | string | Referral reason, if referred. |
| `model` | string | Laptop model. |
| `password` | string | Device password supplied by the customer. |
| `tag` | string | Record tag title (e.g. `no_pending` for untagged). |
| `liquid_metal_status` | string | `unknown`, `liquid_metal`, `to_verify`, or `non_liquid_metal`. |
| `arrive_time` | string | RFC3339; omitted until arrival. |
| `finish_time` | string | RFC3339; omitted until completion. |
| `worker_id` | integer | Staff id of the assigned worker; omitted when unset. |
| `approver_id` | integer | Staff id of the approver; omitted when unset. |
| `revertible` | boolean | Whether the caller can undo their latest action now. |
| `revertible_until` | string | RFC3339 deadline for that revert; omitted when not revertible. |
| `revertible_count` | integer | Number of the caller's in-window actions on this record. |
| `last_action_at` | string | RFC3339 time of the caller's latest action; omitted when none. |

All record mutations bump the `record` counter and return the updated view.

### `GET /api/admin/records`

**Description:** Lists records for staff/admin. Newest ids first.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status` | string | no | Exact lifecycle state. Unknown values yield an empty page. |
| `realname` | string | no | Case-insensitive substring match. |
| `room_id` | integer | no | Exact room filter; non-numeric values are ignored. |
| `from_date` | string | no | `YYYY-MM-DD` inclusive lower bound on appointment date. |
| `to_date` | string | no | `YYYY-MM-DD` inclusive upper bound. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`, capped at `100`. |

**Response `200 OK`** — paginated admin record views, each with the caller's
revert metadata.

**Error responses:** `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/records?status=pending&page=1&pageSize=50" -b cookies.txt
```

### `GET /api/admin/records/{id}`

**Description:** Returns one record including revert metadata for the caller.

**Response `200 OK`** — admin record view.

**Error responses:** `400`, `401`, `404`.

**curl**

```bash
curl "$BASE/api/admin/records/1" -b cookies.txt
```

### `PUT /api/admin/records/{id}`

**Description:** Updates staff-maintained record fields. Currently only
`worker_desc`. Providing it creates/updates the worker side row and records a
revertible `update` action. A JSON object body is required.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `worker_desc` | string | no | Staff repair notes. |

**Response `200 OK`** — admin record view.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/records/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"worker_desc":"已更换风扇并清灰。"}'
```

### `POST /api/admin/records/{id}/confirm`

**Description:** Approves a pending record (`pending` → `confirmed`). Admin only.
Sets `approver_id`.

**Request:** No body.

**Response `200 OK`** — admin record view.

**Error responses:** `400` (invalid id or invalid transition), `401`, `403`,
`404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/confirm" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `POST /api/admin/records/{id}/reject`

**Description:** Rejects a pending record (`pending` → `rejected`). Admin only.
Stores the rejection reason.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `reason` | string | yes | Non-empty rejection reason. |

**Response `200 OK`** — admin record view.

**Error responses:** `400` (missing reason / invalid transition), `401`, `403`,
`404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/reject" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"reason":"预约时间不在服务时段"}'
```

### `POST /api/admin/records/{id}/arrive`

**Description:** Marks a confirmed record as arrived (`confirmed` → `arrived`).
Staff or admin. Records the arrival time.

**Request:** No body.

**Response `200 OK`** — admin record view.

**Error responses:** `400` (invalid id/transition), `401`, `403`, `404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/arrive" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `POST /api/admin/records/{id}/in-progress`

**Description:** Starts repair (`confirmed` or `arrived` → `in_progress`). Staff
or admin. Assigns the acting staff member as the worker.

**Request:** No body.

**Response `200 OK`** — admin record view.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/in-progress" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `POST /api/admin/records/{id}/complete`

**Description:** Completes repair (`in_progress` → `completed`). Staff or admin.
Sets `finish_time`.

**Request:** No body.

**Response `200 OK`** — admin record view.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/complete" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `POST /api/admin/records/{id}/refer`

**Description:** Refers a record elsewhere. Allowed for any non-final state
(`pending`, `confirmed`, `arrived`, `in_progress`) → `referred`. Staff or admin.
Stores the referral reason. A JSON object body is required; `reason` may be
omitted or empty.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `reason` | string | no | Referral reason. |

**Response `200 OK`** — admin record view.

**Error responses:** `400` (invalid id/transition), `401`, `403`, `404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/refer" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"reason":"需要主板级维修，建议返厂"}'
```

### `POST /api/admin/records/{id}/no-show`

**Description:** Marks a confirmed record as a no-show (`confirmed` →
`no_show`). Staff or admin.

**Request:** No body.

**Response `200 OK`** — admin record view.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/no-show" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `POST /api/admin/records/{id}/revert`

**Description:** Undoes the caller's most recent reversible action on a record,
restoring the previous snapshot. Staff or admin; each staff member can only
revert their own actions. Reverts are strict LIFO and only valid inside
`RECORD_ACTION_WINDOW` (default `5m`). Reverting is also refused when the record
has changed since the action.

**Response `200 OK`** — admin record view after the restore.

**Error responses:** `400` (invalid id), `401`, `403` (wrong role), `404`,
`409` (`no revertible action`, `revert window expired`, or
`action has been superseded`).

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/revert" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: staff

Staff object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Staff id. |
| `account_id` | string | CAS/Keycloak account id, unique. |
| `realname` | string | Display name. |
| `email` | string | Email address. |
| `role` | string | `staff`, `admin`, or `""`. |
| `version` | integer | Login version; compared with `STAFF_VERSION`. |

Staff list item adds:

| Field | Type | Description |
| --- | --- | --- |
| `handled_count` | integer | Number of records this staff member has worked on. |
| `work_years` | integer[] | Work years recorded at login, newest first. |

All staff endpoints are **admin only**. Mutations bump the `staff` counter.

### `GET /api/admin/staff`

**Description:** Lists staff, ordered by id ascending.

**Response `200 OK`**

```json
{
  "items": [
    {
      "id": 1,
      "account_id": "20230001",
      "realname": "张三",
      "email": "zhangsan@example.com",
      "role": "staff",
      "handled_count": 12,
      "work_years": [2026, 2025]
    }
  ]
}
```

**Error responses:** `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/staff" -b cookies.txt
```

### `GET /api/admin/staff/{id}`

**Description:** Returns one staff row (no `handled_count`/`work_years`).

**Response `200 OK`** — staff object.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/admin/staff/1" -b cookies.txt
```

### `POST /api/admin/staff`

**Description:** Creates a staff row, or returns the existing one for
`account_id` (updating real name/email when supplied).

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | string | yes | Account id. |
| `realname` | string | no | Display name. |
| `email` | string | no | Email address; when non-empty it is written. |

**Response `201 Created`** — staff object.

**Error responses:** `400`, `401`, `403`, `500`.

**curl**

```bash
curl -X POST "$BASE/api/admin/staff" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"account_id":"20230002","realname":"李四","email":"lisi@example.com"}'
```

### `PUT /api/admin/staff/{id}`

**Description:** Updates real name/email.

**Request body** — `realname` and `email`, both optional.

**Response `200 OK`** — staff object.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/staff/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"realname":"张三（新）"}'
```

### `DELETE /api/admin/staff/{id}`

**Description:** Deletes a staff row.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X DELETE "$BASE/api/admin/staff/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: work schedules

Work schedule object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Schedule id. |
| `name` | string | Unique name, max 128 characters. |
| `start_date` | string | RFC3339 first day. |
| `end_date` | string | RFC3339 last day. |
| `enabled` | boolean | At most one schedule may be enabled. |
| `weekdays` | array | Weekly slots; omitted from list and create responses, present on get/update. |

Weekday slot:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Slot id. |
| `work_schedule_id` | integer | Owning schedule. |
| `weekday` | integer | `0` = Sunday … `6` = Saturday (Go weekday numbering). |
| `start_time` | string | RFC3339 time on 1970-01-01. |
| `end_time` | string | RFC3339 time on 1970-01-01. |
| `room_id` | integer | Room for the slot. |
| `room` | object | Embedded room; omitted when not preloaded. |
| `staff` | array | Staff assignments; omitted when empty. |

Assignment object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Assignment id. |
| `weekday_id` | integer | Slot id. |
| `staff_id` | integer | Staff id. |
| `schedule_id` | integer | Schedule id. |
| `staff` | object | Embedded staff; omitted when not preloaded. |

Mutations bump the `work_schedule` counter. Only `GET /api/admin/work-schedules`
and `GET /api/admin/work-schedules/{id}` are readable by staff; every other
endpoint in this section (including `/all`, `/service-availability`, staff
assignments, and weekday edits) is admin only.

### `GET /api/admin/work-schedules`

**Description:** Lists **enabled** schedules. Staff and admin. Newest data is
shared by all staff through the `work_schedule` counter.

**Query parameters:** `page` (default `1`), `pageSize` (default `20`).

**Response `200 OK`** — paginated schedules (without `weekdays`), ordered by id
ascending.

**Error responses:** `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/work-schedules" -b cookies.txt
```

### `GET /api/admin/work-schedules/all`

**Description:** Lists all schedules, enabled or not. Admin only.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | boolean | no | Filter by enabled state. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated schedules (without `weekdays`).

**Error responses:** `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/work-schedules/all?enabled=false" -b cookies.txt
```

### `GET /api/admin/work-schedules/service-availability`

**Description:** Reports, for every date in a range, whether the enabled
schedule has staff assigned in each requested room. Admin only.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `from` | string | yes | RFC3339 start date (inclusive). |
| `to` | string | yes | RFC3339 end date (inclusive); must not precede `from`. |
| `room_ids` | string | yes | Comma-separated room ids. |

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `items[].room_id` | integer | Room id. |
| `items[].date` | string | `YYYY-MM-DD`. |
| `items[].available` | boolean | Date is inside the schedule period and the weekday has staff for the room. |

```json
{
  "items": [
    { "room_id": 1, "date": "2026-11-02", "available": true },
    { "room_id": 1, "date": "2026-11-03", "available": false }
  ]
}
```

**Error responses:** `400` (missing/invalid parameters), `401`, `403`, `404`
(no enabled schedule).

**curl**

```bash
curl "$BASE/api/admin/work-schedules/service-availability?from=2026-11-02T00:00:00Z&to=2026-11-08T00:00:00Z&room_ids=1,2" \
  -b cookies.txt
```

### `GET /api/admin/work-schedules/{id}`

**Description:** Returns one schedule with weekday slots, rooms, and assigned
staff. Staff and admin; staff receive `404` for a disabled schedule.

**Response `200 OK`** — work schedule object including `weekdays`.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/admin/work-schedules/1" -b cookies.txt
```

### `POST /api/admin/work-schedules`

**Description:** Creates a schedule with optional weekly slots. Admin only.
Refuses to enable a second schedule.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Max 128, unique. |
| `start_date` | string | yes | `YYYY-MM-DD` or RFC3339; must not be after `end_date`. |
| `end_date` | string | yes | `YYYY-MM-DD` or RFC3339. |
| `enabled` | boolean | no | Defaults to `false`. |
| `weekdays` | array | no | Weekly slots. |
| `weekdays[].weekday` | integer | yes | `0`–`6` (Sunday–Saturday). |
| `weekdays[].start_time` | string | yes | `HH:MM` or `HH:MM:SS`; must be before `end_time`. |
| `weekdays[].end_time` | string | yes | `HH:MM` or `HH:MM:SS`. |
| `weekdays[].room_id` | integer | yes | Must exist. |
| `weekdays[].staff_ids` | integer[] | no | Staff ids; each must exist. |

**Response `201 Created`** — work schedule object. `weekdays` is omitted from
this response; fetch the schedule afterwards to read the created slots.

**Error responses:** `400` (invalid date range/weekday/time), `401`, `403`,
`404` (room or staff missing), `409` (name taken or another schedule already
enabled).

**curl**

```bash
curl -X POST "$BASE/api/admin/work-schedules" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"2026 秋季","start_date":"2026-09-01","end_date":"2027-01-15","enabled":true,"weekdays":[{"weekday":1,"start_time":"18:30","end_time":"21:00","room_id":1,"staff_ids":[1,2]}]}'
```

### `PUT /api/admin/work-schedules/{id}`

**Description:** Updates a schedule. Admin only. When `weekdays` is present it
**replaces** all existing slots and assignments.

**Request body** — all fields optional, same constraints as create.

**Response `200 OK`** — updated schedule with `weekdays`.

**Error responses:** `400`, `401`, `403`, `404`, `409` (name taken / duplicate
enabled schedule).

**curl**

```bash
curl -X PUT "$BASE/api/admin/work-schedules/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":false}'
```

### `DELETE /api/admin/work-schedules/{id}`

**Description:** Deletes a schedule and its slots/assignments. Admin only.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X DELETE "$BASE/api/admin/work-schedules/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `GET /api/admin/work-schedules/{id}/staff`

**Description:** Lists all staff assignments of a schedule. Admin only.

**Response `200 OK`**

```json
{ "items": [ { "id": 1, "weekday_id": 3, "staff_id": 2, "schedule_id": 1, "staff": { "id": 2, "realname": "李四" } } ] }
```

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/admin/work-schedules/1/staff" -b cookies.txt
```

### `POST /api/admin/work-schedules/{id}/staff`

**Description:** Assigns a staff member to a weekly slot. Admin only. Identify
the slot by `weekday_id`, or by `room_id` + `weekday`; if that slot does not
exist it is created with default hours 18:30–21:00. The staff member must belong
to the schedule's start year. Assigning twice returns the existing assignment.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `staff_id` | integer | yes | Staff to assign. |
| `weekday_id` | integer | conditional | Existing slot id. Required unless `room_id` + `weekday` are given. |
| `room_id` | integer | conditional | Used with `weekday` to resolve/create a slot. |
| `weekday` | integer | conditional | `0`–`6`. |

**Response `201 Created`** — assignment object.

**Error responses:** `400` (missing slot identifiers, invalid weekday, staff not
in the schedule's work year), `401`, `403`, `404` (schedule/staff/slot missing).

**curl**

```bash
curl -X POST "$BASE/api/admin/work-schedules/1/staff" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"weekday_id":3,"staff_id":2}'
```

### `DELETE /api/admin/work-schedules/{id}/staff`

**Description:** Removes a staff assignment. Admin only. A JSON object body is
required.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `weekday_id` | integer | yes | Slot id. |
| `staff_id` | integer | yes | Staff id. |

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404` (slot or assignment missing).

**curl**

```bash
curl -X DELETE "$BASE/api/admin/work-schedules/1/staff" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"weekday_id":3,"staff_id":2}'
```

### `GET /api/admin/work-schedules/{id}/valid-staff`

**Description:** Lists staff whose recorded work years include the schedule's
start year. Admin only.

**Response `200 OK`** — `{ "items": [<staff list item>] }`.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/admin/work-schedules/1/valid-staff" -b cookies.txt
```

### `PUT /api/admin/work-schedules/{id}/weekdays`

**Description:** Creates or updates one weekday slot's hours. Admin only.
Existing assignments are preserved.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `room_id` | integer | yes | Room id. |
| `weekday` | integer | yes | `0`–`6`. |
| `start_time` | string | yes | `HH:MM` or `HH:MM:SS`; before `end_time`. |
| `end_time` | string | yes | `HH:MM` or `HH:MM:SS`. |

**Response `200 OK`** — weekday slot object.

**Error responses:** `400` (invalid weekday/time), `401`, `403`, `404` (schedule
or room missing).

**curl**

```bash
curl -X PUT "$BASE/api/admin/work-schedules/1/weekdays" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"room_id":1,"weekday":1,"start_time":"19:00","end_time":"21:30"}'
```

## Admin: sync

The backend keeps one monotonically increasing counter per data group in Redis.
Clients store the values they last saw and refetch a group when its value
changes. Counters are best-effort: a Redis failure is logged and never fails the
underlying write.

Groups: `record`, `room`, `work_schedule`, `service_date`, `staff`,
`announcement`.

### `GET /api/admin/sync`

**Description:** Returns the current counter for every group. Staff and admin.

**Response `200 OK`**

```json
{
  "record": 42,
  "room": 3,
  "work_schedule": 5,
  "service_date": 9,
  "staff": 2,
  "announcement": 7
}
```

**Error responses:** `401`, `403`, `500` (Redis read failure).

**curl**

```bash
curl "$BASE/api/admin/sync" -b cookies.txt
```

### `GET /api/admin/sync/config`

**Description:** Reports the recommended polling interval. `interval_ms` is `0`
when periodic polling is disabled.

**Response `200 OK`**

```json
{ "interval_ms": 10000 }
```

**Error responses:** `401`, `403`.

**curl**

```bash
curl "$BASE/api/admin/sync/config" -b cookies.txt
```

## Client: announcements

These endpoints use API-key authentication. Announcement shape is the same as
the admin announcement object.

### `GET /api/announcements`

**Description:** Lists announcements visible to customers. Pass `active=true`
to receive only enabled, non-expired announcements (plus the TOS entry).

**Query parameters:** `tag`, `active`, `page`, `pageSize` — same semantics as the
admin listing.

**Response `200 OK`** — paginated announcement objects.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/announcements?username=$USERNAME&active=true" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/announcements/{id}`

**Description:** Returns one announcement.

**Response `200 OK`** — announcement object.

**Error responses:** `400`, `401`, `404`.

**curl**

```bash
curl "$BASE/api/announcements/1?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

## Client: service dates

Service date shape is the same as the admin service date object.

### `GET /api/service-dates`

**Description:** Lists service dates. Add `active=true` to restrict the listing
to today onward; `from`/`to` select an explicit range.

**Query parameters:** `active`, `room_id`, `room_ids`, `from`, `to`,
`available`, `page`, `pageSize` — same semantics as the admin listing.

**Response `200 OK`** — paginated service date objects, ordered by date/start
time ascending.

**Error responses:** `400` (invalid room/timestamp/range), `401`, `500`.

**curl**

```bash
curl "$BASE/api/service-dates?username=$USERNAME&active=true&available=true" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/service-dates/{id}`

**Description:** Returns one service date with booked `count`.

**Response `200 OK`** — service date object.

**Error responses:** `400`, `401`, `404`.

**curl**

```bash
curl "$BASE/api/service-dates/1?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

## Client: rooms

Room shape is the same as the admin room object.

### `GET /api/rooms`

**Description:** Lists rooms (including disabled ones unless `enabled=true`).

**Query parameters:** `name`, `enabled`, `page`, `pageSize`.

**Response `200 OK`** — paginated room objects.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/rooms?username=$USERNAME&enabled=true" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/rooms/{id}`

**Description:** Returns one room.

**Response `200 OK`** — room object.

**Error responses:** `400`, `401`, `404`.

**curl**

```bash
curl "$BASE/api/rooms/1?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

## Client: tickets

Ticket (customer-facing record) view:

| Field | Type | Description |
| --- | --- | --- |
| `url` | string | `/api/tickets/{id}`. |
| `user` | string | Owner username from the API key. |
| `status` | string | `pending`, `confirmed`, `arrived`, `in_progress`, `completed`, `rejected`, `referred`, or `no_show`. |
| `realname` | string | Customer name. |
| `phone_num` | string | Phone number. |
| `campus` | string | Room name. |
| `appointment_time` | string | `YYYY-MM-DD`. |
| `description` | string | Problem description. |
| `worker_description` | string | Staff notes. |
| `model` | string | Laptop model. |
| `reject_reason` | string | Rejection reason. |
| `password` | string | Device password. |
| `liquid_metal_status` | string | `unknown`, `liquid_metal`, `to_verify`, or `non_liquid_metal`. |

Booking rules (enforced on create and when room/date changes):

1. `campus` must name an existing room.
2. A service date must exist for that room and day.
3. Ticketed bookings must be below the service date capacity.
4. A customer may have at most one unfinished booking with an appointment today
   or later.
5. The appointment date must not be in the past.

### `GET /api/tickets`

**Description:** Lists the caller's tickets, newest first. Active bookings whose
appointment is in the past are hidden.

**Query parameters:** `username` (required for auth), `page`, `pageSize`.

**Response `200 OK`** — paginated ticket views.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/tickets?username=$USERNAME&page=1" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `POST /api/tickets`

**Description:** Creates a booking for the authenticated customer and (when the
tagger service is configured) starts asynchronous record tagging. Bumps the
`record` counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `realname` | string | yes | Customer name. |
| `phone_num` | string | yes | Phone number. |
| `campus` | string | yes | Room name. |
| `appointment_time` | string | yes | `YYYY-MM-DD`. |
| `description` | string | yes | Problem description. |
| `model` | string | no | Laptop model. |
| `password` | string | no | Device password. |

**Response `201 Created`** — ticket view.

**Error responses:** `400` with `{"msg": "..."}` for booking violations
(`campus 不存在`, date closed, no capacity, already one unfinished booking, past
date) or invalid `appointment_time`; `400` with `{"error": "..."}` for malformed
bodies; `401`, `500`.

**curl**

```bash
curl -X POST "$BASE/api/tickets?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"realname":"张三","phone_num":"13800000000","campus":"北区医务室","appointment_time":"2026-11-02","description":"风扇异响","model":"ThinkPad X1","password":"123456"}'
```

### `GET /api/tickets/working`

**Description:** Returns the caller's single unfinished booking, if any.

**Response `200 OK`** — `{ "count": 0 }` when none, otherwise
`{ "count": 1, "data": <ticket view> }`.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/tickets/working?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/tickets/finished`

**Description:** Lists the caller's closed tickets (`rejected`, `completed`,
`referred`, `no_show`), newest first.

**Query parameters:** `username`, `page`, `pageSize`.

**Response `200 OK`** — paginated ticket views.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/tickets/finished?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/tickets/{id}`

**Description:** Returns one of the caller's tickets.

**Response `200 OK`** — ticket view.

**Error responses:** `400`, `401`, `403` (owned by another user), `404`.

**curl**

```bash
curl "$BASE/api/tickets/1?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `PUT /api/tickets/{id}` and `PATCH /api/tickets/{id}`

**Description:** Updates the caller's ticket. `PUT` and `PATCH` behave
identically. Changing `campus` or `appointment_time` re-runs the open-date,
capacity, and past-date checks. Bumps the `record` counter.

**Request body** — all fields optional:

| Field | Type | Description |
| --- | --- | --- |
| `realname` | string | Customer name. |
| `phone_num` | string | Phone number. |
| `campus` | string | Room name; empty string is ignored. |
| `appointment_time` | string | `YYYY-MM-DD`. |
| `description` | string | Problem description. |
| `model` | string | Laptop model. |
| `password` | string | Device password. |

**Response `200 OK`** — updated ticket view.

**Error responses:** `400` (`{"msg": ...}` for booking violations, `{"error":
...}` for malformed bodies), `401`, `403`, `404`, `500`.

**curl**

```bash
curl -X PUT "$BASE/api/tickets/1?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"appointment_time":"2026-11-03"}'
```

### `DELETE /api/tickets/{id}`

**Description:** Deletes the caller's ticket and its side rows. Refused when the
appointment's service date no longer exists. Bumps the `record` counter.

**Response `204 No Content`**

**Error responses:** `400` (`{"msg": "该日期诊所停止营业"}`), `401`, `403`,
`404`.

**curl**

```bash
curl -X DELETE "$BASE/api/tickets/1?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

## Client: sync

### `GET /api/sync`

**Description:** Same counters as `/api/admin/sync`, for API-key clients.

**Response `200 OK`** — object mapping each group to its counter.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/sync?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/sync/config`

**Description:** Same polling configuration as `/api/admin/sync/config`.

**Response `200 OK`**

```json
{ "interval_ms": 10000 }
```

**Error responses:** `401`.

**curl**

```bash
curl "$BASE/api/sync/config?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

## Legacy customer endpoints

These preserve the old Django wire format for the light-app. They use API-key
authentication and return plain arrays or DRF envelopes instead of the standard
paginated shape. Successful writes bump the `record` counter.

Legacy record object:

| Field | Type | Description |
| --- | --- | --- |
| `url` | string | `/api/wechat/{id}/`. |
| `user` | string | Owner username. |
| `status` | integer | Legacy status code (see table below). |
| `realname` | string | Customer name. |
| `phone_num` | string | Phone number. |
| `campus` | string | Room name. |
| `appointment_time` | string | `YYYY-MM-DD` in `CLINIC_TIMEZONE`. |
| `description` | string | Problem description. |
| `worker_description` | string | Staff notes. |
| `model` | string | Laptop model. |
| `reject_reason` | string | Rejection reason. |
| `password` | string | Device password. |

Legacy status codes:

| Code | Status |
| --- | --- |
| `1` | `pending` |
| `2` | `confirmed` |
| `3` | `rejected` |
| `4` | `arrived` |
| `5` | `in_progress` |
| `6` | `completed` |
| `7` | `referred` |
| `9` | `no_show` |

DRF pagination envelope (fixed page size `10`):

```json
{ "count": 12, "next": "?page=2", "previous": null, "results": [] }
```

`next`/`previous` are relative query strings, or `null` at the boundaries.

### `GET /api/wechat` and `GET /api/wechat/`

**Description:** Lists the caller's tickets with legacy records and DRF
pagination.

**Query parameters:** `username`, `page`.

**Response `200 OK`** — DRF page of legacy records.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/wechat/?username=$USERNAME&page=1" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `POST /api/wechat` and `POST /api/wechat/`

**Description:** Creates a booking using the same body and validation rules as
`POST /api/tickets`.

**Response `201 Created`** — legacy record.

**Error responses:** `400` (`{"msg": ...}` for booking violations), `401`,
`500`.

**curl**

```bash
curl -X POST "$BASE/api/wechat/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"realname":"张三","phone_num":"13800000000","campus":"北区医务室","appointment_time":"2026-11-02","description":"风扇异响"}'
```

### `GET /api/wechat/working` and `GET /api/wechat/working/`

**Description:** Returns the caller's unfinished booking in the legacy shape.

**Response `200 OK`** — `{ "count": 0 }` or
`{ "count": 1, "data": <legacy record> }`.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/wechat/working/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/wechat/finished`, `GET /api/wechat/finished/`, `GET /api/wechat/finish`, `GET /api/wechat/finish/`

**Description:** Lists the caller's closed tickets in the legacy shape. All four
paths are equivalent.

**Query parameters:** `username`, `page`.

**Response `200 OK`** — DRF page of legacy records.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/wechat/finished/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/wechat/{id}` and `GET /api/wechat/{id}/`

**Description:** Returns one of the caller's tickets.

**Response `200 OK`** — legacy record.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/wechat/1/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `PUT /api/wechat/{id}` and `PATCH /api/wechat/{id}` (with optional trailing slash)

**Description:** Updates the caller's ticket using the same body and validation
rules as `PUT /api/tickets/{id}`.

**Response `200 OK`** — updated legacy record.

**Error responses:** `400`, `401`, `403`, `404`, `500`.

**curl**

```bash
curl -X PATCH "$BASE/api/wechat/1/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"description":"风扇异响，偶发停转"}'
```

### `DELETE /api/wechat/{id}` and `DELETE /api/wechat/{id}/`

**Description:** Deletes the caller's ticket, with the same rules as
`DELETE /api/tickets/{id}`.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X DELETE "$BASE/api/wechat/1/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/campus` and `GET /api/campus/`

**Description:** Lists enabled rooms as `{name, address}` items.

**Response `200 OK`**

```json
[
  { "name": "北区医务室", "address": "北区 3 号楼 101" }
]
```

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/campus/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/date` and `GET /api/date/`

**Description:** Lists service dates from today onward in the legacy shape,
formatted in UTC+8.

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Service date id. |
| `title` | string | Service date title. |
| `date` | string | `YYYY-MM-DD`. |
| `capacity` | integer | Capacity. |
| `campus` | string | Room name. |
| `startTime` | string | `HH:MM:SS`. |
| `endTime` | string | `HH:MM:SS`. |
| `count` | integer | Ticketed bookings. |
| `finish` | integer | Always `0` (kept for shape compatibility). |
| `working` | integer | Always `0` (kept for shape compatibility). |

```json
[
  {
    "id": 1,
    "title": "周一晚",
    "date": "2026-11-02",
    "capacity": 20,
    "campus": "北区医务室",
    "startTime": "18:30:00",
    "endTime": "21:00:00",
    "count": 5,
    "finish": 0,
    "working": 0
  }
]
```

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/date/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/date/all` and `GET /api/date/all/`

**Description:** Like `/api/date/` but includes past service dates.

**Response `200 OK`** — array of legacy date items.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/date/all/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/announcement` and `GET /api/announcement/`

**Description:** Lists active announcements in the legacy shape (enabled, not
expired, plus TOS).

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Announcement id. |
| `title` | string | Title. |
| `content` | string | Body text. |
| `brief` | string | Short summary. |
| `tag` | string | `normal`, `pinned`, or `tos`. |
| `createdTime` | string | RFC3339 in `CLINIC_TIMEZONE`. |
| `lastEditedTime` | string | RFC3339 in `CLINIC_TIMEZONE`. |
| `expireDate` | string | `YYYY-MM-DD` in `CLINIC_TIMEZONE`. |
| `priority` | integer | Display priority. |

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/announcement/?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/announcement/toc` and `GET /api/announcement/toc/`

**Description:** Returns the terms-of-service announcement content. Public (no
authentication). When no TOS announcement exists, returns the fallback text
`暂无公告`.

**Response `200 OK`**

```json
{ "content": "..." }
```

**Error responses:** `500`.

**curl**

```bash
curl "$BASE/api/announcement/toc/"
```

## Related configuration

See [CONFIGURATION.md](CONFIGURATION.md) for every environment variable and
deployment setting. Settings that affect API behavior are noted inline above:
`CLINIC_API_KEY`, `CLINIC_TIMEZONE`, `STAFF_VERSION`, `SYNC_POLL_INTERVAL`,
`SESSION_*`, `CAS_*`, and `KEYCLOAK_*`.
