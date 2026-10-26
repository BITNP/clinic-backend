# RustFS object storage

This directory contains the deployment assets for the clinic photo store:
RustFS itself (S3-compatible object storage) and the one-shot bucket/user
provisioner.

Both compose files include RustFS. No public proxy is bundled: RustFS is
reachable at `rustfs:9000` on the Compose network, and the S3 API and console
are bound to loopback ports on the host.

## What is provisioned

`deploy/rustfs/init.sh` runs as the one-shot `rustfs-init` compose service. It:

- creates the private `clinic-photos` bucket if it does not exist;
- creates zero-byte markers for the reserved `temporary/` and `photos/`
  prefixes;
- creates/updates the `clinic-photo-app` IAM policy;
- creates/updates the application IAM user from `S3_ACCESS_KEY_ID` /
  `S3_SECRET_ACCESS_KEY`, and attaches only that policy.

The policy allows the application user to list the photo bucket and read,
write, and delete objects only under the reserved `temporary/` and `photos/`
prefixes. It does not allow creating or deleting buckets, changing bucket
policies, writing outside those prefixes, or accessing any other bucket. It
never uses the RustFS root account.

The bucket is private. No anonymous access rule is created. `temporary/` is
reserved for uploads that the backend still has to validate; `photos/` is for
final photos. No lifecycle rule deletes final photos automatically.

## Files

| File | Purpose |
| --- | --- |
| `init.sh` | Idempotent bucket, prefix, policy, and application-user provisioning. |

## Environment

Copy `.env.example` to `.env` and fill in the RustFS values. Keep `.env` out of
source control and readable by the deployment user only:

```bash
chmod 600 .env
```

Required for storage:

| Variable | Purpose |
| --- | --- |
| `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` | RustFS root credentials. Used only by `rustfs-init` and break-glass administration. |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | Dedicated application credentials. The backend uses these. |
| `S3_BUCKET` | Bucket name; production `clinic-photos`, local example `clinic-photos-dev`. |
| `S3_REGION` | Bucket/S3 region, default `us-east-1`. |
| `S3_ENDPOINT` | Browser-facing endpoint used to sign upload/download URLs. Set this to the endpoint browsers will actually use, or leave the local development value if browser uploads are not exposed yet. |
| `S3_INTERNAL_ENDPOINT` | Optional backend-only endpoint. Empty uses `S3_ENDPOINT`; Compose sets `http://rustfs:9000`, and a host-run backend can use `http://127.0.0.1:9000`. |
| `S3_CORS_ALLOWED_ORIGINS` | Comma-separated exact frontend origins. Never use `*` in production. |
| `S3_API_PORT` | Host loopback port for the S3 API, default `9000`. |
| `RUSTFS_CONSOLE_PORT` | Host loopback port for the console, default `9001`. |

Generate credentials with strong random values. Access keys must not contain
`/`; uppercase letters and digits are recommended:

```bash
openssl rand -hex 16 | tr '[:lower:]' '[:upper:]'   # access key
openssl rand -hex 24                                 # secret key
```

## Production setup

1. Set the RustFS root credentials, application credentials, CORS origins, and
   `S3_ENDPOINT` in `.env`. The bucket and application user must be provisioned
   before the backend starts.
2. Load the packaged images and start the stack:

   ```bash
   docker load -i clinic-images.tar
   docker compose -f docker-compose.prod.yml up -d
   ```

3. Check the one-shot provisioner and the services:

   ```bash
   docker compose -f docker-compose.prod.yml ps
   docker compose -f docker-compose.prod.yml logs rustfs-init
   docker compose -f docker-compose.prod.yml logs -f rustfs backend
   ```

   `backend` waits for `rustfs-init` and then verifies bucket access with
   `HeadBucket` before accepting HTTP requests. The application policy grants
   `s3:ListBucket` so this startup check succeeds.

The RustFS console and S3 API listen on loopback only:

- S3 API: `http://127.0.0.1:${S3_API_PORT:-9000}`
- Console: `http://127.0.0.1:${RUSTFS_CONSOLE_PORT:-9001}`

Reach the console through an SSH tunnel:

