#!/usr/bin/env bash
# End-to-end smoke test of the OIDC login (AUTH_PROVIDER=oidc) against the
# local Keycloak stack from `make up-oidc` (curl + jq).
#
#   BASE_URL           origin of the frontend (default http://localhost:3000)
#   OIDC_TEST_USER     Keycloak user name (default: the realm's student@example.com)
#   OIDC_TEST_PASSWORD its password (default: the realm's LOCAL-ONLY password)
#
# It plays the browser: GET /api/auth/oidc/login, follow the redirects to the
# provider's login form, submit it, follow the redirects back to the app, and
# then use the session cookie. The defaults are THROW-AWAY, LOCAL-ONLY values
# from deploy/keycloak/realm-cloud-notes.json.
# jq filters contain $variables that must not be expanded by the shell.
# shellcheck disable=SC2016
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:3000}"
BASE_URL="${BASE_URL%/}"
OIDC_TEST_USER="${OIDC_TEST_USER:-student@example.com}"
OIDC_TEST_PASSWORD="${OIDC_TEST_PASSWORD:-student-local-only-password}"

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
FINAL_URL=""

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
# Sets STATUS, writes headers to $HEADERS and the body to $BODY, with the cookie jar.
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

# browse <curl args...>: a navigation that follows redirects with the cookie jar.
# Sets STATUS (of the last response) and FINAL_URL.
browse() {
    local out
    out="$(curl -sS --max-time 60 -L --max-redirs 15 -o "$BODY" -D "$HEADERS" \
        -w '%{http_code} %{url_effective}' -b "$JAR" -c "$JAR" "$@" || true)"
    STATUS="${out%% *}"
    FINAL_URL="${out#* }"
}

status_is() { [ "$STATUS" = "$1" ]; }
body_has() { jq -e "$@" "$BODY"; }
final_url_is() { [ "$FINAL_URL" = "$1" ]; }

# form_action: the action URL of the provider's login form in $BODY, with the
# HTML escaping of & undone. Keycloak's form is id="kc-form-login"; any other
# POST form is the fallback for providers with a different theme.
form_action() {
    local html form
    html="$(tr '\n\r' '  ' <"$BODY")"
    form="$(printf '%s' "$html" | grep -oE '<form[^>]*id="kc-form-login"[^>]*>' | head -n 1 || true)"
    if [ -z "$form" ]; then
        form="$(printf '%s' "$html" | grep -oiE '<form[^>]*method="post"[^>]*>' | head -n 1 || true)"
    fi
    printf '%s' "$form" | sed -n 's/.*action="\([^"]*\)".*/\1/p' | sed 's/&amp;/\&/g'
}

# Cookie jar lines are tab separated: domain, subdomains, path, secure,
# expiry, name, value; curl prefixes the domain of HttpOnly cookies with #HttpOnly_.
session_cookie_httponly() { grep -qE '^#HttpOnly_.*	(__Host-)?cn_session	' "$JAR"; }
login_state_cookie_gone() { ! grep -E '	(__Host-)?cn_oidc	' "$JAR" | awk -F'\t' '$5 != 0 { found = 1 } END { exit !found }'; }
no_secret_in_body() { ! grep -qi secret "$BODY"; }
location_is() { tr -d '\r' <"$HEADERS" | grep -qixF "location: $1"; }
redirected_off_the_app() { [ "${FINAL_URL#"$BASE_URL"}" = "$FINAL_URL" ]; }

echo "OIDC smoke test against ${BASE_URL}"

# --- configuration ---------------------------------------------------------
anon GET /api/config
check "GET /api/config returns 200" status_is 200
check "the API runs in oidc mode" body_has '.auth.provider == "oidc"'
check "the config names the provider for the sign-in button" body_has '(.auth.display_name | type) == "string" and (.auth.display_name | length) > 0'
check "the config does not leak a client secret" no_secret_in_body

