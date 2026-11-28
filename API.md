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
which one it uses; `/healthz`, `/login`, `/logout`,
`GET /api/downloads/stress-test-helper/download`,
`GET /api/announcement/toc`, and the sync endpoints described below are public.

### Staff, admin, and superadmin (session or Keycloak)

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

CAS and Keycloak roles are resolved from their `realm` values before groups are
checked. The realms `clinic-maintainer`,
`position-association-vice-chair-clinic`, and `position-clinic-head` map to
`superadmin`; `position-clinic-advisor` and `position-clinic-deputy-head` map to
`admin`. Superadmin takes precedence when both levels are present. Without a
matching realm, CAS retains its existing group-based `staff`/`admin` rules,
while Keycloak maps any group containing `clinic` to `staff`.

Authorization levels are hierarchical:

- **Staff** endpoints (`RequireStaff`) accept `staff`, `admin`, or `superadmin`.
- **Admin** endpoints (`RequireAdmin`) accept `admin` or `superadmin`.

A session whose staff `version` differs from the database-managed
`STAFF_VERSION` config item is rejected with `401` and must log in again.
Changes to that item apply after a successful save.

Sessions are created by `GET /login`, which redirects through CAS and sets both
cookies. `GET /logout` destroys the local session and redirects through CAS
logout.

### API-key clients

Customer-facing endpoints under `/api/` and `/api/wechat/` (except the public
`/api/downloads/stress-test-helper/download`, `/api/announcement/toc`, and sync
endpoints) use a signature shared with the DingTalk proxy:

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

**Description:** Liveness/readiness check. Public. Pings Redis on every call
and returns `503` when Redis is unreachable. The response does not expose
individual dependency status.

**Request:** No body, no parameters.

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `status` | string | Always `ok` when Redis is reachable. |

```json
{ "status": "ok" }
```

**Response `503 Service Unavailable`**

| Field | Type | Description |
| --- | --- | --- |
| `status` | string | Always `unavailable` when Redis is unreachable. |

```json
{ "status": "unavailable" }
```

**curl**

```bash
curl "$BASE/healthz"
```

### `GET /login`

**Description:** Starts or completes CAS login. Without a `ticket` it redirects
(`302`) to the CAS login URL. With a `ticket` it validates the ticket, resolves
the role from CAS realms with group fallback, upserts the staff row and work
years, creates a session, sets the `sessionid` (HttpOnly) and `csrf_token`
cookies, then redirects to `next`. Login also bumps the `staff` change counter.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `next` | string | no | Same-origin path to return to after login. Must start with `/` and not `//`; otherwise `CAS_DEFAULT_REDIRECT` is used. |
| `ticket` | string | no | CAS service ticket; provided by the CAS redirect, not by callers. |

**Response `302 Found`** with a `Location` header (CAS login page, or `next`).

**Error responses:** `403` (invalid ticket, no qualifying realm or groups), `500`
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
| `qq_number` | string | QQ number from the `qq-number` login claim (`""` if unset). |
| `role` | string | `staff`, `admin`, `superadmin`, or `""` if unknown. |

```json
{
  "id": 1,
  "account_id": "20230001",
  "realname": "张三",
  "email": "zhangsan@example.com",
  "qq_number": "123456789",
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
{ "id": 0, "account_id": "", "realname": "", "email": "", "qq_number": "", "role": "" }
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

## Admin: campuses

Campus object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Campus id. |
| `name` | string | Unique campus name, max 64 characters. |
| `selection_policy` | string | `balanced` (default, including existing campuses) or `fulfill`. |

Mutations bump the `room` counter.

### `GET /api/admin/campuses`

**Description:** Lists campuses. Staff and admin.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Case-sensitive substring match. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated campus objects, ordered by id ascending.

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/admin/campuses" -b cookies.txt
```

### `GET /api/admin/campuses/{id}`

**Description:** Returns one campus. Staff and admin.

**Response `200 OK`** — campus object.

**Error responses:** `400`, `401`, `404`.

**curl**

```bash
curl "$BASE/api/admin/campuses/1" -b cookies.txt
```

### `POST /api/admin/campuses`

**Description:** Creates a campus. Admin only. Bumps the `room` counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Max 64, unique. |
| `selection_policy` | string | no | `balanced` (default) or `fulfill`; unsupported values return `400`. |

**Response `201 Created`** — campus object.

**Error responses:** `400`, `401`, `403`, `409` (name taken).

**curl**

```bash
curl -X POST "$BASE/api/admin/campuses" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"默认校区"}'
```

### `PUT /api/admin/campuses/{id}`

**Description:** Updates a campus. Admin only. Bumps the `room` counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Max 64, unique. |
| `selection_policy` | string | no | `balanced` or `fulfill`; omitted leaves unchanged, invalid/empty values return `400`. |

**Response `200 OK`** — updated campus object.

**Error responses:** `400`, `401`, `403`, `404`, `409` (name taken).

**curl**

```bash
curl -X PUT "$BASE/api/admin/campuses/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"主校区","selection_policy":"fulfill"}'
```

### `DELETE /api/admin/campuses/{id}`

**Description:** Deletes a campus. Admin only. Refused while any room belongs to
the campus. Bumps the `room` counter.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`, `409` (campus still has rooms).

**curl**

```bash
curl -X DELETE "$BASE/api/admin/campuses/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `GET /api/admin/campuses/{id}/rooms`

**Description:** Lists the rooms belonging to one campus. Staff and admin.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Case-sensitive substring match. |
| `enabled` | string | no | `true` returns only enabled rooms. |
| `type` | string | no | `normal` or `dynamic`. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated room objects, ordered by id ascending. Each
room includes its nested `campus` object.

**Error responses:** `400` (invalid id or type), `401`, `404` (campus not
found), `500`.

**curl**

```bash
curl "$BASE/api/admin/campuses/1/rooms?enabled=true&type=normal" \
  -b cookies.txt
```

## Admin: user ticket bans

A user ticket ban prevents a user from creating clinic tickets. A `global` ban
applies to every campus; a `campus` ban applies only to tickets for rooms in that
campus. Bans expire automatically when the current time reaches `expirate_at`.

