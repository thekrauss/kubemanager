#!/usr/bin/env bash
# Génère les secrets de production HORS du dépôt, sans jamais les afficher :
#   <dossier>/.env.prod                 (600) depuis .env.prod.example
#   <dossier>/seaweedfs/s3.json         identités du stockage objet
#   <dossier>/registry/token.key, .crt  jetons du registre (ECDSA P-256)
#
# Usage : sudo scripts/generate_prod_secrets.sh [/opt/betorun/secrets]
#
# Valeurs en hexadécimal (beto-shared/pkg/db construit le DSN sans échappement),
# sauf la clé STS : 32 octets en base64, format exigé par internal/auth.
#
# Refuse d'écraser : le pepper et la clé STS sont définitifs.
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
out="${1:-/opt/betorun/secrets}"
# UID de l'image API : doit pouvoir lire la clé des jetons.
readonly APP_UID=10001

fail() { echo "ERREUR : $*" >&2; exit 1; }
command -v openssl >/dev/null || fail "openssl requis"

mkdir -p "$out"
out="$(cd "$out" && pwd)"
case "$out/" in "$repo"/*) fail "le dossier doit être hors du dépôt : $out" ;; esac
[ ! -e "$out/.env.prod" ] || fail "$out/.env.prod existe déjà ; rien n'a été modifié"
[ ! -e "$out/registry/token.key" ] || fail "$out/registry/token.key existe déjà ; rien n'a été modifié"

umask 077
chmod 700 "$out"
mkdir -p "$out/registry" "$out/seaweedfs"

operator_uid="${SUDO_UID:-$(id -u)}"
tmp="$(mktemp "$out/.env.prod.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    IAM_STS_KEY_ENCRYPTION_KEY=change-me)
      printf 'IAM_STS_KEY_ENCRYPTION_KEY=%s\n' "$(openssl rand -base64 32)" ;;
    *=change-me)
      printf '%s=%s\n' "${line%%=*}" "$(openssl rand -hex 32)" ;;
    BETORUN_SECRETS_DIR=*) printf 'BETORUN_SECRETS_DIR=%s\n' "$out" ;;
    OPERATOR_UID=*) printf 'OPERATOR_UID=%s\n' "$operator_uid" ;;
    *) printf '%s\n' "$line" ;;
  esac
done < "$repo/.env.prod.example" > "$tmp"

value() { grep -m1 "^$1=" "$tmp" | cut -d= -f2-; }

# Identités S3 : mêmes droits qu'en développement, secrets neufs.
cat > "$out/seaweedfs/s3.json" <<JSON
{
  "identities": [
    {
      "name": "platform",
      "credentials": [{ "accessKey": "platform", "secretKey": "$(value S3_PLATFORM_SECRET_KEY)" }],
      "actions": ["Admin", "Read", "List", "Tagging", "Write"]
    },
    {
      "name": "edge",
      "credentials": [{ "accessKey": "edge", "secretKey": "$(value S3_EDGE_SECRET_KEY)" }],
      "actions": ["Read:betorun-sites"]
    }
  ]
}
JSON

openssl ecparam -name prime256v1 -genkey -noout -out "$out/registry/token.key" 2>/dev/null
openssl req -x509 -new -key "$out/registry/token.key" -subj "/CN=betorun-registry-token" \
  -days 3650 -out "$out/registry/token.crt" 2>/dev/null
chmod 644 "$out/registry/token.crt"
chmod 600 "$out/registry/token.key" "$out/seaweedfs/s3.json"

[ "$(printf '%s' "$(value IAM_STS_KEY_ENCRYPTION_KEY)" | openssl base64 -d -A | wc -c)" -eq 32 ] \
  || fail "clé STS invalide"

mv "$tmp" "$out/.env.prod"
trap - EXIT
chmod 600 "$out/.env.prod"

if [ "$(id -u)" -eq 0 ]; then
  chown "$APP_UID" "$out/registry/token.key"
  chown -R "$operator_uid" "$out/.env.prod"
  chown "$operator_uid" "$out"
else
  echo "À faire en root : chown $APP_UID $out/registry/token.key" >&2
fi

echo "Secrets écrits dans $out (valeurs non affichées)."
echo "Compléter les noms d'hôtes et SMTP dans $out/.env.prod, puis sauvegarder ce dossier hors du serveur."
