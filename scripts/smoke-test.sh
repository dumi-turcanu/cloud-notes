#!/usr/bin/env bash
# End-to-end smoke test for a running Cloud Notes deployment (curl + jq).
#
#   BASE_URL        origin of the frontend (default http://localhost:3000)
#   SMOKE_EMAIL     existing user to log in as; with SMOKE_PASSWORD
#   SMOKE_PASSWORD  (otherwise a random user is registered: needs signup_mode=open)
#
# Works against the local compose stack and against a real HTTPS deployment.
# Test notes are soft-deleted at the end; a self-registered user stays behind
# (the API has no user deletion), so prefer SMOKE_EMAIL on shared environments.
# jq filters contain $variables that must not be expanded by the shell.
# shellcheck disable=SC2016
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:3000}"
BASE_URL="${BASE_URL%/}"

for tool in curl jq; do
    command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
JAR="$WORK_DIR/cookies.txt"
HEADERS="$WORK_DIR/headers.txt"
BODY="$WORK_DIR/body.txt"

PASS=0
FAIL=0
STATUS=""

pass() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf 'FAIL  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# check <description> <command...>: PASS when the command succeeds.
check() {
    local description="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        pass "$description"
    else
        fail "$description"
    fi
}

# call <method> <path> [json-body] [extra curl args...]
# Sets STATUS, writes headers to $HEADERS and the body to $BODY. Uses the
# cookie jar unless the extra arguments say otherwise (use `anon` for that).
call() {
    local method="$1" path="$2" data="${3:-}"
    shift 3 || shift $#
    local args=(-sS --max-time 30 -X "$method" -o "$BODY" -D "$HEADERS" -w '%{http_code}'
        -b "$JAR" -c "$JAR")
    if [ -n "$data" ]; then
        args+=(-H 'Content-Type: application/json' --data "$data")
    fi
    STATUS="$(curl "${args[@]}" "$@" "${BASE_URL}${path}" || true)"
}

# anon <method> <path>: request without any cookies.
anon() {
    STATUS="$(curl -sS --max-time 30 -X "$1" -o "$BODY" -D "$HEADERS" -w '%{http_code}' \
        "${BASE_URL}$2" || true)"
}

status_is() { [ "$STATUS" = "$1" ]; }
header_present() { grep -qi "^$1:" "$HEADERS"; }
body_has() { jq -e "$@" "$BODY"; }

random_hex() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

echo "Smoke test against ${BASE_URL}"

# --- frontend --------------------------------------------------------------
anon GET /healthz
check "frontend /healthz returns 200" status_is 200

anon GET /
check "GET / returns 200" status_is 200
for header in Content-Security-Policy X-Content-Type-Options Referrer-Policy \
    Permissions-Policy Cross-Origin-Opener-Policy Strict-Transport-Security; do
    check "GET / sends ${header}" header_present "$header"
done
check "GET / is served with Cache-Control: no-cache" grep -qi '^cache-control: no-cache' "$HEADERS"

# --- public config ---------------------------------------------------------
anon GET /api/config
check "GET /api/config returns 200" status_is 200
check "/api/config describes signup mode and features" \
    body_has '.signup_mode and (.features.public_sharing | type == "boolean")'
SIGNUP_MODE="$(jq -r '.signup_mode // empty' "$BODY" 2>/dev/null || true)"
SHARING="$(jq -r '.features.public_sharing // false' "$BODY" 2>/dev/null || true)"

anon GET /api/notes
check "unauthenticated GET /api/notes returns 401" status_is 401
check "401 body is the JSON error shape" body_has '.error == "unauthorized"'

# --- authentication --------------------------------------------------------
if [ -n "${SMOKE_EMAIL:-}" ] && [ -n "${SMOKE_PASSWORD:-}" ]; then
    EMAIL="$SMOKE_EMAIL"
    PASSWORD="$SMOKE_PASSWORD"
    LOGIN_BODY="$(jq -nc --arg e "$EMAIL" --arg p "$PASSWORD" '{email:$e,password:$p}')"
    call POST /api/auth/login "$LOGIN_BODY"
    check "login as ${EMAIL} returns 200" status_is 200
