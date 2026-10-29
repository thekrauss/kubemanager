#!/usr/bin/env bash
# Bootstrap Keycloak pour KubeManager.
#
# Adapté du pattern gophercart :
#   - Crée le realm "kubemanager"
#   - Crée 3 clients :
#       * kubemanager-api    (confidentiel, audience mapper)
#       * kubemanager-admin  (confidentiel, service account + rôles realm-management)
#       * kubemanager-web    (public, PKCE, redirects pour le frontend)
#   - Configure les Identity Providers sociaux (Google, GitHub) selon les env vars
#   - Affiche le secret admin à mettre dans KUBEMANAGER_KEYCLOAK_ADMIN_CLIENT_SECRET
#
# Usage :
#   KEYCLOAK_BASE_URL=http://127.0.0.1:18081 \
#   KEYCLOAK_ADMIN_USER=admin KEYCLOAK_ADMIN_PASSWORD=admin \
#   KEYCLOAK_GOOGLE_IDP_ENABLED=true \
#   KEYCLOAK_GOOGLE_CLIENT_ID=xxx KEYCLOAK_GOOGLE_CLIENT_SECRET=yyy \
#   KEYCLOAK_GITHUB_IDP_ENABLED=true \
#   KEYCLOAK_GITHUB_CLIENT_ID=xxx KEYCLOAK_GITHUB_CLIENT_SECRET=yyy \
#   bash scripts/keycloak/bootstrap_platform.sh

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
ADMIN_CLIENT_ID="${ADMIN_CLIENT_ID:-kubemanager-admin}"
WEB_CLIENT_ID="${WEB_CLIENT_ID:-kubemanager-web}"

WEB_FRONTEND_BASE_URL="${WEB_FRONTEND_BASE_URL:-http://127.0.0.1:3000}"
WEB_FRONTEND_ALT_BASE_URL="${WEB_FRONTEND_ALT_BASE_URL:-http://localhost:3000}"

KEYCLOAK_GOOGLE_IDP_ENABLED="${KEYCLOAK_GOOGLE_IDP_ENABLED:-false}"
KEYCLOAK_GOOGLE_CLIENT_ID="${KEYCLOAK_GOOGLE_CLIENT_ID:-}"
KEYCLOAK_GOOGLE_CLIENT_SECRET="${KEYCLOAK_GOOGLE_CLIENT_SECRET:-}"

KEYCLOAK_GITHUB_IDP_ENABLED="${KEYCLOAK_GITHUB_IDP_ENABLED:-false}"
KEYCLOAK_GITHUB_CLIENT_ID="${KEYCLOAK_GITHUB_CLIENT_ID:-}"
KEYCLOAK_GITHUB_CLIENT_SECRET="${KEYCLOAK_GITHUB_CLIENT_SECRET:-}"

# SMTP — pointe vers MailHog en dev (service mailhog:1025 dans docker-compose).
# En prod : SMTP_HOST=smtp.mail.yourco.net SMTP_PORT=587 SMTP_USER=... SMTP_PASSWORD=...
KEYCLOAK_SMTP_ENABLED="${KEYCLOAK_SMTP_ENABLED:-true}"
KEYCLOAK_SMTP_HOST="${KEYCLOAK_SMTP_HOST:-mailhog}"
KEYCLOAK_SMTP_PORT="${KEYCLOAK_SMTP_PORT:-1025}"
KEYCLOAK_SMTP_FROM="${KEYCLOAK_SMTP_FROM:-no-reply@kubemanager.local}"
KEYCLOAK_SMTP_FROM_DISPLAY_NAME="${KEYCLOAK_SMTP_FROM_DISPLAY_NAME:-KubeManager}"
KEYCLOAK_SMTP_AUTH="${KEYCLOAK_SMTP_AUTH:-false}"
KEYCLOAK_SMTP_USER="${KEYCLOAK_SMTP_USER:-}"
KEYCLOAK_SMTP_PASSWORD="${KEYCLOAK_SMTP_PASSWORD:-}"
KEYCLOAK_SMTP_STARTTLS="${KEYCLOAK_SMTP_STARTTLS:-false}"
KEYCLOAK_SMTP_SSL="${KEYCLOAK_SMTP_SSL:-false}"

