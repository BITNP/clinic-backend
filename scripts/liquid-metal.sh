#!/usr/bin/env bash
# Query the liquid-metal tag set already registered by the backend.
set -Eeuo pipefail

usage() {
	cat <<'EOF'
Usage: scripts/liquid-metal.sh MODEL [EXTRA_TEXT]

Example:
  scripts/liquid-metal.sh '24年拯救者y9000p' 'RTX4060，未改装'

Environment:
  TAGGER_API_TOKEN   Bearer token; falls back to the backend's .env when empty
  TAGGER_URL         Default: http://127.0.0.1:4514
  LM_TAGGER_TAG_SET  Default: clinic_laptop_liquid_metal

Requires curl and jq. The backend must have registered the liquid-metal tag set.
Prints the raw response body to stdout; errors go to stderr. HTTP errors also
print their response body and return a nonzero exit status.
EOF
}

if [[ ${1:-} == '-h' || ${1:-} == '--help' ]]; then
	usage
	exit 0
fi
if (( $# < 1 || $# > 2 )); then
	usage >&2
	exit 2
fi
if [[ -z ${1//[[:space:]]/} ]]; then
	printf 'Error: MODEL must not be empty.\n' >&2
	exit 2
fi
for dependency in curl jq; do
	if ! command -v "$dependency" >/dev/null 2>&1; then
		printf 'Error: %s is required.\n' "$dependency" >&2
		exit 1
	fi
done
if [[ -z ${TAGGER_API_TOKEN:-} ]]; then
	root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
	if [[ -f "$root_dir/.env" ]]; then
		# Read only the token as data; never execute .env as shell code.
		while IFS= read -r line || [[ -n "$line" ]]; do
			if [[ $line =~ ^[[:space:]]*(export[[:space:]]+)?TAGGER_API_TOKEN[[:space:]]*=[[:space:]]*(.*)$ ]]; then
				token=${BASH_REMATCH[2]}
				if [[ $token =~ ^\"([^\"]*)\"[[:space:]]*(#.*)?$ || $token =~ ^\'([^\']*)\'[[:space:]]*(#.*)?$ ]]; then
					token=${BASH_REMATCH[1]}
				else
					# Unquoted inline comments start after whitespace.
					token=${token%%[[:space:]]#*}
					token=${token%"${token##*[![:space:]]}"}
				fi
				TAGGER_API_TOKEN=$token
			fi
		done < "$root_dir/.env"
	fi
fi
if [[ -z ${TAGGER_API_TOKEN:-} ]]; then
	printf 'Error: set TAGGER_API_TOKEN in the backend .env or export it.\n' >&2
	exit 1
fi

tagger_url=${TAGGER_URL:-http://127.0.0.1:4514}
payload=$(jq -cn \
	--arg name "${LM_TAGGER_TAG_SET:-clinic_laptop_liquid_metal}" \
	--arg model "$1" \
	--arg extra "${2:-}" '
	def trim: gsub("^\\s+|\\s+$"; "");
	($model | trim) as $model | ($extra | trim) as $extra |
	{name: $name, text: ("型号: " + $model +
		if $extra == "" then "" else "\n其他信息: " + $extra end)}
')

exec curl --silent --show-error --fail-with-body \
	--connect-timeout 5 --max-time 30 \
	--header "Authorization: Bearer ${TAGGER_API_TOKEN}" \
	--header 'Content-Type: application/json' \
	--data-binary "$payload" \
	"${tagger_url%/}/api/v1/agents/tagger/tag"
