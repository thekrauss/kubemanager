#!/usr/bin/env bash
# Crée un utilisateur de test dans le realm kubemanager, avec password et
# assignation d'un rôle client (API_CLIENT_ROLES du bootstrap).
#
# Usage :
#   KEYCLOAK_ADMIN_PASSWORD=admin bash scripts/keycloak/create_test_user.sh
#
# Personnalisation via env :
#   TEST_USER_EMAIL=alice@kubemanager.local \
#   TEST_USER_PASSWORD=Str0ngP@ss \
#   TEST_USER_ROLE=PROJECT_ADMIN \
#   bash scripts/keycloak/create_test_user.sh
#
# À la fin, affiche les credentials — utilisables par get_test_token.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# =====================
# Defaults
# =====================
KEYCLOAK_BASE_URL="${KEYCLOAK_BASE_URL:-http://127.0.0.1:18081}"
KEYCLOAK_REALM="${KEYCLOAK_REALM:-kubemanager}"
KEYCLOAK_ADMIN_USER="${KEYCLOAK_ADMIN_USER:-admin}"
KEYCLOAK_ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-admin}"

API_CLIENT_ID="${API_CLIENT_ID:-kubemanager-api}"

TEST_USER_EMAIL="${TEST_USER_EMAIL:-test.admin@kubemanager.local}"
TEST_USER_USERNAME="${TEST_USER_USERNAME:-$TEST_USER_EMAIL}"
TEST_USER_FIRST_NAME="${TEST_USER_FIRST_NAME:-Test}"
TEST_USER_LAST_NAME="${TEST_USER_LAST_NAME:-Admin}"
TEST_USER_PASSWORD="${TEST_USER_PASSWORD:-}"
TEST_USER_ROLE="${TEST_USER_ROLE:-PROJECT_ADMIN}"

# =====================
# Helpers
# =====================
fail() { echo "ERROR: $*" >&2; exit 1; }
log()  { echo "==> $*" >&2; }
require_bin() { command -v "$1" >/dev/null 2>&1 || fail "required binary not found: $1"; }
trim_trailing_slash() { echo "${1%/}"; }

random_secret() {
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 20
}

api() {
  local method="$1" path="$2" data="${3:-}"
  if [ -n "$data" ]; then
    curl -fsS -X "$method" \
      -H "Authorization: Bearer ${KEYCLOAK_ADMIN_TOKEN}" \
      -H "Content-Type: application/json" \
      -H "Accept: application/json" \
      -d "$data" \
      "${KEYCLOAK_BASE_URL}${path}"
  else
    curl -fsS -X "$method" \
      -H "Authorization: Bearer ${KEYCLOAK_ADMIN_TOKEN}" \
      -H "Accept: application/json" \
      "${KEYCLOAK_BASE_URL}${path}"
  fi
}

status() {
  local method="$1" path="$2"
  curl -sS -o /dev/null -w "%{http_code}" -X "$method" \
    -H "Authorization: Bearer ${KEYCLOAK_ADMIN_TOKEN}" \
    -H "Accept: application/json" \
    "${KEYCLOAK_BASE_URL}${path}"
}

get_admin_token() {
  KEYCLOAK_ADMIN_TOKEN="$(
    curl -fsS -X POST "${KEYCLOAK_BASE_URL}/realms/master/protocol/openid-connect/token" \
      -H "Content-Type: application/x-www-form-urlencoded" \
      -d "grant_type=password" \
      -d "client_id=admin-cli" \
      -d "username=${KEYCLOAK_ADMIN_USER}" \
      -d "password=${KEYCLOAK_ADMIN_PASSWORD}" \
    | jq -r '.access_token'
  )"
  [ -n "${KEYCLOAK_ADMIN_TOKEN}" ] || fail "could not obtain admin token"
}

get_client_uuid() {
  api GET "/admin/realms/${KEYCLOAK_REALM}/clients?clientId=${API_CLIENT_ID}" \
    | jq -r --arg clientId "$API_CLIENT_ID" '.[] | select(.clientId == $clientId) | .id' \
    | head -n 1
}

get_user_id() {
  api GET "/admin/realms/${KEYCLOAK_REALM}/users?exact=true&username=${TEST_USER_USERNAME}" \
    | jq -r --arg username "$TEST_USER_USERNAME" '.[] | select(.username == $username) | .id' \
    | head -n 1
}