```bash
ssh -L 9001:127.0.0.1:9001 user@server
# then open http://127.0.0.1:9001
```

The backend reaches RustFS internally at `http://rustfs:9000`. Browser-facing
access is not provided by this stack. When an external endpoint is available,
set `S3_ENDPOINT` to it so presigned URLs use the right host; that
endpoint must preserve Host/path/query for SigV4.

This deployment uses path-style S3 addressing: presigned URLs look like
`https://<public-endpoint>/clinic-photos/photos/<key>`. The backend S3 client
must therefore be configured with path-style bucket lookup
(`usePathStyle=true` in the AWS SDK). Virtual-host style
(`clinic-photos.<public-endpoint>`) is not enabled; keep
`RUSTFS_SERVER_DOMAINS` unset unless you add wildcard DNS and a wildcard
certificate.

## Development

`docker-compose.yml` exposes the S3 API and console on loopback:

- S3 API: `http://127.0.0.1:${S3_API_PORT:-9000}`
- Console: `http://127.0.0.1:${RUSTFS_CONSOLE_PORT:-9001}`

Containers use `http://rustfs:9000` on the compose network, and Compose sets
that as the backend's `S3_INTERNAL_ENDPOINT`. `rustfs-init` runs automatically
with the stack. If the backend runs with `go run .` on the host, set
`S3_INTERNAL_ENDPOINT=http://127.0.0.1:9000` or leave it empty to use
`S3_ENDPOINT`.

## CORS

RustFS receives `S3_CORS_ALLOWED_ORIGINS` as `RUSTFS_CORS_ALLOWED_ORIGINS` and
answers browser preflights for the configured origins. Use exact origins,
including scheme and port. CORS only controls which browser origins may read
responses; it does not make the bucket public and does not replace signed
authorization.

The backend should presign browser uploads and downloads with
`S3_ENDPOINT`. A browser `PUT` sends the bytes directly to RustFS; include
any headers that were part of the signature (for example `Content-Type`). No
upload-body limit is enforced by this stack; enforce one in the backend or
deployment infrastructure.

## Verify the deployment

Run an interactive client inside the compose network. It has the root and
application credentials from `.env`:

```bash
docker compose -f docker-compose.prod.yml run --rm -it \
  --entrypoint /bin/sh -e HOME=/tmp/rc-home rustfs-init
```

Inside the container:

```bash
rc alias set verify "$RUSTFS_INTERNAL_ENDPOINT" \
  "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY"

# upload, inspect, list, download
printf 'rustfs verification\n' | rc pipe "verify/$S3_BUCKET/temporary/verify.txt"
printf 'private object\n' | rc pipe "verify/$S3_BUCKET/photos/private.txt"
rc object stat "verify/$S3_BUCKET/temporary/verify.txt"
rc bucket list "verify/$S3_BUCKET/temporary/"
rc object copy "verify/$S3_BUCKET/temporary/verify.txt" /tmp/verify.txt
cat /tmp/verify.txt
rc object remove -f "verify/$S3_BUCKET/temporary/verify.txt"
```

The application user must not be able to escape the photo bucket:

```bash
rc bucket create "verify/not-allowed"   # expect AccessDenied
printf 'x' | rc pipe "verify/$S3_BUCKET/outside-prefix.txt"  # expect AccessDenied
rc bucket list "verify/"                # expect only clinic-photos (or AccessDenied)
```

Exit the in-network client container. The remaining checks run on the host and
assume the `rc` CLI is installed. If it is not, run the `rc` commands in this
one-off container and keep the `curl` / `docker compose` commands in a host
shell:

```bash
docker run --rm -it --network host --env-file .env \
  -e HOME=/tmp/rc-home --entrypoint /bin/sh \
  rustfs/rc:v0.1.36
```

On the host:

```bash
set -a
source .env
set +a

local_endpoint=${S3_INTERNAL_ENDPOINT:-http://127.0.0.1:${S3_API_PORT:-9000}}
rc alias set local "$local_endpoint" \
  "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY"
```

Confirm anonymous reads fail against the object uploaded above. Expect `403` or
`404`, never `200`:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' \
  "http://127.0.0.1:${S3_API_PORT:-9000}/$S3_BUCKET/photos/private.txt"
