#!/usr/bin/env bash
# Obtient un access_token via ROPC pour le user de test.
#
# Provisionne automatiquement un client "kubemanager-dev-cli" (public,
# directAccessGrants=true) avec un audience mapper vers kubemanager-api —
# pour que les tokens obtenus soient acceptés par le middleware auth
# (StrictAudience=true).
#
# Usage :
#   KEYCLOAK_ADMIN_PASSWORD=admin \
#   TEST_USER_USERNAME=test.admin@kubemanager.local \
#   TEST_USER_PASSWORD=<secret> \
#   bash scripts/keycloak/get_test_token.sh
#
#   # JSON complet (access + refresh + expires_in) :
#   OUTPUT=json bash scripts/keycloak/get_test_token.sh
#
#   # Token uniquement (usable dans un header Authorization) :
#   TOKEN=$(bash scripts/keycloak/get_test_token.sh)
#   curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/kmanager/v1/...

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
TEST_CLIENT_ID="${TEST_CLIENT_ID:-kubemanager-dev-cli}"

TEST_USER_USERNAME="${TEST_USER_USERNAME:-test.admin@kubemanager.local}"
TEST_USER_PASSWORD="${TEST_USER_PASSWORD:-}"

OUTPUT="${OUTPUT:-token}" # token|json

# =====================
# Helpers
# =====================
fail() { echo "ERROR: $*" >&2; exit 1; }
log()  { echo "==> $*" >&2; }
require_bin() { command -v "$1" >/dev/null 2>&1 || fail "required binary not found: $1"; }
trim_trailing_slash() { echo "${1%/}"; }

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
  local client_id="$1"
  api GET "/admin/realms/${KEYCLOAK_REALM}/clients?clientId=${client_id}" \
    | jq -r --arg clientId "$client_id" '.[] | select(.clientId == $clientId) | .id' \
    | head -n 1
}

create_test_client() {
  local payload
  payload="$(jq -n --arg clientId "$TEST_CLIENT_ID" '{
    clientId: $clientId,
    name: $clientId,
    protocol: "openid-connect",
    enabled: true,
    publicClient: true,
    bearerOnly: false,
    standardFlowEnabled: false,
    directAccessGrantsEnabled: true,
    serviceAccountsEnabled: false,
    fullScopeAllowed: true
  }')"
  log "Creating test client ${TEST_CLIENT_ID} (public, ROPC)"
  api POST "/admin/realms/${KEYCLOAK_REALM}/clients" "$payload" >/dev/null
}

reconcile_test_client() {
  local client_uuid="$1"
  local payload
  payload="$(
    api GET "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}" \
      | jq '
          .enabled = true |
          .publicClient = true |
          .bearerOnly = false |
          .standardFlowEnabled = false |
          .directAccessGrantsEnabled = true |
          .serviceAccountsEnabled = false |
          .fullScopeAllowed = true
        '
  )"
  log "Reconciling test client ${TEST_CLIENT_ID}"
  api PUT "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}" "$payload" >/dev/null
}

ensure_test_client() {
  local client_uuid
  client_uuid="$(get_client_uuid "$TEST_CLIENT_ID")"
  if [ -n "$client_uuid" ]; then
    log "Test client ${TEST_CLIENT_ID} already exists (${client_uuid})"
    reconcile_test_client "$client_uuid"
    echo "$client_uuid"
    return
  fi
  create_test_client
  client_uuid="$(get_client_uuid "$TEST_CLIENT_ID")"
  [ -n "$client_uuid" ] || fail "failed to resolve test client uuid"
  reconcile_test_client "$client_uuid"
  echo "$client_uuid"
}

get_client_scope_id() {
  local scope_name="$1"
  api GET "/admin/realms/${KEYCLOAK_REALM}/client-scopes?search=${scope_name}" \
    | jq -r --arg scopeName "$scope_name" '.[] | select(.name == $scopeName) | .id' \
    | head -n 1
}

default_scope_attached() {
  local client_uuid="$1" scope_id="$2"
  api GET "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}/default-client-scopes" \
    | jq -e --arg scopeId "$scope_id" '.[] | select(.id == $scopeId)' >/dev/null
}

ensure_roles_client_scope() {
  local client_uuid="$1"
  local scope_id
  scope_id="$(get_client_scope_id "roles")"
  [ -n "$scope_id" ] || fail "built-in client scope 'roles' not found"

  if default_scope_attached "$client_uuid" "$scope_id"; then
    log "Scope 'roles' déjà attaché à ${TEST_CLIENT_ID}"
    return
  fi
  log "Attaching scope 'roles' to ${TEST_CLIENT_ID}"
  api PUT "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}/default-client-scopes/${scope_id}" >/dev/null
}

get_protocol_mapper_id() {
  local client_uuid="$1" mapper_name="$2"
  api GET "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}/protocol-mappers/models" \
    | jq -r --arg mapperName "$mapper_name" '.[] | select(.name == $mapperName) | .id' \
    | head -n 1
}

ensure_audience_mapper() {
  local client_uuid="$1"
  local mapper_name="audience-${API_CLIENT_ID}"
  if [ -n "$(get_protocol_mapper_id "$client_uuid" "$mapper_name")" ]; then
    log "Audience mapper ${mapper_name} déjà présent"
    return
  fi
  local payload
  payload="$(jq -n \
    --arg name "$mapper_name" \
    --arg clientId "$API_CLIENT_ID" \
    '{
      name: $name,
      protocol: "openid-connect",
      protocolMapper: "oidc-audience-mapper",
      consentRequired: false,
      config: {
        "included.client.audience": $clientId,
        "id.token.claim": "false",
        "access.token.claim": "true",
        "introspection.token.claim": "true"
      }
    }')"
  log "Adding audience mapper → ${API_CLIENT_ID} on ${TEST_CLIENT_ID}"
  api POST "/admin/realms/${KEYCLOAK_REALM}/clients/${client_uuid}/protocol-mappers/models" "$payload" >/dev/null
}

fetch_token() {
  curl -fsS -X POST "${KEYCLOAK_BASE_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/token" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "grant_type=password" \
    -d "client_id=${TEST_CLIENT_ID}" \
    -d "username=${TEST_USER_USERNAME}" \
    -d "password=${TEST_USER_PASSWORD}" \
    -d "scope=openid"
}

main() {
  require_bin curl
  require_bin jq

  [ -n "$KEYCLOAK_ADMIN_PASSWORD" ] || fail "KEYCLOAK_ADMIN_PASSWORD is required"
  [ -n "$TEST_USER_PASSWORD" ]      || fail "TEST_USER_PASSWORD is required (run create_test_user.sh first)"

  KEYCLOAK_BASE_URL="$(trim_trailing_slash "$KEYCLOAK_BASE_URL")"

  log "Getting admin token (master)"
  get_admin_token

  local client_uuid
  client_uuid="$(ensure_test_client)"
  ensure_roles_client_scope "$client_uuid"
  ensure_audience_mapper "$client_uuid"

  log "Fetching access token for ${TEST_USER_USERNAME}"
  if [ "${OUTPUT}" = "json" ]; then
    fetch_token | jq .
    return
  fi
  fetch_token | jq -r '.access_token'
}

main "$@"