Staff and admin users can read bans. Only admin and superadmin users can create,
update, or delete them. `user_id` and scope are immutable after creation.

User ticket ban object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Ban id. |
| `user_id` | string | Banned account id, max 50 characters. |
| `scope` | string | `global` or `campus`. Derived from `campus_id`. |
| `campus_id` | integer or null | Campus id for a campus ban; null for a global ban. |
| `reason` | string | Stored banning reason, max 500 characters. |
| `expirate_at` | string | RFC3339 timestamp at which the ban stops applying. |
| `active` | boolean | Whether `expirate_at` is after the current server time. |

### `GET /api/admin/user-ticket-bans`

**Description:** Lists user ticket bans, ordered by id ascending. Staff and
admin.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `user_id` | string | no | Exact user id match. |
| `scope` | string | no | `global` or `campus`. Invalid values return `400`. |
| `campus_id` | integer | no | Positive campus id. |
| `active` | boolean string | no | `true` for unexpired bans, `false` for expired bans. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated user ticket ban objects.

```json
{
  "items": [
    {
      "id": 1,
      "user_id": "1120221234",
      "scope": "campus",
      "campus_id": 1,
      "reason": "repeated abusive bookings",
      "expirate_at": "2026-12-31T23:59:59+08:00",
      "active": true
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 20
}
```

**Error responses:** `400` (invalid query parameter), `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/user-ticket-bans?scope=campus&active=true" \
  -b cookies.txt
```

### `GET /api/admin/user-ticket-bans/{id}`

**Description:** Returns one user ticket ban. Staff and admin.

**Path parameter**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer | yes | Ban id. |

**Response `200 OK`** — user ticket ban object.

**Error responses:** `400`, `401`, `403`, `404`, `500`.

**curl**

```bash
curl "$BASE/api/admin/user-ticket-bans/1" -b cookies.txt
```

### `POST /api/admin/user-ticket-bans`

**Description:** Creates a global or campus-level user ticket ban. Admin only.
Only one configured ban is allowed for the same user and scope.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `user_id` | string | yes | Non-empty account id, max 50 characters. |
| `scope` | string | yes | `global` or `campus`. |
| `campus_id` | integer | conditional | Required for `campus`; must be omitted or null for `global`. |
| `reason` | string | yes | Non-empty reason, max 500 characters. |
| `expirate_at` | string | yes | Future RFC3339 timestamp. |

**Response `201 Created`** — created user ticket ban object.

**Error responses:** `400` (invalid field, unknown campus, or past expiration),
`401`, `403`, `409` (same user and scope already configured), `500`.

**curl**

```bash
curl -X POST "$BASE/api/admin/user-ticket-bans" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"1120221234","scope":"campus","campus_id":1,"reason":"repeated abusive bookings","expirate_at":"2026-12-31T23:59:59+08:00"}'
```

### `PUT /api/admin/user-ticket-bans/{id}`

**Description:** Updates a ban reason, expiration, or both. Admin only.
Updating `expirate_at` to a future timestamp also reactivates an expired ban.

**Path parameter**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer | yes | Ban id. |

**Request body** — all fields optional:

| Field | Type | Description |
| --- | --- | --- |
| `reason` | string | Non-empty reason, max 500 characters. |
| `expirate_at` | string | Future RFC3339 timestamp. |

**Response `200 OK`** — updated user ticket ban object.

**Error responses:** `400`, `401`, `403`, `404`, `500`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/user-ticket-bans/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"reason":"extended after review","expirate_at":"2027-01-31T23:59:59+08:00"}'
```

### `DELETE /api/admin/user-ticket-bans/{id}`

**Description:** Immediately removes a user ticket ban. The user can create
tickets for that scope if no other active ban applies. Admin only.

**Path parameter**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer | yes | Ban id. |

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`, `500`.

**curl**

```bash
curl -X DELETE "$BASE/api/admin/user-ticket-bans/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: rooms

Room object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Room id. |
| `name` | string | Unique room name, max 64 characters. |
| `address` | string | Address text, max 256 characters. |
| `enabled` | boolean | Disabled rooms are not bookable. |
| `campus_id` | integer | Id of the campus the room belongs to. |
| `campus` | object | Nested campus `{id, name}`. |
| `type` | string | `normal` or `dynamic`. Defaults to `normal`. |

Mutations bump the `room` counter.

### `GET /api/admin/rooms`

**Description:** Lists rooms. Staff and admin.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Case-sensitive substring match. |
| `enabled` | string | no | `true` returns only enabled rooms. |
| `campus_id` | integer | no | Return only rooms belonging to this campus. |
| `type` | string | no | `normal` or `dynamic`. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated room objects, ordered by id ascending.

**Error responses:** `400` (invalid `campus_id` or `type`), `401`, `500`.

**curl**

```bash
curl "$BASE/api/admin/rooms?enabled=true&campus_id=1&type=normal" \
  -b cookies.txt
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
| `campus_id` | integer | yes | Existing campus id. |
| `enabled` | boolean | no | Defaults to `true`. |
| `type` | string | no | `normal` or `dynamic`. Defaults to `normal`. |

**Response `201 Created`** — room object.

**Error responses:** `400` (missing or invalid `campus_id`, invalid `type`),
`401`, `403`, `409` (name taken).

**curl**

```bash
curl -X POST "$BASE/api/admin/rooms" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"北区医务室","address":"北区 3 号楼 101","campus_id":1,"enabled":true,"type":"normal"}'
```

### `PUT /api/admin/rooms/{id}`

**Description:** Updates a room. Admin only. Disabling is refused while the room
has unfinished tickets. Bumps the `room` counter.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Max 64, unique. |
| `address` | string | no | Max 256. |
| `campus_id` | integer | no | Move the room to this campus. |
| `enabled` | boolean | no | `true` enables, `false` disables. |
| `type` | string | no | `normal` or `dynamic`. |

**Response `200 OK`** — updated room object.

**Error responses:** `400` (invalid `campus_id` or `type`), `401`, `403`, `404`,
`409` (name taken, or unfinished tickets when disabling).

**curl**