```

Confirm CORS for an allowed origin. Expect an
`Access-Control-Allow-Origin` header echoing the origin and `PUT` in the
allowed methods:

```bash
curl -sS -D - -o /dev/null -X OPTIONS \
  -H 'Origin: https://admin.example.com' \
  -H 'Access-Control-Request-Method: PUT' \
  -H 'Access-Control-Request-Headers: content-type' \
  "http://127.0.0.1:${S3_API_PORT:-9000}/$S3_BUCKET/temporary/verify.txt"
```

Verify a temporary signed upload and download URL through
`S3_ENDPOINT`. If that endpoint is not reachable from the server, use
the loopback S3 endpoint instead:

```bash
public_endpoint=${S3_ENDPOINT:-http://127.0.0.1:${S3_API_PORT:-9000}}
rc alias set public "$public_endpoint" \
  "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY"

printf 'signed upload\n' | rc pipe "public/$S3_BUCKET/temporary/signed.txt"
url=$(rc object share "public/$S3_BUCKET/temporary/signed.txt" --expire 5m |
  grep -E '^https?://' | head -1)
curl -fsS "$url"
rc object remove -f "public/$S3_BUCKET/temporary/signed.txt"
```

Verify files survive a container restart:

```bash
printf 'persistent\n' | rc pipe "public/$S3_BUCKET/temporary/persistent.txt"
docker compose -f docker-compose.prod.yml restart rustfs
# wait for /health/ready
docker compose -f docker-compose.prod.yml exec rustfs \
  curl -fsS http://127.0.0.1:9000/health/ready
rc object stat "public/$S3_BUCKET/temporary/persistent.txt"
```

The object is stored in the `rustfsdata` volume and must still exist. Remove the
verification objects when finished:

```bash
rc object remove -f "public/$S3_BUCKET/photos/private.txt"
rc object remove -f "public/$S3_BUCKET/temporary/persistent.txt"
```

## Credential rotation

### Application credentials

Rotate the application secret without changing the access key:

```bash
# Inside the rustfs-init shell with root credentials:
rc alias set admin "$RUSTFS_INTERNAL_ENDPOINT" \
  "$RUSTFS_ACCESS_KEY" "$RUSTFS_SECRET_KEY"

export NEW_S3_SECRET='<new-secret>'
rc admin user passwd admin "$S3_ACCESS_KEY_ID" \
  --password-from-env NEW_S3_SECRET
unset NEW_S3_SECRET
```

Update `S3_SECRET_ACCESS_KEY` in `.env` in the same operation, then restart the
backend and confirm the new secret works while the old one fails. Keep `.env`
in sync: a later `rustfs-init` run re-applies the secret from `.env`.

To rotate the access key too:

1. Generate new `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` values.
2. Update `.env`.
3. Re-run provisioning and restart the backend:

   ```bash
   docker compose -f docker-compose.prod.yml up -d rustfs-init backend
   ```

4. Remove the old application user after the new one is verified. In the
   `rustfs-init` shell, with the `admin` alias configured as above:

   ```bash
   rc admin user rm admin <old-access-key>
   ```

Presigned URLs signed before the rotation stop working when their key is
removed; generate new URLs after the backend restart.

### Root credentials

1. Generate a new root pair and update `RUSTFS_ACCESS_KEY` /
   `RUSTFS_SECRET_KEY` in `.env`.
2. Recreate the RustFS container. This is a short single-node restart:

   ```bash
   docker compose -f docker-compose.prod.yml up -d rustfs
   ```

3. Wait for `GET /health/ready` and verify the console login with the new root
   credentials.
4. Confirm the old root pair is rejected.
5. Update any break-glass records. Re-run `rustfs-init` only if you also want to
   re-apply the bucket policy/user.

Do not keep both root pairs configured. For a multi-node cluster, set an
explicit `RUSTFS_RPC_SECRET` before rotating root credentials; this single-node
deployment does not need it.

## Backup and restore

The authoritative copy is the `rustfsdata` Docker volume. There are two useful
backup strategies:

### Object-level copy while running

Use `rc mirror` to copy the bucket to another S3 endpoint or a local directory.
With the `public` alias configured as in the verification section, for example:

```bash
rc mirror public/clinic-photos /backup/clinic-photos
```

This is convenient for offsite copies, but it does not back up IAM state. The
IAM state can be recreated by `rustfs-init` and the policy in this repository.

### Volume snapshot (recommended before upgrades)

Stop RustFS first so the on-disk metadata is consistent. Find the volume name
with `docker volume ls | grep rustfsdata`; with the default project name it is
`clinic-backend_rustfsdata`. The already-packaged `redis:7-alpine` image is used
as a small helper because it contains `tar`:

```bash
docker compose -f docker-compose.prod.yml stop rustfs

docker run --rm \
  -v clinic-backend_rustfsdata:/data:ro \
  -v "$PWD:/backup" \
  redis:7-alpine \
  tar czf "/backup/rustfs-data-$(date +%F).tar.gz" -C /data .

docker compose -f docker-compose.prod.yml start rustfs
```

Restore is destructive. Stop the stack, recreate an empty volume, extract the
archive into it, then start again:

```bash
docker compose -f docker-compose.prod.yml down
docker volume rm clinic-backend_rustfsdata
docker volume create clinic-backend_rustfsdata

docker run --rm \
  -v clinic-backend_rustfsdata:/data \
  -v "$PWD:/backup" \
  redis:7-alpine \
  sh -c 'tar xzf /backup/rustfs-data-YYYY-MM-DD.tar.gz -C /data && chown -R 10001:10001 /data'

docker compose -f docker-compose.prod.yml up -d
```

After a restore, run `docker compose -f docker-compose.prod.yml logs rustfs-init`
and verify an object and an anonymous read. Keep at least one recent archive
outside the server, and test a restore periodically.

## Upgrades

1. Read the RustFS release notes and take a volume snapshot.
2. Set `RUSTFS_IMAGE_TAG` in `.env` to the new version.
3. Package/pull the new image and load it on the server.
4. Recreate only RustFS:

   ```bash
   docker compose -f docker-compose.prod.yml up -d rustfs
   ```

5. Wait for `/health/ready`, run `rustfs-init`, and repeat the verification
   steps. `RUSTFS_CLI_IMAGE_TAG` can be upgraded independently.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `permission denied` writing `/data` | Use the named `rustfsdata` volume. For a bind mount, `chown -R 10001:10001` the host directory. |
| `403 SignatureDoesNotMatch` | The endpoint used to sign must match the URL being requested, including scheme, host, port, and query string. Check server clock skew. |
| No CORS headers | The origin must exactly match `S3_CORS_ALLOWED_ORIGINS`; restart `rustfs` after changing `.env`; wildcards do not work with credentialed requests. |
| Console not reachable | It is bound to `127.0.0.1`; use the SSH tunnel. |
| `rustfs-init` failed | `docker compose ... logs rustfs-init`; verify root credentials and rerun the service. |

## Offline packaging

There is no packaging helper script. Pull and save the production images,
including RustFS and the `rc` provisioner, as shown in the root
[README](../../README.md#deploy-to-a-server):

```bash
docker pull rustfs/rustfs:${RUSTFS_IMAGE_TAG:-latest}
docker pull rustfs/rc:${RUSTFS_CLI_IMAGE_TAG:-v0.1.36}
docker save -o clinic-images.tar \
  ghcr.io/bitnp/clinic-backend:${BACKEND_IMAGE_TAG:-latest} \
  ghcr.io/bitnp/clinic_admin_frontend:${ADMIN_FRONTEND_IMAGE_TAG:-latest} \
  redis:7-alpine \
  rustfs/rustfs:${RUSTFS_IMAGE_TAG:-latest} \
  rustfs/rc:${RUSTFS_CLI_IMAGE_TAG:-v0.1.36}
sha256sum clinic-images.tar > clinic-images.tar.sha256
```

Copy the tarball, `docker-compose.prod.yml`, `.env`, and `deploy/rustfs/` to the
server, then `docker load -i clinic-images.tar`. Every bundled service in the
production compose file uses `pull_policy: never`, so the server never needs
registry access.