# Rôles du client API à provisionner.
API_CLIENT_ROLES=(
  "PLATFORM_ADMIN"
  "PROJECT_ADMIN"
  "PROJECT_MEMBER"
  "PROJECT_VIEWER"
)

# Rôles realm-management attribués au service account admin
# (minimum pour gérer users + clients + rôles côté API admin).
REALM_MGMT_ROLES=(
  "view-users"
  "manage-users"
  "view-clients"
  "manage-clients"
  "query-users"
)

# =====================
# Helpers
# =====================
fail() { echo "ERROR: $*" >&2; exit 1; }
log()  { echo "==> $*" >&2; }
require_bin() { command -v "$1" >/dev/null 2>&1 || fail "required binary not found: $1"; }
trim_trailing_slash() { echo "${1%/}"; }

is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
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

# =====================
# Realm + clients
# =====================
ensure_realm() {
  local code
  code="$(status GET "/admin/realms/${KEYCLOAK_REALM}")"
  if [ "$code" = "200" ]; then
    log "Realm ${KEYCLOAK_REALM} already exists"
    return
  fi
  log "Creating realm ${KEYCLOAK_REALM}"
  api POST "/admin/realms" "$(
    jq -n --arg realm "${KEYCLOAK_REALM}" '{
      realm: $realm,
      enabled: true,
      resetPasswordAllowed: true,
      loginWithEmailAllowed: true,
      registrationAllowed: false,
      verifyEmail: false
    }'
  )" >/dev/null
}

# Construit le payload smtpServer pour le realm.
# Si SMTP désactivé, retourne {} (reset de la config).
build_smtp_server_payload() {
  if ! is_true "$KEYCLOAK_SMTP_ENABLED"; then
    jq -n '{}'
    return 0
  fi

  [ -n "$KEYCLOAK_SMTP_HOST" ] || fail "KEYCLOAK_SMTP_HOST required when KEYCLOAK_SMTP_ENABLED=true"
  [ -n "$KEYCLOAK_SMTP_FROM" ] || fail "KEYCLOAK_SMTP_FROM required when KEYCLOAK_SMTP_ENABLED=true"

  jq -n \
    --arg host "$KEYCLOAK_SMTP_HOST" \
    --arg port "$KEYCLOAK_SMTP_PORT" \
    --arg from "$KEYCLOAK_SMTP_FROM" \
    --arg fromDisplayName "$KEYCLOAK_SMTP_FROM_DISPLAY_NAME" \
    --arg auth "$KEYCLOAK_SMTP_AUTH" \
    --arg user "$KEYCLOAK_SMTP_USER" \
    --arg password "$KEYCLOAK_SMTP_PASSWORD" \
    --arg starttls "$KEYCLOAK_SMTP_STARTTLS" \
    --arg ssl "$KEYCLOAK_SMTP_SSL" \
    '{
      host: $host,
      port: $port,
      from: $from,
      fromDisplayName: $fromDisplayName,
      auth: $auth,
      user: $user,
      password: $password,
      starttls: $starttls,
      ssl: $ssl
    } | with_entries(select(.value != ""))'
}

# Applique la config SMTP au realm existant (et active resetPasswordAllowed).
# À appeler APRÈS ensure_realm.
reconcile_realm_smtp() {
  local realm_json smtp_json payload
  realm_json="$(api GET "/admin/realms/${KEYCLOAK_REALM}")"
  smtp_json="$(build_smtp_server_payload)"
  payload="$(
    jq -n \
      --argjson realm "${realm_json}" \
      --argjson smtpServer "${smtp_json}" \
      '$realm + {
        resetPasswordAllowed: true,
        smtpServer: $smtpServer
      }'
  )"
  api PUT "/admin/realms/${KEYCLOAK_REALM}" "${payload}" >/dev/null

  if is_true "$KEYCLOAK_SMTP_ENABLED"; then
    log "Realm ${KEYCLOAK_REALM} SMTP configured (${KEYCLOAK_SMTP_HOST}:${KEYCLOAK_SMTP_PORT}, from=${KEYCLOAK_SMTP_FROM})"
  else
    log "Realm ${KEYCLOAK_REALM} SMTP disabled"
  fi
}