else
    if [ "$SIGNUP_MODE" != "open" ]; then
        fail "no SMOKE_EMAIL/SMOKE_PASSWORD given and signup_mode is '${SIGNUP_MODE}' (need 'open')"
        printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
        exit 1
    fi
    EMAIL="smoke-$(random_hex 6)@example.com"
    PASSWORD="$(random_hex 16)"
    REGISTER_BODY="$(jq -nc --arg e "$EMAIL" --arg p "$PASSWORD" \
        '{email:$e,password:$p,display_name:"Smoke Test"}')"
    call POST /api/auth/register "$REGISTER_BODY"
    check "register ${EMAIL} returns 201" status_is 201
fi
check "login/register set an HttpOnly SameSite=Strict cookie" \
    grep -qiE '^set-cookie: .*httponly.*samesite=strict|^set-cookie: .*samesite=strict.*httponly' "$HEADERS"

call GET /api/auth/me
check "GET /api/auth/me returns 200" status_is 200
check "/api/auth/me returns the user without password data" \
    body_has --arg e "$(printf '%s' "$EMAIL" | tr '[:upper:]' '[:lower:]')" \
    '.email == $e and (has("password_hash") | not) and (has("password") | not)'

# --- notes lifecycle -------------------------------------------------------
call POST /api/notes '{"title":"Smoke note","content":{"body":"hello from the smoke test"}}'
check "create note returns 201" status_is 201
NOTE_ID="$(jq -r '.id // empty' "$BODY" 2>/dev/null || true)"
SHARE_UUID="$(jq -r '.share_uuid // empty' "$BODY" 2>/dev/null || true)"
check "created note has an id" test -n "$NOTE_ID"

if [ -n "$NOTE_ID" ]; then
    call GET /api/notes
    check "list notes returns 200" status_is 200
    check "list contains the new note" body_has --arg id "$NOTE_ID" 'map(.id) | index($id) != null'

    call GET "/api/notes/${NOTE_ID}"
    check "get note returns 200" status_is 200
    check "note content round-trips" body_has '.content.body == "hello from the smoke test"'

    call PUT "/api/notes/${NOTE_ID}" '{"title":"Smoke note (edited)","content":{"body":"edited"}}'
    check "update note returns 200" status_is 200
    check "update is reflected" body_has '.title == "Smoke note (edited)" and .content.body == "edited"'

    call DELETE "/api/notes/${NOTE_ID}"
    check "delete note returns 204" status_is 204

    call GET /api/notes
    check "deleted note leaves the active list" body_has --arg id "$NOTE_ID" 'map(.id) | index($id) == null'
    call GET /api/notes/trash
    check "trash lists the deleted note" body_has --arg id "$NOTE_ID" 'map(.id) | index($id) != null'

    call POST "/api/notes/${NOTE_ID}/restore"
    check "restore note returns 200" status_is 200
    call GET /api/notes
    check "restored note is back in the active list" body_has --arg id "$NOTE_ID" 'map(.id) | index($id) != null'

    # --- public sharing (feature flag) ---------------------------------------
    if [ "$SHARING" = "true" ]; then
        call POST "/api/notes/${NOTE_ID}/publish"
        check "publish note returns 200" status_is 200
        check "publish returns a share_url ending in the share uuid" \
            body_has --arg u "$SHARE_UUID" '.share_url | endswith("/shared/" + $u)'

        anon GET "/api/public/notes/${SHARE_UUID}"
        check "anonymous public read returns 200" status_is 200
        check "public view exposes only title, content and updated_at" \
            body_has '(keys | sort) == ["content","title","updated_at"]'

        call DELETE "/api/notes/${NOTE_ID}/publish"
        check "unpublish returns 204" status_is 204
        anon GET "/api/public/notes/${SHARE_UUID}"
        check "unpublished note is no longer public (404)" status_is 404
    else
        echo "SKIP  public sharing is disabled on this deployment"
    fi

    # Leave nothing visible behind.
    call DELETE "/api/notes/${NOTE_ID}"
fi

# --- CSRF ------------------------------------------------------------------
call POST /api/notes '{"title":"cross-site","content":{}}' \
    -H 'Origin: https://evil.example' -H 'Sec-Fetch-Site: cross-site'
check "cross-site POST is rejected with 403" status_is 403
check "CSRF rejection is the JSON error shape" body_has '.error == "forbidden"'

# --- logout ----------------------------------------------------------------
call POST /api/auth/logout
check "logout returns 204" status_is 204
call GET /api/auth/me
check "/api/auth/me after logout returns 401" status_is 401

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
