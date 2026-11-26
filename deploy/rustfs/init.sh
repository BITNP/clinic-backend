#!/bin/sh
#
# One-shot RustFS provisioning for clinic object storage.
#
# Creates private photo and executable buckets, scoped application policies, and
# the required application IAM users. This script is idempotent and runs as the
# `rustfs-init` compose service with the root credentials. The backend only ever
# receives application credentials.
#
# Required environment:
#   RUSTFS_INTERNAL_ENDPOINT  e.g. http://rustfs:9000
#   RUSTFS_ACCESS_KEY         root access key (provisioning only)
#   RUSTFS_SECRET_KEY         root secret key (provisioning only)
#   S3_ACCESS_KEY_ID          application access key
#   S3_SECRET_ACCESS_KEY      application secret key
#   S3_BUCKET                 bucket name, e.g. clinic-photos
#   MULTIMETER_S3_BUCKET      executable bucket, defaults to clinic-executable
#   MULTIMETER_S3_ACCESS_KEY_ID / MULTIMETER_S3_SECRET_ACCESS_KEY
#                             optional dedicated read credentials; S3_* creds
#                             are used when both are empty

set -eu
umask 077

: "${RUSTFS_INTERNAL_ENDPOINT:?RUSTFS_INTERNAL_ENDPOINT is required}"
: "${RUSTFS_ACCESS_KEY:?RUSTFS_ACCESS_KEY is required}"
: "${RUSTFS_SECRET_KEY:?RUSTFS_SECRET_KEY is required}"
: "${S3_ACCESS_KEY_ID:?S3_ACCESS_KEY_ID is required}"
: "${S3_SECRET_ACCESS_KEY:?S3_SECRET_ACCESS_KEY is required}"
: "${S3_BUCKET:?S3_BUCKET is required}"
: "${MULTIMETER_S3_BUCKET:=clinic-executable}"

if [ -n "${MULTIMETER_S3_ACCESS_KEY_ID:-}" ] || [ -n "${MULTIMETER_S3_SECRET_ACCESS_KEY:-}" ]; then
	: "${MULTIMETER_S3_ACCESS_KEY_ID:?MULTIMETER_S3_ACCESS_KEY_ID is required when a multimeter credential is set}"
	: "${MULTIMETER_S3_SECRET_ACCESS_KEY:?MULTIMETER_S3_SECRET_ACCESS_KEY is required when a multimeter credential is set}"
	MULTIMETER_USER=$MULTIMETER_S3_ACCESS_KEY_ID
	MULTIMETER_SECRET=$MULTIMETER_S3_SECRET_ACCESS_KEY
else
	MULTIMETER_USER=$S3_ACCESS_KEY_ID
	MULTIMETER_SECRET=$S3_SECRET_ACCESS_KEY
fi

ALIAS=clinic-rustfs
PHOTO_POLICY_NAME=clinic-photo-app
PHOTO_POLICY_FILE=/tmp/clinic-photo-app-policy.json
MULTIMETER_POLICY_NAME=clinic-multimeter-reader
MULTIMETER_POLICY_FILE=/tmp/clinic-multimeter-reader-policy.json

# Keep the rc config away from the image's read-only home directory.
HOME=/tmp/rc-home
export HOME
mkdir -p "$HOME"

log() { printf '[rustfs-init] %s\n' "$*"; }

rc alias set "$ALIAS" "$RUSTFS_INTERNAL_ENDPOINT" \
	"$RUSTFS_ACCESS_KEY" "$RUSTFS_SECRET_KEY" >/dev/null

# The compose health check normally guarantees readiness; retry briefly anyway
# so the provisioner can also be run by hand against a still-starting server.
tries=0
until rc bucket list "$ALIAS/" >/dev/null 2>&1; do
	tries=$((tries + 1))
	if [ "$tries" -ge 30 ]; then
		log "RustFS did not become ready at $RUSTFS_INTERNAL_ENDPOINT" >&2
		exit 1
	fi
	sleep 2
done

log "creating private bucket $S3_BUCKET (if missing)"
rc bucket create --ignore-existing "$ALIAS/$S3_BUCKET"

