#!/usr/bin/env bash
#
# Start the local development stack from the README's
# "Run locally (development)" section:
#
#   1. fake CAS server        -> http://127.0.0.1:9999
#   2. Redis                  -> 127.0.0.1:6379 (docker, falls back to redis-server)
#   3. RustFS + provisioning  -> http://127.0.0.1:9000, when S3 storage is local
#   4. message-gateway        -> http://127.0.0.1:8081 (with fake NapCat)
#   5. backend (this repo)    -> http://127.0.0.1:8080
#
# The backend runs in the foreground with its stdout/stderr untouched, so
# everything main.go logs (Gin routes, request logs, startup messages) stays
# in your terminal. Only the helpers (fake CAS, Redis, RustFS, message-gateway,
# and fake NapCat) are backgrounded. Ctrl-C stops the backend and
# the helpers this script started.
# RustFS and message-gateway are left running if they were already running before
# this script started.
#
# Usage: scripts/dev.sh [--no-cas] [--no-redis] [--no-storage] [--no-message-gateway] [--no-fake-napcat]

set -Eeuo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
MESSAGE_GATEWAY_DIR=${MESSAGE_GATEWAY_DIR:-$ROOT_DIR/../message-gateway}
cd "$ROOT_DIR"

# Export the local configuration to the backend and helper processes.
# In particular, the backend needs TAGGER_API_TOKEN to register its tag sets.
if [[ -f "$ROOT_DIR/.env" ]]; then
	set -a
	source "$ROOT_DIR/.env"
	set +a
fi

CAS_PORT=${CAS_PORT:-9999}
REDIS_PORT=${REDIS_PORT:-6379}
BACKEND_PORT=${BACKEND_PORT:-8080}
REDIS_CONTAINER=${REDIS_CONTAINER:-clinic-dev-redis}

# Helper output goes to files so the terminal stays clean for backend logs.
LOG_DIR=${LOG_DIR:-${TMPDIR:-/tmp}/clinic-dev}
CAS_LOG=$LOG_DIR/fake_cas.log
REDIS_LOG=$LOG_DIR/redis.log
STORAGE_LOG=$LOG_DIR/rustfs.log
MESSAGE_GATEWAY_LOG=$LOG_DIR/message-gateway.log
FAKE_NAPCAT_LOG=$LOG_DIR/fake-napcat.log
mkdir -p "$LOG_DIR"

START_CAS=1
START_REDIS=1
START_STORAGE=1
START_MESSAGE_GATEWAY=1
START_FAKE_NAPCAT=1
STORAGE_STARTED=0
FAKE_NAPCAT_STARTED=0
MESSAGE_GATEWAY_NETWORK_CREATED=0

usage() {
	cat <<'EOF'
Usage: scripts/dev.sh [options]

Loads and exports the backend's .env before starting services, if present.
Values in .env override existing environment variables.

Options:
  --no-cas        Do not start the fake CAS server (assume one is running)
  --no-redis      Do not start Redis (assume one is running)
  --no-storage    Do not start or provision local RustFS (assume configured
                  storage is already running, or use an external S3 endpoint)
  --no-message-gateway
                  Do not start message-gateway (assume one is running)
  --no-fake-napcat  Do not start the dev fake NapCat stack
  --fake-napcat   Start fake NapCat (the default); it does not connect to QQ
  --no-napcat     Deprecated alias for --no-message-gateway
  -h, --help      Show this help
EOF
}

while (($# > 0)); do
	case "$1" in
	--no-cas) START_CAS=0 ;;
	--no-redis) START_REDIS=0 ;;
	--no-storage | --no-rustfs) START_STORAGE=0 ;;
	--no-message-gateway | --no-napcat) START_MESSAGE_GATEWAY=0 ;;
	--fake-napcat) START_FAKE_NAPCAT=1 ;;
	--no-fake-napcat) START_FAKE_NAPCAT=0 ;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'Unknown option: %s\n\n' "$1" >&2
		usage >&2
		exit 1
		;;
	esac
	shift
done

log() { printf '\033[1;34m[dev]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[dev]\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[1;31m[dev]\033[0m %s\n' "$*" >&2
	exit 1
}

port_open() {
	local host=$1 port=$2
	(exec 3<>"/dev/tcp/$host/$port") 2>/dev/null
}

wait_for_port() {
	local host=$1 port=$2 label=$3 timeout=${4:-30} deadline=$((SECONDS + ${4:-30}))
	while ((SECONDS < deadline)); do
		port_open "$host" "$port" && return 0
		sleep 0.2
	done
	printf 'Timed out after %ss waiting for %s on %s:%s\n' "$timeout" "$label" "$host" "$port" >&2
	return 1
}