ensure_user() {
  local user_id
  user_id="$(get_user_id)"
  if [ -n "$user_id" ]; then
    log "User ${TEST_USER_USERNAME} already exists (${user_id})"
    echo "$user_id"
    return
  fi
  local payload
  payload="$(jq -n \
    --arg username "$TEST_USER_USERNAME" \
    --arg email "$TEST_USER_EMAIL" \
    --arg firstName "$TEST_USER_FIRST_NAME" \
    --arg lastName "$TEST_USER_LAST_NAME" \
    '{
      username: $username,
      email: $email,
      firstName: $firstName,
      lastName: $lastName,
      enabled: true,
      emailVerified: true
    }')"
  log "Creating test user ${TEST_USER_USERNAME}"
  api POST "/admin/realms/${KEYCLOAK_REALM}/users" "$payload" >/dev/null
  user_id="$(get_user_id)"
  [ -n "$user_id" ] || fail "failed to resolve created user id"
  echo "$user_id"
}

set_user_password() {
  local user_id="$1"
  local payload
  payload="$(jq -n --arg password "$TEST_USER_PASSWORD" '{
    type: "password",
    temporary: false,
    value: $password
  }')"
  log "Setting password for ${TEST_USER_USERNAME}"
  api PUT "/admin/realms/${KEYCLOAK_REALM}/users/${user_id}/reset-password" "$payload" >/dev/null
}

role_exists() {
  local client_uuid="$1" role_name="$2"
  local code
  code="$(status GET "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}/roles/${role_name}")"
  [ "$code" = "200" ]
}

user_has_role() {
  local client_uuid="$1" user_id="$2" role_name="$3"
  api GET "/admin/realms/${KEYCLOAK_REALM}/users/${user_id}/role-mappings/clients/${client_uuid}" \
    | jq -e --arg roleName "$role_name" '.[] | select(.name == $roleName)' >/dev/null
}

assign_role() {
  local client_uuid="$1" user_id="$2" role_name="$3"
  local role_json role_id container_id payload

  role_json="$(api GET "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}/roles/${role_name}")"
  role_id="$(echo "$role_json" | jq -r '.id')"
  container_id="$(echo "$role_json" | jq -r '.containerId')"
  payload="$(jq -n --arg id "$role_id" --arg name "$role_name" --arg containerId "$container_id" '[
    {id: $id, name: $name, clientRole: true, containerId: $containerId}
  ]')"

  log "Assigning role ${role_name} to ${TEST_USER_USERNAME}"
  api POST "/admin/realms/${KEYCLOAK_REALM}/users/${user_id}/role-mappings/clients/${client_uuid}" "$payload" >/dev/null
}

main() {
  require_bin curl
  require_bin jq

  [ -n "$KEYCLOAK_ADMIN_PASSWORD" ] || fail "KEYCLOAK_ADMIN_PASSWORD is required"
  KEYCLOAK_BASE_URL="$(trim_trailing_slash "$KEYCLOAK_BASE_URL")"

  # Génère un password aléatoire si pas fourni.
  local generated_password=false
  if [ -z "$TEST_USER_PASSWORD" ]; then
    TEST_USER_PASSWORD="$(random_secret)"
    generated_password=true
  fi

  log "Getting admin token (master realm)"
  get_admin_token

  local client_uuid
  client_uuid="$(get_client_uuid)"
  [ -n "$client_uuid" ] || fail "client ${API_CLIENT_ID} not found — run bootstrap_platform.sh first"

  role_exists "$client_uuid" "$TEST_USER_ROLE" \
    || fail "role ${TEST_USER_ROLE} not found on ${API_CLIENT_ID} — run bootstrap_platform.sh first"

  local user_id
  user_id="$(ensure_user)"
  set_user_password "$user_id"

  if user_has_role "$client_uuid" "$user_id" "$TEST_USER_ROLE"; then
    log "User already has role ${TEST_USER_ROLE}"
  else
    assign_role "$client_uuid" "$user_id" "$TEST_USER_ROLE"
  fi

  echo
  log "Test user ready ✅"
  echo "Realm:       ${KEYCLOAK_REALM}"
  echo "Username:    ${TEST_USER_USERNAME}"
  echo "Email:       ${TEST_USER_EMAIL}"
  echo "Password:    ${TEST_USER_PASSWORD}"
  echo "Client role: ${TEST_USER_ROLE} (on ${API_CLIENT_ID})"
  if [ "$generated_password" = "true" ]; then
    echo
    echo "Password généré aléatoirement — note-le pour get_test_token.sh :"
    echo "  export TEST_USER_USERNAME=${TEST_USER_USERNAME}"
    echo "  export TEST_USER_PASSWORD=${TEST_USER_PASSWORD}"
  fi
}

main "$@"