get_client_uuid() {
  local realm="$1" clientId="$2"
  api GET "/admin/realms/${realm}/clients?clientId=${clientId}" |
    jq -r --arg clientId "$clientId" '.[] | select(.clientId == $clientId) | .id' |
    head -n 1
}

ensure_api_client() {
  local realm="$1" clientId="$2"
  local uuid payload

  payload="$(
    jq -n --arg cid "$clientId" '{
      clientId: $cid,
      name: $cid,
      protocol: "openid-connect",
      enabled: true,
      publicClient: false,
      bearerOnly: true,
      standardFlowEnabled: false,
      directAccessGrantsEnabled: false,
      serviceAccountsEnabled: false,
      fullScopeAllowed: true,
      attributes: {
        "access.token.lifespan": "900"
      }
    }'
  )"

  uuid="$(get_client_uuid "$realm" "$clientId")"
  if [ -n "$uuid" ]; then
    log "Reconciling API client ${clientId} (${uuid})"
    api PUT "/admin/realms/${realm}/clients/${uuid}" "$payload" >/dev/null
  else
    log "Creating API client ${clientId}"
    api POST "/admin/realms/${realm}/clients" "$payload" >/dev/null
    uuid="$(get_client_uuid "$realm" "$clientId")"
  fi
  [ -n "$uuid" ] || fail "failed to resolve API client uuid for ${clientId}"

  # Audience mapper : force l'audience du JWT vers ce client ID
  ensure_audience_mapper "$realm" "$uuid" "$clientId"
  echo "$uuid"
}

ensure_audience_mapper() {
  local realm="$1" client_uuid="$2" audience="$3"
  local mappers mapper_id

  mappers="$(api GET "/admin/realms/${realm}/clients/${client_uuid}/protocol-mappers/models")"
  mapper_id="$(echo "$mappers" | jq -r --arg aud "$audience" '.[] | select(.name == "audience-" + $aud) | .id')"
  if [ -n "$mapper_id" ]; then
    log "Audience mapper for ${audience} already present"
    return
  fi

  log "Adding audience mapper for ${audience}"
  api POST "/admin/realms/${realm}/clients/${client_uuid}/protocol-mappers/models" "$(
    jq -n --arg aud "$audience" '{
      name: ("audience-" + $aud),
      protocol: "openid-connect",
      protocolMapper: "oidc-audience-mapper",
      config: {
        "included.client.audience": $aud,
        "id.token.claim": "false",
        "access.token.claim": "true"
      }
    }'
  )" >/dev/null
}

ensure_api_client_roles() {
  local realm="$1" client_uuid="$2"
  for role in "${API_CLIENT_ROLES[@]}"; do
    local code
    code="$(status GET "/admin/realms/${realm}/clients/${client_uuid}/roles/${role}")"
    if [ "$code" = "200" ]; then
      log "Role ${role} already exists"
      continue
    fi
    log "Creating client role ${role}"
    api POST "/admin/realms/${realm}/clients/${client_uuid}/roles" "$(
      jq -n --arg n "$role" '{name:$n}'
    )" >/dev/null
  done
}

ensure_admin_client() {
  local realm="$1" clientId="$2"
  local uuid

  uuid="$(get_client_uuid "$realm" "$clientId")"
  if [ -z "$uuid" ]; then
    log "Creating admin client ${clientId} (service account)"
    api POST "/admin/realms/${realm}/clients" "$(
      jq -n --arg cid "$clientId" '{
        clientId: $cid,
        name: $cid,
        protocol: "openid-connect",
        enabled: true,
        publicClient: false,
        serviceAccountsEnabled: true,
        standardFlowEnabled: false,
        directAccessGrantsEnabled: true
      }'
    )" >/dev/null
    uuid="$(get_client_uuid "$realm" "$clientId")"
    [ -n "$uuid" ] || fail "failed to resolve admin client uuid"
  else
    log "Admin client ${clientId} already exists (${uuid})"
  fi
  echo "$uuid"
}