# --- background process bookkeeping ----------------------------------------

CAS_PGID=""
REDIS_CONTAINER_STARTED=0
REDIS_PGID=""
MESSAGE_GATEWAY_PGID=""

# Kill a whole process group (go run / redis-server spawn children).
kill_group() {
	local pgid=$1 sig=${2:-TERM}
	[[ -n "$pgid" ]] || return 0
	kill "-$sig" -- "-$pgid" 2>/dev/null || kill "-$sig" "$pgid" 2>/dev/null || true
}

cleanup() {
	local status=$?
	trap - EXIT INT TERM

	if [[ -n "$REDIS_PGID" ]]; then
		log "stopping local redis-server"
		kill_group "$REDIS_PGID"
	fi

	if ((REDIS_CONTAINER_STARTED)); then
		log "stopping redis container ($REDIS_CONTAINER)"
		docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
	fi

	if ((STORAGE_STARTED)); then
		log "stopping RustFS container"
		docker compose stop rustfs >/dev/null 2>&1 || true
	fi

	if ((FAKE_NAPCAT_STARTED)); then
		log "stopping fake NapCat stack"
		(cd "$MESSAGE_GATEWAY_DIR" &&
			docker compose -f docker-compose.fake-napcat.yml down) >/dev/null 2>&1 || true
	fi

	if [[ -n "$MESSAGE_GATEWAY_PGID" ]]; then
		log "stopping message-gateway"
		kill_group "$MESSAGE_GATEWAY_PGID"
	fi

	if [[ -n "$CAS_PGID" ]]; then
		log "stopping fake CAS server"
		kill_group "$CAS_PGID"
		kill_group "$CAS_PGID" KILL
	fi

	if ((MESSAGE_GATEWAY_NETWORK_CREATED)); then
		docker network rm message-gateway >/dev/null 2>&1 || true
	fi

	exit "$status"
}
trap cleanup EXIT INT TERM

# Start a command in its own process group so cleanup can take down its
# children too. Sets SPAWNED_PGID (never echo it: a command substitution would
# wait for EOF on the pipe, which the backgrounded child keeps open forever).
SPAWNED_PGID=""
spawn() {
	if command -v setsid >/dev/null 2>&1; then
		setsid "$@" &
	else
		"$@" &
	fi
	SPAWNED_PGID=$!
}

# --- 1. fake CAS server ------------------------------------------------------

start_cas() {
	if port_open 127.0.0.1 "$CAS_PORT"; then
		log "port $CAS_PORT already in use - reusing existing fake CAS"
		START_CAS=0
		return
	fi

	command -v go >/dev/null 2>&1 || die "go is not installed"
	[[ -f "$ROOT_DIR/fake/fake_cas.go" ]] || die "fake/fake_cas.go not found"

	log "starting fake CAS server on :$CAS_PORT (log: $CAS_LOG)"
	spawn go run fake/fake_cas.go >>"$CAS_LOG" 2>&1
	CAS_PGID=$SPAWNED_PGID

	if ! wait_for_port 127.0.0.1 "$CAS_PORT" "fake CAS" 60; then
		tail -n 20 "$CAS_LOG" >&2 || true
		die "fake CAS server did not come up; see $CAS_LOG"
	fi
	log "fake CAS ready (admin role)"
}

# --- 2. Redis ----------------------------------------------------------------

start_redis_container() {
	docker run --rm --name "$REDIS_CONTAINER" \
		-p "$REDIS_PORT:6379" redis:7-alpine >>"$REDIS_LOG" 2>&1 &
}

start_redis_direct() {
	redis-server --port "$REDIS_PORT" --save '' --appendonly no >>"$REDIS_LOG" 2>&1 &
	REDIS_PGID=$!
}

start_redis() {
	if port_open 127.0.0.1 "$REDIS_PORT"; then
		log "port $REDIS_PORT already in use - reusing existing Redis"
		START_REDIS=0
		return
	fi

	if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
		log "starting Redis container on :$REDIS_PORT (log: $REDIS_LOG)"
		REDIS_CONTAINER_STARTED=1
		# Pull first so a failing pull is reported clearly instead of looking
		# like a slow container start.
		docker image inspect redis:7-alpine >/dev/null 2>&1 ||
			docker pull redis:7-alpine >/dev/null || {
			warn "could not pull redis:7-alpine; falling back to local redis-server"
			REDIS_CONTAINER_STARTED=0
		}
		if ((REDIS_CONTAINER_STARTED)); then
			start_redis_container
		fi
	fi

	if ((!REDIS_CONTAINER_STARTED)); then
		command -v redis-server >/dev/null 2>&1 ||
			die "no Redis running on :$REDIS_PORT, docker unavailable and redis-server not installed"
		warn "docker unavailable - starting local redis-server instead"
		start_redis_direct
	fi

	if ! wait_for_port 127.0.0.1 "$REDIS_PORT" "Redis" 60; then
		tail -n 20 "$REDIS_LOG" >&2 || true
		die "Redis did not come up on :$REDIS_PORT; see $REDIS_LOG"
	fi
	log "Redis ready"
}

