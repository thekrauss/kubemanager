#!/usr/bin/env bash
# Décode un JWT access_token (payload only) pour inspecter les claims.
# Aucune vérif de signature — à utiliser UNIQUEMENT pour debug.
#
# Usage :
#   # Token passé en arg :
#   bash scripts/keycloak/decode_test_token.sh "eyJhbGc..."
#
#   # Token via env :
#   ACCESS_TOKEN="eyJhbGc..." bash scripts/keycloak/decode_test_token.sh
#
#   # Token généré à la volée (appelle get_test_token.sh) :
#   bash scripts/keycloak/decode_test_token.sh
#
#   # Payload complet au lieu du résumé (aud + roles) :
#   FULL=1 bash scripts/keycloak/decode_test_token.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ACCESS_TOKEN="${ACCESS_TOKEN:-${1:-}}"
API_CLIENT_ID="${API_CLIENT_ID:-kubemanager-api}"
FULL="${FULL:-0}"

fail() { echo "ERROR: $*" >&2; exit 1; }
log()  { echo "==> $*" >&2; }
require_bin() { command -v "$1" >/dev/null 2>&1 || fail "required binary not found: $1"; }

resolve_token() {
  if [ -n "${ACCESS_TOKEN}" ]; then
    printf '%s' "${ACCESS_TOKEN}"
    return
  fi
  log "Pas de token fourni, appel get_test_token.sh"
  "${SCRIPT_DIR}/get_test_token.sh"
}

main() {
  require_bin jq
  require_bin python3

  local token payload
  token="$(resolve_token)"
  [ -n "$token" ] || fail "access token is empty"

  payload="$(printf '%s' "$token" | cut -d '.' -f2)"
  [ -n "$payload" ] || fail "invalid JWT payload (pas de segment .payload.)"

  if [ "$FULL" = "1" ] || [ "$FULL" = "true" ]; then
    PAYLOAD_B64URL="$payload" python3 <<'PY' | jq .
import base64, json, os, sys
payload = os.environ["PAYLOAD_B64URL"]
padding = "=" * (-len(payload) % 4)
try:
    decoded = base64.urlsafe_b64decode(payload + padding)
    data = json.loads(decoded)
except Exception as exc:
    print(f"decode error: {exc}", file=sys.stderr)
    sys.exit(1)
print(json.dumps(data))
PY
    return
  fi

  # Vue résumée : sub / email / aud / realm_access.roles / resource_access.<api>.roles / exp.
  PAYLOAD_B64URL="$payload" API_CLIENT_ID="$API_CLIENT_ID" python3 <<'PY' | jq --arg clientId "$API_CLIENT_ID" '{
  sub: .sub,
  email: .email,
  preferred_username: .preferred_username,
  aud: (.aud // []),
  iss: .iss,
  exp: .exp,
  realm_access_roles: (.realm_access.roles // []),
  api_roles: (.resource_access[$clientId].roles // []),
  scope: .scope
}'
import base64, json, os, sys
payload = os.environ["PAYLOAD_B64URL"]
padding = "=" * (-len(payload) % 4)
try:
    decoded = base64.urlsafe_b64decode(payload + padding)
    data = json.loads(decoded)
except Exception as exc:
    print(f"decode error: {exc}", file=sys.stderr)
    sys.exit(1)
print(json.dumps(data))
PY
}

main "$@"