```bash
curl -X PUT "$BASE/api/admin/rooms/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"address":"北区 3 号楼 102","campus_id":2,"type":"dynamic"}'
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

## Admin: configuration

All configuration endpoints require `admin` or `superadmin`; ordinary `staff`
users receive `403`. Group endpoints are available to both allowed roles.
Item endpoints are scoped by the item's `role_level`: an `admin` can access only
`admin` items, while a `superadmin` can access items at every level. Direct
requests for items outside the caller's scope return `404`.

Configuration group object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Group id. |
| `name` | string | Unique machine-readable name, max 64 characters. Immutable after creation. |
| `display_name` | string | Human-readable name, max 128 characters. |
| `sort_order` | integer | Non-negative position among groups. |

Configuration item object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Item id. |
| `name` | string | Globally unique machine-readable name, max 128 characters. Immutable after creation. |
| `value` | string | Canonical text representation of the value. Omitted when `writeonly` is `true`. |
| `value_type` | string | `int`, `double`, `boolean`, or `string`. |
| `display_name` | string | Human-readable name, max 128 characters. |
| `unit` | string | Optional display unit, max 32 characters. Omitted when empty. |
| `hint` | string | Optional display hint, max 512 characters. Omitted when empty. |
| `is_required` | boolean | When true, an empty value is rejected. |
| `role_level` | string | Role-level metadata: `staff`, `admin`, or `superadmin`. Defaults to `admin`. |
| `writeonly` | boolean | When true, the stored value is available only to internal backend code and is never returned through this API. Defaults to `false`. |
| `group_id` | integer | Owning configuration group. |
| `sort_order` | integer | Non-negative position within the group. |

Integer and double values are normalized to canonical decimal representations.
Booleans must be `true` or `false`. Optional items may have an empty value.
Write-only items can still be created, updated, and deleted through the normal
item endpoints. `unit` and `hint` are presentation metadata; they do not change
how the value is parsed or validated.

The backend seeds and reserves `SYNC_POLL_INTERVAL`, `STAFF_VERSION`,
`RECORD_ACTION_WINDOW`, `RECORD_PHOTO_MAX_COUNT`,
`RECORD_PHOTO_MAX_SIZE_BYTES`, `SERVICE_DATE_REMINDER_ENABLED`,
`SERVICE_DATE_REMINDER_LEAD_TIMES`, `SERVICE_DATE_REMINDER_TEMPLATE`,
`MESSAGE_GATEWAY_URL`, `MESSAGE_GATEWAY_ACCOUNT`,
`MESSAGE_GATEWAY_ACCOUNT_TOKEN`, `MESSAGE_GATEWAY_TIMEOUT`,
`TAGGER_LLM_API_KEY`, `TAGGER_LLM_BASE_URL`, `TAGGER_LLM_MODEL`,
`TAGGER_AGENT_POOL_SIZE`, `TAGGER_CONTEXT_TOKENS`, `TAGGER_TIMEOUT`,
`DINGTALK_NOTIFY_ENABLED`, `DINGTALK_BASE_URL`, `DINGTALK_APP_CODE`,
`DINGTALK_APP_KEY`, `DINGTALK_APP_SECRET`, `DINGTALK_TIMEOUT`,
`DINGTALK_REMINDER_LEAD_TIME`, `DINGTALK_REMINDER_POLL_INTERVAL`,
`DINGTALK_REDIRECT_ENABLED`, `DINGTALK_REDIRECT_RECEIVER`,
`DINGTALK_NOTIFICATION_TITLE`, `DINGTALK_NOTIFICATION_HEADER`,
`DINGTALK_NOTIFICATION_QQ_ACCOUNT`, `DINGTALK_TEMPLATE_CONFIRMED`,
`DINGTALK_TEMPLATE_REJECTED`, `DINGTALK_TEMPLATE_REFERRED`,
`DINGTALK_TEMPLATE_TRANSFER`, `DINGTALK_TEMPLATE_NO_SHOW`, and
`DINGTALK_TEMPLATE_REMINDER`. Their types, required flags, write-only flags,
and role levels are fixed and their values receive key-specific validation.
`TAGGER_LLM_API_KEY`,
`TAGGER_LLM_BASE_URL`, `TAGGER_LLM_MODEL`, `TAGGER_AGENT_POOL_SIZE`,
`TAGGER_CONTEXT_TOKENS`, `TAGGER_TIMEOUT`, `MESSAGE_GATEWAY_URL`,
`MESSAGE_GATEWAY_ACCOUNT`, `MESSAGE_GATEWAY_ACCOUNT_TOKEN`, and
`MESSAGE_GATEWAY_TIMEOUT` are `superadmin` items. DingTalk enablement,
credentials, transport, redirect, and polling items are also `superadmin`;
notification content, lead time, organization fields, and templates are
`admin` items. The remaining built-ins are `admin` items. Initial values come
from the external
`runtime_settings.json` file beside `.env`; environment variables with these
names may only initialize a missing item during migration and are ignored once
the row exists. `TAGGER_LLM_API_KEY`, `MESSAGE_GATEWAY_ACCOUNT_TOKEN`, and
`DINGTALK_APP_SECRET` are optional and write-only: their values are never
returned by the API. All successful saves apply without restarting the backend;
tagger changes atomically replace the active pool generation, DingTalk changes
atomically replace the prepared message-center client, and reminder delivery
reads new gateway settings on its next send. The service-date reminder template
supports `Date`, `StartTime`, `Room`, `PendingCount`, and `LeadTime`. DingTalk
reminder scheduling reads its current lead time and polling interval at the next
worker cycle.

### `GET /api/admin/config`

**Description:** Returns the configuration catalog visible to the caller as two
flat arrays. An `admin` receives only items whose `role_level` is `admin`; a
`superadmin` receives all items. All groups are returned. Groups are ordered by
`sort_order`, then `id`. Items are ordered by their group's `sort_order`,
`group_id`, item `sort_order`, and item `id`.

**Response `200 OK`**

```json
{
  "groups": [
    {
      "id": 1,
      "name": "booking",
      "display_name": "预约设置",
      "sort_order": 0
    }
  ],
  "items": [
    {
      "id": 10,
      "name": "max_booking_days",
      "value": "7",
      "value_type": "int",
      "display_name": "最多提前预约天数",
      "unit": "天",
      "hint": "最多可以提前预约的天数。",
      "is_required": true,
      "role_level": "admin",
      "writeonly": false,
      "group_id": 1,
      "sort_order": 0
    }
  ]
}
```

Both arrays are always present and are empty arrays when no records exist.
Items contain `group_id` only; they do not embed a group object.
For an item with `writeonly: true`, the `value` property is omitted.

**Error responses:** `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/config" -b cookies.txt
```

### `GET /api/admin/config/groups/{id}`

**Description:** Returns one configuration group.

**Response `200 OK`** — configuration group object.

**Error responses:** `400` (invalid id), `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/admin/config/groups/1" -b cookies.txt
```

### `POST /api/admin/config/groups`

**Description:** Creates a configuration group. The name is immutable after
creation.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Unique machine name, max 64 characters. |
| `display_name` | string | yes | Human-readable name, max 128 characters. |
| `sort_order` | integer | no | Non-negative position. Defaults to `0`. |

**Response `201 Created`** — configuration group object.

**Error responses:** `400` (invalid fields), `401`, `403`, `409` (name already
taken).

**curl**

```bash
curl -X POST "$BASE/api/admin/config/groups" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"booking","display_name":"预约设置","sort_order":0}'
```

### `PUT /api/admin/config/groups/{id}`

**Description:** Updates a configuration group. Omitted fields are unchanged.
The group name cannot be changed.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `display_name` | string | no | Human-readable name, max 128 characters. |
| `sort_order` | integer | no | Non-negative position. |

**Response `200 OK`** — updated configuration group object.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/config/groups/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"预约配置","sort_order":2}'
```