# --- password endpoints are gone -------------------------------------------
call POST /api/auth/login '{"email":"a@example.com","password":"a-very-good-password"}'
check "POST /api/auth/login is not mounted (404)" status_is 404
call POST /api/auth/register '{"email":"a@example.com","password":"a-very-good-password"}'
check "POST /api/auth/register is not mounted (404)" status_is 404

# --- callback without a login in progress ----------------------------------
anon GET '/api/auth/oidc/callback?code=forged&state=forged'
check "a forged callback redirects to /login?error=oidc_failed" location_is '/login?error=oidc_failed'

# --- login through the provider --------------------------------------------
browse "${BASE_URL}/api/auth/oidc/login?from=%2Ftrash"
check "the login redirect chain ends on the provider's login page (200)" status_is 200
ACTION="$(form_action)"
check "the provider's login form was found" test -n "$ACTION"
check "the login page is served by the provider, not by the app" redirected_off_the_app

if [ -n "$ACTION" ]; then
    browse --data-urlencode "username=${OIDC_TEST_USER}" --data-urlencode "password=${OIDC_TEST_PASSWORD}" \
        --data-urlencode "credentialId=" "$ACTION"
    check "submitting the form ends back in the app (200)" status_is 200
    check "...on the page the user asked for (/trash)" final_url_is "${BASE_URL}/trash"
else
    fail "cannot submit the login form"
fi
check "the session cookie is HttpOnly" session_cookie_httponly
check "the one-time login state cookie is gone" login_state_cookie_gone

# --- the session works ------------------------------------------------------
call GET /api/auth/me
check "GET /api/auth/me returns 200" status_is 200
check "/api/auth/me is the signed-in user, without password data" \
    body_has --arg e "$(printf '%s' "$OIDC_TEST_USER" | tr '[:upper:]' '[:lower:]')" \
    '.email == $e and (has("password_hash") | not) and (has("password") | not)'
FIRST_ID="$(jq -r '.id // empty' "$BODY" 2>/dev/null || true)"

call POST /api/notes '{"title":"OIDC smoke note","content":{"body":"hello through single sign-on"}}'
check "create note returns 201" status_is 201
NOTE_ID="$(jq -r '.id // empty' "$BODY" 2>/dev/null || true)"
check "created note has an id" test -n "$NOTE_ID"
if [ -n "$NOTE_ID" ]; then
    call GET /api/notes
    check "list notes returns 200" status_is 200
    check "list contains the new note" body_has --arg id "$NOTE_ID" 'map(.id) | index($id) != null'
    call DELETE "/api/notes/${NOTE_ID}"
    check "delete note returns 204" status_is 204
fi

# --- logout -----------------------------------------------------------------
call POST /api/auth/logout
check "logout returns 200 with the provider's logout URL" status_is 200
check "the logout URL names the client" body_has '(.logout_url | type) == "string" and (.logout_url | test("client_id=cloud-notes"))'
LOGOUT_URL="$(jq -r '.logout_url // empty' "$BODY" 2>/dev/null || true)"
call GET /api/auth/me
check "/api/auth/me after logout returns 401" status_is 401

# End the provider session too (as the SPA does), so the next login asks for
# the password again. Keycloak shows a confirmation page when it gets no
# id_token_hint: submit it.
if [ -n "$LOGOUT_URL" ]; then
    browse "$LOGOUT_URL"
    ACTION="$(form_action)"
    if [ -n "$ACTION" ]; then
        browse --data-urlencode "confirmLogout=Logout" --data-urlencode "session_code=" "$ACTION"
    fi
    check "the provider logout lands back on /login" final_url_is "${BASE_URL}/login"
fi

# --- login again: same account ----------------------------------------------
: > "$JAR"
browse "${BASE_URL}/api/auth/oidc/login"
ACTION="$(form_action)"
if [ -n "$ACTION" ]; then
    browse --data-urlencode "username=${OIDC_TEST_USER}" --data-urlencode "password=${OIDC_TEST_PASSWORD}" \
        --data-urlencode "credentialId=" "$ACTION"
fi
call GET /api/auth/me
check "logging in again returns 200" status_is 200
check "...to the same account" body_has --arg id "$FIRST_ID" '.id == $id'

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
