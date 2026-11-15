#!/usr/bin/env bash
# check-proxy-logs asserts that a signed media credential never reaches a log,
# and that the request which carried it really did produce a redacted line.
#
# The three signed endpoints carry their credential in the path:
#   /api/admin/v1/uploads/staging/{token}   upload staging PUT
#   /api/admin/v1/media-preview/{token}     private preview read
#   /api/web/v1/media/{token}               guest playback read
# Both consoles proxy the whole /api/ tree, so each of the three can be reached
# through either entry: six combinations, all checked.
#
# For every combination the script asserts three things:
#   1. the synthetic value is absent from the application log and from both
#      proxy logs;
#   2. the target proxy gained a line for that route, so the request did not
#      bypass it and its log did reach the container output;
#   3. the application logged the same request under the redacted route and the
#      identity sent in the request, so the redacted line belongs to this call.
#
# The value is invented here: no real signature, no real credential, no object
# is touched. The Go test covers the filter; this covers the proxy layers.
#
# Usage:
#   scripts/check-proxy-logs.sh [admin-port] [web-port] [server-container] [admin-container] [web-container]
#
# Defaults match deploy/compose.yaml (the delivery stack). The acceptance stack
# uses other ports:
#   scripts/check-proxy-logs.sh 18091 18092 \
#     openduanju-baccept-server-1 openduanju-baccept-admin-1 openduanju-baccept-web-1
set -uo pipefail

ADMIN_PORT="${1:-18081}"
WEB_PORT="${2:-18082}"
SERVER_CONTAINER="${3:-openduanju-rebuild-server-1}"
ADMIN_CONTAINER="${4:-openduanju-rebuild-admin-1}"
WEB_CONTAINER="${5:-openduanju-rebuild-web-1}"

failures=0
run_id="$(date +%s)-$$"

fail() {
  echo "FAIL $*" >&2
  failures=$((failures + 1))
}

log_of() {
  docker logs --since 300s "$1" 2>&1
}

count_in() {
  # count_in <container> <fixed string>; a missing container counts as 0 and is
  # reported by its own check.
  log_of "$1" | grep -cF -- "$2" || true
}

for container in "$SERVER_CONTAINER" "$ADMIN_CONTAINER" "$WEB_CONTAINER"; do
  if ! docker inspect "$container" >/dev/null 2>&1; then
    fail "container $container is not running"
  fi
done
if [ "$failures" -ne 0 ]; then
  echo "FAILED: ${failures} check(s)" >&2
  exit 1
fi

# route <name> prints the request path prefix, the redacted route the log must
# carry, and the file the local driver serves it from.
route_path() {
  case "$1" in
    staging)  echo "/api/admin/v1/uploads/staging" ;;
    preview)  echo "/api/admin/v1/media-preview" ;;
    playback) echo "/api/web/v1/media" ;;
  esac
}

check_combination() {
  local entry="$1" route="$2"
  local port container prefix sentinel marker status
  local proxy_before proxy_after proxy_delta
  local server_line

  if [ "$entry" = "admin" ]; then
    port="$ADMIN_PORT"; container="$ADMIN_CONTAINER"
  else
    port="$WEB_PORT"; container="$WEB_CONTAINER"
  fi
  prefix="$(route_path "$route")"
  sentinel="synthetic-signed-credential-${run_id}-${entry}-${route}"
  # A 32 character identity: letters and digits, which the server accepts as a
  # caller supplied trace.
  marker="obs003$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"

  proxy_before="$(count_in "$container" " $prefix/{token} ")"
  status="$(curl -s -o /dev/null -w '%{http_code}' \
    -H "X-Trace-Id: ${marker}" \
    "http://127.0.0.1:${port}${prefix}/${sentinel}" || true)"

  if [ -z "$status" ] || [ "$status" = "000" ]; then
    fail "${entry}/${route}: unreachable through port ${port}"
    return
  fi

  sleep 1

  # 1. The value must not be anywhere.
  local leak_container leak_count
  for leak_container in "$SERVER_CONTAINER" "$ADMIN_CONTAINER" "$WEB_CONTAINER"; do
    leak_count="$(count_in "$leak_container" "$sentinel")"
    if [ "$leak_count" != "0" ]; then
      fail "${entry}/${route}: ${leak_container} logged the signed value ${leak_count} time(s)"
      log_of "$leak_container" | grep -F -- "$sentinel" | head -2 >&2
    fi
  done

  # 2. The request reached this proxy and this proxy logged the route.
  proxy_after="$(count_in "$container" " $prefix/{token} ")"
  proxy_delta=$((proxy_after - proxy_before))
  if [ "$proxy_delta" -lt 1 ]; then
    fail "${entry}/${route}: ${container} logged no redacted line for ${prefix}/{token} (delta ${proxy_delta})"
    return
  fi

  # 3. The application logged this request under the redacted route and the
  #    identity the request carried.
  server_line="$(log_of "$SERVER_CONTAINER" | grep -F "trace_id=${marker}" | grep -F "path=${prefix}/{token}")"
  if [ -z "$server_line" ]; then
    fail "${entry}/${route}: no application line with the redacted route and the request identity"
    return
  fi

  echo "PASS ${entry}/${route}: status ${status}, proxy line +${proxy_delta}, application line carries ${prefix}/{token} and the identity"
}

for entry in admin web; do
  for route in staging preview playback; do
    check_combination "$entry" "$route"
  done
done

if [ "$failures" -ne 0 ]; then
  echo "FAILED: ${failures} check(s)" >&2
  exit 1
fi
echo "PASS six combinations: no signed value in any log, every request produced a redacted line"