### `DELETE /api/admin/config/groups/{id}`

**Description:** Deletes an empty configuration group. Groups containing items
cannot be deleted.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404`, `409` (group still has items).

**curl**

```bash
curl -X DELETE "$BASE/api/admin/config/groups/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `GET /api/admin/config/items/{id}`

**Description:** Returns one configuration item when it is visible to the
caller. The response contains `group_id` and does not embed the group object.
The `value` property is omitted for write-only items.

**Response `200 OK`** — configuration item object.

**Error responses:** `400` (invalid id), `401`, `403`, `404` (missing or outside
the caller's item scope).

**curl**

```bash
curl "$BASE/api/admin/config/items/10" -b cookies.txt
```

### `POST /api/admin/config/items`

**Description:** Creates a configuration item. The name is immutable after
creation, and `group_id` must reference an existing group. An `admin` may create
only `admin` items; a `superadmin` may create any level.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Globally unique machine name, max 128 characters. |
| `value` | string | no | Defaults to an empty string. Must match `value_type`. |
| `value_type` | string | yes | `int`, `double`, `boolean`, or `string`. |
| `display_name` | string | yes | Human-readable name, max 128 characters. |
| `unit` | string | no | Optional display unit, max 32 characters. Defaults to empty. |
| `hint` | string | no | Optional display hint, max 512 characters. Defaults to empty. |
| `is_required` | boolean | no | Rejects an empty value when true. Defaults to `false`. |
| `role_level` | string | no | `staff`, `admin`, or `superadmin`. Defaults to `admin`. |
| `writeonly` | boolean | no | Hide the stored value from API responses. Defaults to `false`. |
| `group_id` | integer | yes | Existing configuration group id. |
| `sort_order` | integer | no | Non-negative position within the group. Defaults to `0`. |

**Response `201 Created`** — configuration item object. For a write-only item,
the submitted `value` is not echoed in the response.

**Error responses:** `400` (invalid fields or value), `401`, `403` (item level
outside the caller's scope), `404` (group not found), `409` (item name already
taken).

**curl**

```bash
curl -X POST "$BASE/api/admin/config/items" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"name":"max_booking_days","value":"7","value_type":"int","display_name":"最多提前预约天数","unit":"天","hint":"最多可以提前预约的天数。","is_required":true,"role_level":"admin","writeonly":true,"group_id":1,"sort_order":0}'
```

### `PUT /api/admin/config/items/{id}`

**Description:** Updates a configuration item. Omitted fields are unchanged.
The item name cannot be changed. The final value is validated against the final
`value_type` and `is_required` settings.
An `admin` may update only items that are currently `admin` level and cannot
change them to another level. A `superadmin` may update items at every level.
Known runtime settings take effect after a successful save for subsequent
operations. Existing operations may finish with the previous value. Enabling
service-date reminders requires a gateway URL and account token; invalid URLs,
durations, accounts, or templates are rejected without changing the saved or
active values. Disabling reminders pauses new discovery and delivery; a message
already submitted may still be delivered. The next reminder uses the committed
URL, account, write-only token, timeout, and template without a backend
restart. Pending reminders for removed lead times are cancelled with the save;
claimed jobs are checked before delivery. Increasing the action window cannot
restore actions already expired or pruned from the journal under the previous
window.

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `value` | string | no | New value. |
| `value_type` | string | no | `int`, `double`, `boolean`, or `string`. |
| `display_name` | string | no | Human-readable name, max 128 characters. |
| `unit` | string | no | New display unit, max 32 characters. Use `""` to clear it. |
| `hint` | string | no | New display hint, max 512 characters. Use `""` to clear it. |
| `is_required` | boolean | no | Whether an empty value is allowed. |
| `role_level` | string | no | `staff`, `admin`, or `superadmin`. |
| `writeonly` | boolean | no | Hide the stored value from API responses. |
| `group_id` | integer | no | Move the item to another existing group. |
| `sort_order` | integer | no | Non-negative position within the group. |

**Response `200 OK`** — updated configuration item object. When the resulting
item is write-only, the current value is not included in the response.

**Error responses:** `400` (invalid fields or value), `401`, `403` (changing an
item to a level outside the caller's scope), `404` (hidden item or target group
not found).

**curl**

```bash
curl -X PUT "$BASE/api/admin/config/items/10" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"value":"14","display_name":"最多提前预约天数","unit":"天","hint":"最多可以提前预约的天数。","writeonly":true,"group_id":2,"sort_order":1}'
```

### `DELETE /api/admin/config/items/{id}`

**Description:** Deletes a configuration item. Built-in runtime settings cannot
be deleted and return `400`. Delete authorization follows the item's current
`role_level`; write-only status does not prevent deletion.

**Response `204 No Content`**

**Error responses:** `400`, `401`, `403`, `404` (missing or outside the
caller's item scope).

**curl**

```bash
curl -X DELETE "$BASE/api/admin/config/items/10" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

## Admin: business journal

The journal is an append-only business history. Successful record staff
actions, record undo operations, configuration item updates, and reminder
notification lifecycle events are written in the same database transaction as
their corresponding state change. Immediate record-status notification outcomes
are written after the external provider verifies delivery and are linked to the
already-committed record transition. Read operations and skipped notifications
are not journaled.

Journal entries are immutable. A record undo creates a new `record.reverted`
event and does not modify the original event. Write-only and known secret
configuration values are stored as `{"changed":true,"redacted":true}`.

### `GET /api/admin/journal/entity-kinds`

**Description:** Lists the entity types and actions currently present in the
journal. Requires an admin or superadmin session or Keycloak token. Results are
grouped by entity type and sorted by entity type and action. The arrays are
empty when the journal has no events.

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `items` | array | Entity types with their recorded actions. |
| `items[].entity_type` | string | Stable entity category used by the `entity_type` filter. |
| `items[].actions` | array of strings | Stable actions recorded for this entity type. |

```json
{
  "items": [
    {
      "entity_type": "record",
      "actions": ["record.confirmed", "record.updated"]
    },
    {
      "entity_type": "service_date",
      "actions": ["notification.requested", "notification.sent"]
    }
  ]
}
```

**Error responses:** `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/journal/entity-kinds" \
  -b cookies.txt
```

### `GET /api/admin/journal`

**Description:** Lists journal events newest first. Requires an admin or
superadmin session or Keycloak token. Uses cursor pagination instead of the
page/pageSize convention used by older list endpoints.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `entity_type` | string | no | Exact entity type, for example `record`, `service_date`, or `config_item`. |
| `entity_id` | string | no | Exact entity identifier. |
| `actor_type` | string | no | One of `staff`, `customer`, `system`, or `integration`. |
| `actor_id` | string | no | Exact actor identifier. Staff IDs use their decimal string form. |
| `action` | string | no | Exact stable action, for example `record.confirmed`. |
| `from` | string | no | Inclusive lower `occurred_at` bound in RFC3339 format. |
| `to` | string | no | Inclusive upper `occurred_at` bound in RFC3339 format. |
| `cursor` | integer | no | Return events with IDs lower than this cursor. |
| `limit` | integer | no | Page size from 1 to 100; defaults to 100. |

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `items` | array | Journal events, newest first. |
| `items[].id` | integer | Monotonic event ID used for cursor pagination. |
| `items[].occurred_at` | string | Server-recorded event time in RFC3339. |
| `items[].recorded_at` | string | Persistence time in RFC3339. |
| `items[].actor_type` | string | Actor category. |
| `items[].actor_id` | string | Trusted actor identifier. |
| `items[].action` | string | Stable action name. |
| `items[].entity_type` | string | Affected entity category. |
| `items[].entity_id` | string | Affected entity identifier. |
| `items[].operation_id` | string | Groups events created by one operation. |
| `items[].caused_by_event_id` | integer or null | Optional causal event reference. |
| `items[].details` | object | Versioned operation-specific JSON payload. |
| `next_cursor` | integer or null | Pass as `cursor` to request the next page; null means no more results. |

```json
{
  "items": [
    {
      "id": 1002,
      "occurred_at": "2026-11-22T02:05:00Z",
      "recorded_at": "2026-11-22T02:05:00.012Z",
      "actor_type": "staff",
      "actor_id": "42",
      "action": "record.confirmed",
      "entity_type": "record",
      "entity_id": "123",
      "operation_id": "op_5f5f0ac1d4924c9f9d2d0e1b195f4c42",
      "caused_by_event_id": null,
      "details": {
        "version": 1,
        "changes": {
          "status": {
            "before": "pending",
            "after": "confirmed"
          },
          "approver_id": {
            "before": null,
            "after": 42
          }
        }
      }
    }
  ],
  "next_cursor": 1002
}
```

**Error responses:** `400` (invalid actor type, cursor, limit, time bound, or
time range), `401`, `403`, `500`.

**curl**

```bash
curl "$BASE/api/admin/journal?entity_type=record&entity_id=123&limit=20" \
  -b cookies.txt
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

At the clinic-local midnight (`CLINIC_TIMEZONE`, UTC+8 by default), records
whose appointment date has passed are closed automatically. `pending` and
`confirmed` records become `no_show`; `arrived` and `in_progress` records become
`completed`. An existing arrival timestamp is preserved. Automatic `no_show`
transitions send the same best-effort DingTalk notification described below
when the `DINGTALK_NOTIFY_ENABLED` config item is true.

DingTalk message text is rendered from configurable templates. The available
fields are `HEDR`, `DATE`, `ID`, `ROOM`, `LOCA`, `TRRO`, `TRLO`, `REAS`, and
`QQAC`; see `CONFIGURATION.md` for their source config items.
When DingTalk notifications are enabled, confirmed tickets receive a persisted
pre-shift reminder when their service window enters the configured lead-time
window (`DINGTALK_REMINDER_LEAD_TIME`, default `30m`). Disabling
`DINGTALK_NOTIFY_ENABLED` stops new discovery and sends; pending reminders
resume after it is re-enabled.

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
| `photo_count` | integer | Number of ordered photos attached to the worker notes. |
| `reject_reason` | string | Rejection reason, if rejected. |
| `referral_reason` | string | Referral reason, if referred. |
| `model` | string | Laptop model. |
| `password` | string | Device password supplied by the customer. |
| `tag` | string | Record tag title (e.g. `no_pending` for untagged). |
| `liquid_metal_status` | string | `unknown`, `liquid_metal`, `to_verify`, or `non_liquid_metal`. |
| `arrive_time` | string | RFC3339; omitted until arrival. |
| `finish_time` | string | RFC3339; omitted until completion. |
| `worker_id` | integer | Staff id of the assigned worker; omitted when unset. |
| `worker_realname` | string | Staff realname of the assigned worker; omitted when unset or unknown. |
| `approver_id` | integer | Staff id of the approver; omitted when unset. |
| `approver_realname` | string | Staff realname of the approver; omitted when unset or unknown. |
| `revertible` | boolean | Whether the caller can undo their latest action now. |
| `revertible_until` | string | RFC3339 deadline for that revert; omitted when not revertible. |
| `revertible_count` | integer | Number of the caller's in-window actions on this record. |
| `last_action_at` | string | RFC3339 time of the caller's latest action; omitted when none. |

All record lifecycle and `worker_desc` mutations below bump the `record`
counter and return the updated admin record view. Photo mutations also bump the
counter but return photo objects.

When an immediate DingTalk status notification is attempted, its verified
outcome is appended to the business journal as `notification.sent` or
`notification.failed`, linked to the corresponding record transition. Disabled
or unsupported notifications produce no notification journal event. The journal
stores the channel, template key, resulting record status, transfer flag,
message-center message ID, and sanitized failure code; it does not store the
rendered message body or recipient address.

### `GET /api/admin/records`

**Description:** Lists records for staff/admin. Newest ids first.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status` | string | no | Exact lifecycle state. Unknown values yield an empty page. |
| `realname` | string | no | Case-insensitive substring match. |
| `room_id` | integer | no | Exact room filter; non-numeric values are ignored. |
| `campus_id` | integer | no | Filter records by their room’s campus; non-numeric values are ignored. Combined with `room_id` when both are supplied. |
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

### Record photo positions

Worker-note photos are ordered. `position` is **1-based and contiguous**:

- A new photo appended with `POST` gets `position = count + 1`.
- Replacing a photo with `PUT` keeps its position.
- Deleting a photo shifts every later photo down by one.
- `GET` always returns photos in ascending position order.

The admin record view reports the current number of photos as `photo_count`.

Photo object:

| Field | Type | Description |
| --- | --- | --- |
| `id` | integer | Stable photo id. |
| `position` | integer | 1-based position in the record's ordered photo list. |
| `content_type` | string | `image/jpeg`, `image/png`, or `image/webp`. |
| `size` | integer | Object size in bytes. |
| `created_at` | string | RFC3339 creation time. |
| `url` | string | Backend URL for the image bytes. |

### `GET /api/admin/records/{id}/photos`

**Description:** Lists a record's photos in position order, together with the
active limits. Staff or admin only.

**Response `200 OK`**

```json
{
  "items": [
    {
      "id": 12,
      "position": 1,
      "content_type": "image/jpeg",
      "size": 204800,
      "created_at": "2026-10-27T12:00:00Z",
      "url": "/api/admin/records/7/photos/1"
    }
  ],
  "count": 1,
  "max_count": 9,
  "max_size_bytes": 10485760
}
```

A record with no photos returns `200` with `items: []` and `count: 0`.

**Error responses:** `400` (invalid id), `401`, `403`, `404`.

**curl**

```bash
curl "$BASE/api/admin/records/7/photos" -b cookies.txt
```

### `GET /api/admin/records/{id}/photos/{position}`

**Description:** Streams one photo's bytes through the backend. Staff or admin
only. The S3 bucket remains private.

**Response `200 OK`** — the image body, not JSON. Headers include:

```http
Content-Type: image/jpeg
Content-Length: 204800
ETag: "storage-etag"
Cache-Control: private, no-cache
X-Content-Type-Options: nosniff
```

**Error responses:** `400` (invalid id/position), `401`, `403`, `404`, `500`.

**curl**

```bash
curl "$BASE/api/admin/records/7/photos/1" -b cookies.txt -o photo.jpg
```

### `POST /api/admin/records/{id}/photos`

**Description:** Inserts one photo at the end of the record's ordered photo
list. Staff or admin only. Bumps the `record` counter.

**Request body** — `multipart/form-data`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `photo` | file | yes | One JPEG, PNG, or WebP image. |

The server detects the image type from the bytes, so the part's declared
`Content-Type` is ignored. The request and file are limited by the
database-managed `RECORD_PHOTO_MAX_SIZE_BYTES` config item; the record is
limited by `RECORD_PHOTO_MAX_COUNT`.

**Response `201 Created`** — the new photo object. A `Location` header points at
its position URL.

**Error responses:** `400` (invalid id, missing/empty file, more than one file,
unsupported type), `401`, `403`, `404` (record missing), `409` (photo limit
reached), `413` (file or request body too large), `500`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/7/photos" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -F 'photo=@repair.jpg'
```

### `PUT /api/admin/records/{id}/photos/order`

**Description:** Reorders a record's existing photos without changing their
image content, object keys, or stable ids. Staff or admin only. Bumps the
`record` synchronization counter.

Every photo currently attached to the record must appear exactly once in both
lists. `expected_photo_ids` is the client's snapshot of the current order; if
another request changed the order, the backend returns `409` and changes
nothing. Updates are atomic.

**Request body** — `application/json`

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_photo_ids` | integer[] | yes | Current ordered photo ids, as last seen by the client. |
| `photo_ids` | integer[] | yes | Complete desired order of the same photo ids. |

```json
{
  "expected_photo_ids": [11, 12, 13],
  "photo_ids": [13, 11, 12]
}
```

**Response `200 OK`** — the authoritative ordered photo list, in the same shape
as `GET /api/admin/records/{id}/photos`.

**Error responses:** `400` (invalid id or duplicate/missing/foreign photo ids),
`401`, `403`, `404` (record missing), `409` (the current order differs from
`expected_photo_ids`), `500`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/records/7/photos/order" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"expected_photo_ids":[11,12,13],"photo_ids":[13,11,12]}'
```

### `PUT /api/admin/records/{id}/photos/{position}`

**Description:** Replaces the photo at an existing position without changing
its position. Staff or admin only. Bumps the `record` counter.

> The admin frontend no longer uses this endpoint. Photo replacement was
> removed in favor of deletion plus ordering. It remains available for API
> compatibility.

**Request body** — same `multipart/form-data` `photo` field as `POST`.

**Response `200 OK`** — the updated photo object.

**Error responses:** `400` (invalid id/position or invalid file), `401`, `403`,
`404` (record or position missing), `413` (file too large), `500`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/records/7/photos/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -F 'photo=@repair-updated.jpg'
```

### `DELETE /api/admin/records/{id}/photos/{position}`

**Description:** Removes the photo at a position and shifts every later photo
down by one. Staff or admin only. Bumps the `record` counter.

**Response `204 No Content`**

**Error responses:** `400` (invalid id/position), `401`, `403`, `404` (record or
position missing), `500`.

**curl**

```bash
curl -X DELETE "$BASE/api/admin/records/7/photos/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF"
```

### `POST /api/admin/records/{id}/confirm`

**Description:** Approves a pending record (`pending` → `confirmed`). Admin only.
When `room_id` is supplied, the record is moved to that enabled room in the same
transaction. Sets `approver_id` and sends a best-effort DingTalk notification to
the record's `user` id. If `room_id` differs from the current room, the
`DINGTALK_TEMPLATE_TRANSFER` template is sent with title `工单转诊`; otherwise the
regular confirmed template is used. The message-center delivery status is
checked after submission; notification failures are recorded in the journal and
do not roll back the transition. The send is skipped when the
`DINGTALK_NOTIFY_ENABLED` config item is false.

**Request body** — optional

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `room_id` | integer | no | Enabled room selected by `调整场地`; when omitted the record keeps its current room. |

**Response `200 OK`** — admin record view.

**Error responses:** `400` (invalid id, invalid transition, or unavailable
room), `401`, `403`, `404`.

**curl**

```bash
curl -X POST "$BASE/api/admin/records/1/confirm" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"room_id":2}'
```

### `POST /api/admin/records/{id}/reject`

**Description:** Rejects a pending record (`pending` → `rejected`). Admin only.
Stores the rejection reason and sends a best-effort DingTalk notification to the
record's `user` id. The message-center delivery status is checked after
submission; notification failures are recorded in the journal and do not roll
back the transition. The send is skipped when the `DINGTALK_NOTIFY_ENABLED`
config item is false.

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
omitted or empty. Sends a best-effort DingTalk notification to the record's
`user` id. The message-center delivery status is checked after submission;
notification failures are recorded in the journal and do not roll back the
transition. The send is skipped when the `DINGTALK_NOTIFY_ENABLED` config item
is false.

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
`no_show`). Staff or admin. Sends a best-effort DingTalk notification to the
record's `user` id. The message-center delivery status is checked after
submission; notification failures are recorded in the journal and do not roll
back the transition. The send is skipped when the `DINGTALK_NOTIFY_ENABLED`
config item is false.

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
the database-managed `RECORD_ACTION_WINDOW` (default `5m`). Reverting is also
refused when the record has changed since the action.

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
| `qq_number` | string | QQ number, synced from the `qq-number` login claim. A missing claim does not clear it. |
| `role` | string | `staff`, `admin`, `superadmin`, or `""`. |
| `version` | integer | Login version; compared with the database-managed `STAFF_VERSION` config item. |

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
      "qq_number": "123456789",
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
`account_id` (updating real name, email, and QQ number when supplied).

**Request body**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | string | yes | Account id. |
| `realname` | string | no | Display name. |
| `email` | string | no | Email address; when non-empty it is written. |
| `qq_number` | string | no | QQ number; when non-empty it is written. |

**Response `201 Created`** — staff object.

**Error responses:** `400`, `401`, `403`, `500`.

**curl**

```bash
curl -X POST "$BASE/api/admin/staff" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"account_id":"20230002","realname":"李四","email":"lisi@example.com","qq_number":"123456789"}'
```

### `PUT /api/admin/staff/{id}`

**Description:** Updates real name, email, and QQ number.

**Request body** — `realname`, `email`, and `qq_number`, all optional.

**Response `200 OK`** — staff object.

**Error responses:** `400`, `401`, `403`, `404`.

**curl**

```bash
curl -X PUT "$BASE/api/admin/staff/1" \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' \
  -d '{"realname":"张三（新）","qq_number":"123456789"}'
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