# --- 3. RustFS ---------------------------------------------------------------

storage_host() {
	local url=$1
	url=${url#*://}
	url=${url%%/*}
	url=${url%%:*}
	printf '%s' "$url"
}

start_storage() {
	[[ -n "${S3_ENDPOINT:-}" ]] || die "S3_ENDPOINT is not set; configure .env before starting"

	if ((!START_STORAGE)); then
		log "skipping local RustFS; using configured/remote S3"
		return
	fi

	local endpoint=${S3_INTERNAL_ENDPOINT:-$S3_ENDPOINT}
	local host
	host=$(storage_host "$endpoint")
	case "$host" in
	localhost | 127.0.0.1 | 0.0.0.0) ;;
	*)
		log "using external S3 at $host; skipping local RustFS"
		return
		;;
	esac

	command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 ||
		die "local storage is required but Docker is unavailable; start it first or use --no-storage"
	docker compose version >/dev/null 2>&1 ||
		die "docker compose is unavailable; start storage first or use --no-storage"

	local var
	for var in S3_BUCKET S3_REGION S3_ACCESS_KEY_ID S3_SECRET_ACCESS_KEY RUSTFS_ACCESS_KEY RUSTFS_SECRET_KEY; do
		[[ -n "${!var:-}" ]] || die "$var is not set in .env"
	done

	local was_running=0
	if docker compose ps -q rustfs 2>/dev/null | grep -q .; then
		was_running=1
	fi

	: >"$STORAGE_LOG"
	log "starting RustFS and provisioning storage (log: $STORAGE_LOG)"
	if ! docker compose up -d rustfs >>"$STORAGE_LOG" 2>&1; then
		tail -n 30 "$STORAGE_LOG" >&2 || true
		die "RustFS failed to start; see $STORAGE_LOG"
	fi
	if ((!was_running)); then
		STORAGE_STARTED=1
	fi

	if ! docker compose run --rm rustfs-init >>"$STORAGE_LOG" 2>&1; then
		tail -n 40 "$STORAGE_LOG" >&2 || true
		die "RustFS provisioning failed; see $STORAGE_LOG"
	fi
	log "storage ready"
}

# --- 4. message-gateway ------------------------------------------------------

start_message_gateway() {
	if ((!START_MESSAGE_GATEWAY)); then
		log "skipping message-gateway; using configured gateway"
		return
	fi

	export MESSAGE_GATEWAY_URL=${MESSAGE_GATEWAY_URL:-http://127.0.0.1:8081}
	if port_open 127.0.0.1 8081; then
		log "port 8081 already in use - reusing existing message-gateway"
		return
	fi

	command -v go >/dev/null 2>&1 || die "go is not installed"
	[[ -f "$MESSAGE_GATEWAY_DIR/go.mod" ]] ||
		die "message-gateway Go module not found at $MESSAGE_GATEWAY_DIR"
	[[ -f "$MESSAGE_GATEWAY_DIR/.env" ]] ||
		die "message-gateway .env not found at $MESSAGE_GATEWAY_DIR"
	[[ -f "$MESSAGE_GATEWAY_DIR/accounts.json" ]] ||
		die "message-gateway accounts.json not found at $MESSAGE_GATEWAY_DIR"

	: >"$MESSAGE_GATEWAY_LOG"
	log "starting message-gateway with go run (log: $MESSAGE_GATEWAY_LOG)"
	spawn bash -c '
		cd "$1" || exit 1
		set -a
		source .env
		set +a
		export HTTP_ADDR=${HTTP_ADDR:-:8081}
		export MESSAGE_ACCOUNTS_FILE=./accounts.json
		exec go run ./cmd/message-gateway
	' bash "$MESSAGE_GATEWAY_DIR" >>"$MESSAGE_GATEWAY_LOG" 2>&1
	MESSAGE_GATEWAY_PGID=$SPAWNED_PGID

	if ! wait_for_port 127.0.0.1 8081 "message-gateway" 60; then
		tail -n 30 "$MESSAGE_GATEWAY_LOG" >&2 || true
		die "message-gateway failed to start; see $MESSAGE_GATEWAY_LOG"
	fi
	log "message-gateway ready: http://127.0.0.1:8081"
	if ((!START_FAKE_NAPCAT)); then
		log "NapCat is external; sample stack: (cd $MESSAGE_GATEWAY_DIR && docker compose -f docker-compose.napcat.yml up -d)"
		log "NapCat WebUI: http://127.0.0.1:6099/webui (first run: scan the QR code)"
	fi
}

start_fake_napcat() {
	if ((!START_FAKE_NAPCAT)); then
		return
	fi

	if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
		warn "Docker is unavailable - not starting fake NapCat"
		return
	fi
	if ! docker compose version >/dev/null 2>&1; then
		warn "docker compose is unavailable - not starting fake NapCat"
		return
	fi

	[[ -f "$MESSAGE_GATEWAY_DIR/docker-compose.fake-napcat.yml" ]] ||
		die "fake NapCat compose file not found at $MESSAGE_GATEWAY_DIR"

	if (cd "$MESSAGE_GATEWAY_DIR" && docker compose -f docker-compose.fake-napcat.yml ps -q napcat 2>/dev/null | grep -q .); then
		log "fake NapCat already running - reusing it"
		return
	fi

	: >"$FAKE_NAPCAT_LOG"
	if ! docker network inspect message-gateway >/dev/null 2>&1; then
		log "creating Docker network message-gateway for fake NapCat"
		if ! docker network create message-gateway >>"$FAKE_NAPCAT_LOG" 2>&1; then
			tail -n 30 "$FAKE_NAPCAT_LOG" >&2 || true
			die "failed to create the message-gateway Docker network; see $FAKE_NAPCAT_LOG"
		fi
		MESSAGE_GATEWAY_NETWORK_CREATED=1
	fi

	log "starting fake NapCat (log: $FAKE_NAPCAT_LOG)"
	if ! (cd "$MESSAGE_GATEWAY_DIR" && docker compose -f docker-compose.fake-napcat.yml up -d --build) >>"$FAKE_NAPCAT_LOG" 2>&1; then
		tail -n 30 "$FAKE_NAPCAT_LOG" >&2 || true
		die "fake NapCat failed to start; see $FAKE_NAPCAT_LOG"
	fi
	FAKE_NAPCAT_STARTED=1

	NAPCAT_ONEBOT_PORT=$(sed -n 's/^NAPCAT_ONEBOT_PORT=//p' "$MESSAGE_GATEWAY_DIR/.env" | head -n 1)
	NAPCAT_ONEBOT_PORT=${NAPCAT_ONEBOT_PORT:-3001}
	if ! wait_for_port 127.0.0.1 "$NAPCAT_ONEBOT_PORT" "fake NapCat" 30; then
		warn "fake NapCat is still starting; follow it with: (cd $MESSAGE_GATEWAY_DIR && docker compose -f docker-compose.fake-napcat.yml logs -f napcat)"
		return
	fi
	log "fake NapCat ready: http://127.0.0.1:$NAPCAT_ONEBOT_PORT"
	log "fake NapCat messages: curl http://127.0.0.1:$NAPCAT_ONEBOT_PORT/_messages"
}

# --- 5. backend --------------------------------------------------------------

start_backend() {
	if port_open 127.0.0.1 "$BACKEND_PORT"; then
		die "port $BACKEND_PORT is already in use; stop the other backend first"
	fi

	# Outbound messaging goes through message-gateway; keep the legacy manager off.
	export NAPCAT_BASE_URL=
	export CLINIC_API_KEY=${CLINIC_API_KEY:-local-dev-key}
	export REDIS_ADDR=${REDIS_ADDR:-127.0.0.1:$REDIS_PORT}
	export REDIS_PASSWORD=${REDIS_PASSWORD:-}
	export REDIS_DB=${REDIS_DB:-0}
	export CAS_SERVER_URL=${CAS_SERVER_URL:-http://127.0.0.1:$CAS_PORT}
	export APP_BASE_URL=${APP_BASE_URL:-http://127.0.0.1:5173}
	export CAS_DEFAULT_REDIRECT=${CAS_DEFAULT_REDIRECT:-/}
	export SESSION_COOKIE_SAMESITE=${SESSION_COOKIE_SAMESITE:-lax}
	export STAFF_VERSION=${STAFF_VERSION:-0}

	log "starting backend on :$BACKEND_PORT (Ctrl-C to stop)"

	# Foreground, no redirection: backend output goes straight to the terminal.
	go run .
}

[[ -n "${S3_ENDPOINT:-}" ]] || die "S3_ENDPOINT is not set; configure .env before starting"
if port_open 127.0.0.1 "$BACKEND_PORT"; then
	die "port $BACKEND_PORT is already in use; stop the other backend first"
fi

((START_CAS)) && start_cas
((START_REDIS)) && start_redis
start_storage
start_message_gateway
start_fake_napcat
start_backend
