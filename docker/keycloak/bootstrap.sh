#!/bin/sh
# Étape keycloak-bootstrap (production) : realm et clients, puis écriture des
# secrets des deux clients confidentiels dans /out/keycloak.env (600), sans les
# afficher. Rejouable.
set -eu
apk add --no-cache --quiet bash curl jq

# La sortie du script d'origine contient le secret du client admin : on la coupe.
bash /bootstrap/bootstrap_platform.sh >/dev/null

token="$(curl -fsS "$KEYCLOAK_BASE_URL/realms/master/protocol/openid-connect/token" \
  --data-urlencode grant_type=password --data-urlencode client_id=admin-cli \
  --data-urlencode "username=$KEYCLOAK_ADMIN_USER" --data-urlencode "password=$KEYCLOAK_ADMIN_PASSWORD" | jq -r .access_token)"

client_secret() {
  uuid="$(curl -fsS -H "Authorization: Bearer $token" \
    "$KEYCLOAK_BASE_URL/admin/realms/kubemanager/clients?clientId=$1" | jq -r '.[0].id')"
  curl -fsS -H "Authorization: Bearer $token" \
    "$KEYCLOAK_BASE_URL/admin/realms/kubemanager/clients/$uuid/client-secret" | jq -r .value
}

admin_secret="$(client_secret kubemanager-admin)"
auth_secret="$(client_secret betorun-auth)"
[ -n "$admin_secret" ] && [ "$admin_secret" != null ] || { echo "secret kubemanager-admin introuvable" >&2; exit 1; }
[ -n "$auth_secret" ] && [ "$auth_secret" != null ] || { echo "secret betorun-auth introuvable" >&2; exit 1; }

umask 077
printf 'KEYCLOAK_ADMIN_CLIENT_SECRET=%s\nKEYCLOAK_AUTH_CLIENT_SECRET=%s\n' "$admin_secret" "$auth_secret" > /out/keycloak.env
chown "${OPERATOR_UID:-0}" /out/keycloak.env
echo "Keycloak prêt ; secrets écrits dans keycloak.env."