## Sync

The backend keeps one monotonically increasing counter per data group in Redis.
Clients store the values they last saw and refetch a group when its value
changes. Counters are best-effort: a Redis failure is logged and never fails the
underlying write.

Groups: `record`, `room`, `work_schedule`, `service_date`, `staff`,
`announcement`, `config`, `journal`. Successful record and configuration writes
bump `journal` alongside their domain group. Background reminder journal writes
bump `journal` directly. Reads and failed writes do not bump counters. The
counter contains no settings or journal values.

### `GET /api/admin/sync`

**Description:** Returns the current counter for every group. Public endpoint;
the admin-prefixed path is retained for compatibility.

**Response `200 OK`**

```json
{
  "record": 42,
  "room": 3,
  "work_schedule": 5,
  "service_date": 9,
  "staff": 2,
  "announcement": 7,
  "config": 1,
  "journal": 18
}
```

**Error responses:** `500` (Redis read failure).

**curl**

```bash
curl "$BASE/api/admin/sync"
```

### `GET /api/admin/sync/config`

**Description:** Reports the recommended polling interval. `interval_ms` is `0`
when periodic polling is disabled. The value reflects the current runtime
setting. Public endpoint; the admin-prefixed path is retained for compatibility.

**Response `200 OK`**

```json
{ "interval_ms": 10000 }
```