ensure_web_client() {
  local realm="$1" clientId="$2"
  local primary="${3%/}" alternate="${4%/}"
  local uuid payload extra_base_urls_json

  extra_base_urls_json="$(jq -cn '[
    "http://127.0.0.1:3000",
    "http://localhost:3000",
    "http://127.0.0.1:5173",
    "http://localhost:5173"
  ]')"

  payload="$(
    jq -n \
      --arg cid "$clientId" \
      --arg primary "$primary" \
      --arg alternate "$alternate" \
      --argjson extras "$extra_base_urls_json" \
      '{
        clientId: $cid,
        name: $cid,
        protocol: "openid-connect",
        enabled: true,
        publicClient: true,
        bearerOnly: false,
        standardFlowEnabled: true,
        implicitFlowEnabled: false,
        directAccessGrantsEnabled: false,
        serviceAccountsEnabled: false,
        fullScopeAllowed: true,
        rootUrl: $primary,
        baseUrl: $primary,
        redirectUris: (
          ([$primary, $alternate] + $extras)
          | unique
          | map([. + "/auth/callback", . + "/sign-in"])
          | add
          | unique
        ),
        webOrigins: (([$primary, $alternate] + $extras) | unique),
        attributes: { "pkce.code.challenge.method": "S256" }
      }'
  )"

  uuid="$(get_client_uuid "$realm" "$clientId")"
  if [ -n "$uuid" ]; then
    log "Reconciling public web client ${clientId} (${uuid})"
    api PUT "/admin/realms/${realm}/clients/${uuid}" "$payload" >/dev/null
  else
    log "Creating public web client ${clientId}"
    api POST "/admin/realms/${realm}/clients" "$payload" >/dev/null
    uuid="$(get_client_uuid "$realm" "$clientId")"
  fi
  [ -n "$uuid" ] || fail "failed to resolve web client uuid"
  echo "$uuid"
}

# =====================
# Service account roles
# =====================
get_client_secret() {
  local realm="$1" uuid="$2"
  api GET "/admin/realms/${realm}/clients/${uuid}/client-secret" | jq -r '.value // empty'
}

get_service_account_user_id() {
  local realm="$1" client_uuid="$2"
  api GET "/admin/realms/${realm}/clients/${client_uuid}/service-account-user" | jq -r '.id // empty'
}

sa_has_rm_role() {
  local realm="$1" sa_user_id="$2" rm_uuid="$3" role_name="$4"
  api GET "/admin/realms/${realm}/users/${sa_user_id}/role-mappings/clients/${rm_uuid}" \
    | jq -e --arg r "$role_name" '.[] | select(.name == $r)' >/dev/null
}

assign_rm_role_to_sa() {
  local realm="$1" sa_user_id="$2" rm_uuid="$3" role_name="$4"
  if sa_has_rm_role "$realm" "$sa_user_id" "$rm_uuid" "$role_name"; then
    log "Service account already has realm-management role: ${role_name}"
    return
  fi
  local role_json
  role_json="$(api GET "/admin/realms/${realm}/clients/${rm_uuid}/roles/${role_name}")"
  log "Assigning realm-management role to SA: ${role_name}"
  api POST "/admin/realms/${realm}/users/${sa_user_id}/role-mappings/clients/${rm_uuid}" "[$role_json]" >/dev/null
}

# =====================
# Social IDPs (Google + GitHub)
# =====================
get_idp() {
  local realm="$1" alias="$2" code
  code="$(status GET "/admin/realms/${realm}/identity-provider/instances/${alias}")"
  [ "$code" = "200" ] || return 1
  api GET "/admin/realms/${realm}/identity-provider/instances/${alias}"
}