# Reserve the two prefixes used by the future upload workflow. S3 prefixes do
# not have standalone existence, so a zero-byte marker keeps them visible in
# the console. No lifecycle rule is created for either prefix; final photos
# under photos/ are never deleted automatically.
for prefix in temporary photos; do
	printf '' | rc pipe "$ALIAS/$S3_BUCKET/$prefix/" \
		--content-type application/x-directory >/dev/null
done

cat >"$PHOTO_POLICY_FILE" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ListPhotoBucket",
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketLocation",
        "s3:ListBucket",
        "s3:ListBucketMultipartUploads"
      ],
      "Resource": ["arn:aws:s3:::$S3_BUCKET"]
    },
    {
      "Sid": "ReadWritePhotoObjects",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:GetObjectVersion",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts"
      ],
      "Resource": [
        "arn:aws:s3:::$S3_BUCKET/temporary/*",
        "arn:aws:s3:::$S3_BUCKET/photos/*"
      ]
    }
  ]
}
EOF

log "creating/updating application user $S3_ACCESS_KEY_ID"
rc admin user add "$ALIAS" "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY"

# Detach before replacing the policy so a credential rotation or policy edit
# cannot leave a stale binding behind. The app user is dedicated to this policy.
rc admin policy detach "$ALIAS" "$PHOTO_POLICY_NAME" --user "$S3_ACCESS_KEY_ID" >/dev/null 2>&1 || true

log "creating/updating application policy $PHOTO_POLICY_NAME"
rc admin policy create "$ALIAS" "$PHOTO_POLICY_NAME" "$PHOTO_POLICY_FILE"

rc admin policy attach "$ALIAS" "$PHOTO_POLICY_NAME" --user "$S3_ACCESS_KEY_ID" >/dev/null

log "creating private bucket $MULTIMETER_S3_BUCKET (if missing)"
rc bucket create --ignore-existing "$ALIAS/$MULTIMETER_S3_BUCKET"
printf '' | rc pipe "$ALIAS/$MULTIMETER_S3_BUCKET/multimeter/latest/" \
	--content-type application/x-directory >/dev/null

cat >"$MULTIMETER_POLICY_FILE" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ListExecutableBucket",
      "Effect": "Allow",
      "Action": [
        "s3:GetBucketLocation",
        "s3:ListBucket"
      ],
      "Resource": ["arn:aws:s3:::$MULTIMETER_S3_BUCKET"]
    },
    {
      "Sid": "ReadExecutableObjects",
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:GetObjectVersion"
      ],
      "Resource": ["arn:aws:s3:::$MULTIMETER_S3_BUCKET/multimeter/latest/*"]
    }
  ]
}
EOF

if [ "$MULTIMETER_USER" != "$S3_ACCESS_KEY_ID" ]; then
	# Drop any old fallback binding from the photo application user.
	rc admin policy detach "$ALIAS" "$MULTIMETER_POLICY_NAME" --user "$S3_ACCESS_KEY_ID" >/dev/null 2>&1 || true
	log "creating/updating multimeter reader user $MULTIMETER_USER"
	rc admin user add "$ALIAS" "$MULTIMETER_USER" "$MULTIMETER_SECRET"
else
	log "using photo application user $MULTIMETER_USER for multimeter reads"
fi

rc admin policy detach "$ALIAS" "$MULTIMETER_POLICY_NAME" --user "$MULTIMETER_USER" >/dev/null 2>&1 || true

log "creating/updating application policy $MULTIMETER_POLICY_NAME"
rc admin policy create "$ALIAS" "$MULTIMETER_POLICY_NAME" "$MULTIMETER_POLICY_FILE"
if [ "$MULTIMETER_USER" = "$S3_ACCESS_KEY_ID" ]; then
	rc admin policy attach "$ALIAS" "$PHOTO_POLICY_NAME,$MULTIMETER_POLICY_NAME" --user "$MULTIMETER_USER" >/dev/null
else
	rc admin policy attach "$ALIAS" "$MULTIMETER_POLICY_NAME" --user "$MULTIMETER_USER" >/dev/null
fi

log "provisioning complete"