**Error responses:** None.

**curl**

```bash
curl "$BASE/api/admin/sync/config"
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

## Client: campuses

Campus shape is the same as the admin campus object.

### `GET /api/campuses`

**Description:** Lists campuses available to API-key clients.

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | no | Case-sensitive substring match. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated campus objects, ordered by id ascending.

```json
{
  "items": [
    { "id": 1, "name": "中关村校区", "selection_policy": "balanced" }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 20
}
```

**Error responses:** `401`, `500`.

**curl**

```bash
curl "$BASE/api/campuses?username=$USERNAME" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

### `GET /api/campuses/{id}/rooms`

**Description:** Lists the rooms belonging to one campus for API-key clients.
Disabled rooms are included unless `enabled=true`, but are never recommended.
`preferred_room_id` recommends a room for ticket insertion on the required date.
Only enabled rooms with a service date and remaining capacity are eligible.
`balanced` chooses the fewest tickets (ties: lowest room id); `fulfill` chooses
the lowest room id until full, then the next eligible room. Counts match booking:
rejected, referred and no-show tickets are excluded; completed tickets are also
excluded for future dates. Today is determined in the configured clinic timezone.
Selection respects name/type filters across all matching rooms independently of
pagination, so the preferred room may be on another page. This does not reserve
capacity; ticket insertion still validates availability.

**Path parameter**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer | yes | Campus id. |

**Query parameters**

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `date` | string | yes | Appointment date, strictly `YYYY-MM-DD`, today or later. |
| `name` | string | no | Case-sensitive substring match. |
| `enabled` | string | no | `true` returns only enabled rooms. |
| `type` | string | no | `normal` or `dynamic`. |
| `page` | integer | no | Default `1`. |
| `pageSize` | integer | no | Default `20`. |

**Response `200 OK`** — paginated room objects, ordered by id ascending. Each
room includes its nested `campus` object.

| Field | Type | Description |
| --- | --- | --- |
| `items` | array | Room objects on this page. |
| `total` | integer | Total matching rooms, including unavailable rooms. |
| `page` | integer | Current page. |
| `pageSize` | integer | Page size. |
| `preferred_room_id` | integer or null | Recommended room id; null if none is eligible. |

```json
{"items":[],"total":0,"page":1,"pageSize":20,"preferred_room_id":null}
```

**Error responses:** `400` (invalid id/type, missing/malformed/past date), `401`, `404` (campus not
found), `500`.

**curl**

```bash
curl "$BASE/api/campuses/1/rooms?username=$USERNAME&date=2026-12-01&enabled=true&type=normal" \
  -H "Date: $DATE" -H "X-API-KEY: $KEY"
```

## Client: rooms

Room shape is the same as the admin room object.

### `GET /api/rooms`

**Description:** Lists rooms (including disabled ones unless `enabled=true`).

**Query parameters:** `name`, `enabled`, `campus_id`, `type`, `page`, `pageSize`.

**Response `200 OK`** — paginated room objects.

**Error responses:** `400` (invalid `campus_id` or `type`), `401`, `500`.

**curl**

```bash
curl "$BASE/api/rooms?username=$USERNAME&enabled=true&campus_id=1" \
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
in-process tagger agent is configured) starts asynchronous record tagging. Bumps
the `record` counter.

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

## Sync aliases

### `GET /api/sync`

**Description:** Public alias for `/api/admin/sync`. Returns the current counter
for every group.

**Response `200 OK`** — object mapping each group to its counter.

**Error responses:** `500` (Redis read failure).

**curl**

```bash
curl "$BASE/api/sync"
```

### `GET /api/sync/config`

**Description:** Public alias for `/api/admin/sync/config`. Reports the
recommended polling interval.

**Response `200 OK`**

```json
{ "interval_ms": 10000 }
```

**Error responses:** None.

**curl**

```bash
curl "$BASE/api/sync/config"
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

### `GET /api/downloads/stress-test-helper/download`

**Description:** Selects the latest multimeter executable for the requester's
platform, verifies that the object is readable with the configured storage
credentials, and returns a short-lived S3 download URL. Public (no
authentication).

The backend prefers the `Sec-CH-UA-Platform` and `Sec-CH-UA-Arch` Client Hints
and falls back to the `User-Agent` header. It responds with `Accept-CH` and
`Critical-CH`; supporting Chromium browsers may repeat the request
automatically with the architecture hint.

| Classification | Returned `platform` | Selected object |
| --- | --- | --- |
| Linux x86_64 | `linux-gnu` | `multimeter-x86_64-unknown-linux-gnu` |
| Linux x86_64 with `musl` or `alpine` UA marker | `linux-musl` | `multimeter-x86_64-unknown-linux-musl` |
| Windows x86_64 | `windows-x86` | `multimeter-x86_64-pc-windows-msvc.exe` |
| Windows ARM64 | `windows-arm` | `multimeter-aarch64-pc-windows-msvc.exe` |
| Other platforms or architectures | `mismatch` | None |

Linux libc is not exposed by standard browser UA or Client Hints, so ordinary
Linux x86_64 requests default to GNU. A recognized architecture hint is used
when present, except that an explicit 32-bit UA marker always forces
`mismatch`. Without a hint, the legacy UA must contain a supported 64-bit
architecture marker. ARM Linux, ChromeOS, Android, macOS, and other unsupported
platforms are also classified as `mismatch`.

Current Linux release assets are executable files rather than archives. The
selected Windows x86_64 asset is the self-contained static-DXC build; the
smaller `-no-static-dxc` variant is not selected automatically because GPU
compute testing depends on a runtime shader compiler.

**Response `200 OK`**

| Field | Type | Description |
| --- | --- | --- |
| `platform` | string | `linux-gnu`, `linux-musl`, `windows-x86`, `windows-arm`, or `mismatch`. |
| `downloadUrl` | string \| null | Signed URL for the selected executable; `null` when `platform` is `mismatch`. The URL expires according to `S3_GET_URL_TTL` (default `10m`). |

```json
{
  "platform": "linux-gnu",
  "downloadUrl": "https://s3.bitnp.net/clinic-executable/multimeter/latest/multimeter-x86_64-unknown-linux-gnu?X-Amz-Algorithm=..."
}
```

```json
{
  "platform": "mismatch",
  "downloadUrl": null
}
```

**Error responses:** `500` (download URL signing failed); `503` (the selected
executable is missing or cannot be read with the configured storage
credentials).

**curl**

```bash
curl "$BASE/api/downloads/stress-test-helper/download" \
  -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36'
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
`RECORD_ACTION_WINDOW`, `RECORD_PHOTO_MAX_COUNT`,
`RECORD_PHOTO_MAX_SIZE_BYTES`, the service-date reminder settings,
`MESSAGE_GATEWAY_*`, the `TAGGER_*` settings, `SESSION_*`, `CAS_*`, and
`KEYCLOAK_*`.