ensure_idp() {
  local realm="$1" provider_id="$2" alias="$3"
  local client_id="$4" client_secret="$5" display_name="$6" default_scope="$7"
  local existing payload

  existing="$(get_idp "$realm" "$alias" || true)"
  payload="$(
    jq -n \
      --argjson existing "${existing:-null}" \
      --arg alias "$alias" \
      --arg providerId "$provider_id" \
      --arg displayName "$display_name" \
      --arg clientId "$client_id" \
      --arg clientSecret "$client_secret" \
      --arg defaultScope "$default_scope" \
      '($existing // {}) + {
        alias: $alias,
        providerId: $providerId,
        displayName: $displayName,
        enabled: true,
        trustEmail: true,
        storeToken: false,
        addReadTokenRoleOnCreate: false,
        authenticateByDefault: false,
        linkOnly: false,
        firstBrokerLoginFlowAlias: ((($existing // {}).firstBrokerLoginFlowAlias) // "first broker login"),
        config: (((($existing // {}).config) // {}) + {
          clientId: $clientId,
          clientSecret: $clientSecret,
          defaultScope: $defaultScope,
          useJwksUrl: "true"
        })
      }'
  )"

  if [ -n "$existing" ]; then
    log "Reconciling IDP ${alias}"
    api PUT "/admin/realms/${realm}/identity-provider/instances/${alias}" "$payload" >/dev/null
  else
    log "Creating IDP ${alias} (${provider_id})"
    api POST "/admin/realms/${realm}/identity-provider/instances" "$payload" >/dev/null
  fi
}

configure_social_idps() {
  local realm="$1"
  if is_true "$KEYCLOAK_GOOGLE_IDP_ENABLED"; then
    [ -n "$KEYCLOAK_GOOGLE_CLIENT_ID" ]     || fail "KEYCLOAK_GOOGLE_CLIENT_ID required"
    [ -n "$KEYCLOAK_GOOGLE_CLIENT_SECRET" ] || fail "KEYCLOAK_GOOGLE_CLIENT_SECRET required"
    ensure_idp "$realm" "google" "google" \
      "$KEYCLOAK_GOOGLE_CLIENT_ID" "$KEYCLOAK_GOOGLE_CLIENT_SECRET" \
      "Google" "openid email profile"
  else
    log "Skipping Google IDP (KEYCLOAK_GOOGLE_IDP_ENABLED=${KEYCLOAK_GOOGLE_IDP_ENABLED})"
  fi

  if is_true "$KEYCLOAK_GITHUB_IDP_ENABLED"; then
    [ -n "$KEYCLOAK_GITHUB_CLIENT_ID" ]     || fail "KEYCLOAK_GITHUB_CLIENT_ID required"
    [ -n "$KEYCLOAK_GITHUB_CLIENT_SECRET" ] || fail "KEYCLOAK_GITHUB_CLIENT_SECRET required"
    ensure_idp "$realm" "github" "github" \
      "$KEYCLOAK_GITHUB_CLIENT_ID" "$KEYCLOAK_GITHUB_CLIENT_SECRET" \
      "GitHub" "user:email"
  else
    log "Skipping GitHub IDP (KEYCLOAK_GITHUB_IDP_ENABLED=${KEYCLOAK_GITHUB_IDP_ENABLED})"
  fi
}

# =====================
# Main
# =====================
main() {
  require_bin curl
  require_bin jq

  KEYCLOAK_BASE_URL="$(trim_trailing_slash "$KEYCLOAK_BASE_URL")"
  log "Keycloak base: ${KEYCLOAK_BASE_URL}"
  log "Repo root: ${REPO_ROOT}"

  log "Getting admin token (master realm)"
  get_admin_token

  ensure_realm
  reconcile_realm_smtp

  log "Ensuring API client ${API_CLIENT_ID}"
  local api_uuid
  api_uuid="$(ensure_api_client "$KEYCLOAK_REALM" "$API_CLIENT_ID")"
  ensure_api_client_roles "$KEYCLOAK_REALM" "$api_uuid"

  log "Ensuring admin client ${ADMIN_CLIENT_ID}"
  local admin_uuid
  admin_uuid="$(ensure_admin_client "$KEYCLOAK_REALM" "$ADMIN_CLIENT_ID")"

  log "Ensuring public web client ${WEB_CLIENT_ID}"
  local web_uuid
  web_uuid="$(ensure_web_client "$KEYCLOAK_REALM" "$WEB_CLIENT_ID" \
    "$WEB_FRONTEND_BASE_URL" "$WEB_FRONTEND_ALT_BASE_URL")"
  # Login PKCE : les tokens émis pour le client web doivent porter
  # aud=<API client>, sinon l'AuthMiddleware (strict_audience) les refuse.
  ensure_audience_mapper "$KEYCLOAK_REALM" "$web_uuid" "$API_CLIENT_ID"

  log "Configuring social identity providers"
  configure_social_idps "$KEYCLOAK_REALM"

  local admin_secret sa_user_id rm_uuid
  admin_secret="$(get_client_secret "$KEYCLOAK_REALM" "$admin_uuid")"
  [ -n "$admin_secret" ] || fail "could not read client secret for ${ADMIN_CLIENT_ID}"

  sa_user_id="$(get_service_account_user_id "$KEYCLOAK_REALM" "$admin_uuid")"
  [ -n "$sa_user_id" ] || fail "could not resolve SA user for ${ADMIN_CLIENT_ID}"

  rm_uuid="$(get_client_uuid "$KEYCLOAK_REALM" "realm-management")"
  [ -n "$rm_uuid" ] || fail "could not resolve realm-management client uuid"

  for role in "${REALM_MGMT_ROLES[@]}"; do
    assign_rm_role_to_sa "$KEYCLOAK_REALM" "$sa_user_id" "$rm_uuid" "$role"
  done

  echo
  log "Bootstrap completed ✅"
  echo "Realm:         ${KEYCLOAK_REALM}"
  echo "Client (API):   ${API_CLIENT_ID}"
  echo "Client (Admin): ${ADMIN_CLIENT_ID}"
  echo "Client (Web):   ${WEB_CLIENT_ID}"
  if is_true "$KEYCLOAK_SMTP_ENABLED"; then
    echo "SMTP:           ${KEYCLOAK_SMTP_HOST}:${KEYCLOAK_SMTP_PORT} (from=${KEYCLOAK_SMTP_FROM})"
    if [ "${KEYCLOAK_SMTP_HOST}" = "mailhog" ] || [ "${KEYCLOAK_SMTP_HOST}" = "127.0.0.1" ]; then
      echo "MailHog UI:     http://localhost:8025 (lis ici les mails envoyés par Keycloak)"
    fi
  fi
  if is_true "$KEYCLOAK_GOOGLE_IDP_ENABLED"; then
    echo "Google redirect URI: ${KEYCLOAK_BASE_URL}/realms/${KEYCLOAK_REALM}/broker/google/endpoint"
  fi
  if is_true "$KEYCLOAK_GITHUB_IDP_ENABLED"; then
    echo "GitHub redirect URI: ${KEYCLOAK_BASE_URL}/realms/${KEYCLOAK_REALM}/broker/github/endpoint"
  fi
  echo
  echo "Add this to your env file:"
  echo "  KUBEMANAGER_KEYCLOAK_ADMIN_CLIENT_ID=${ADMIN_CLIENT_ID}"
  echo "  KUBEMANAGER_KEYCLOAK_ADMIN_CLIENT_SECRET=${admin_secret}"
  echo "  KUBEMANAGER_KEYCLOAK_REALM=${KEYCLOAK_REALM}"
  echo "  KUBEMANAGER_KEYCLOAK_ISSUER_URL=${KEYCLOAK_BASE_URL}/realms/${KEYCLOAK_REALM}"
  echo "  KUBEMANAGER_KEYCLOAK_CLIENT_ID=${API_CLIENT_ID}"
  echo
  echo "Next steps :"
  echo "  1. Créer un user de test :"
  echo "     bash scripts/keycloak/create_test_user.sh"
  echo "  2. Obtenir un access_token :"
  echo "     bash scripts/keycloak/get_test_token.sh"
  echo "  3. Décoder pour debug :"
  echo "     bash scripts/keycloak/decode_test_token.sh"
}

main "$@"
