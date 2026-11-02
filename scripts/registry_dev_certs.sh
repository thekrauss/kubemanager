#!/usr/bin/env bash
# Clé et certificat de développement des jetons du registre d'images.
# Ne pas réutiliser en production. Ces fichiers sont ignorés par git.
set -euo pipefail
dir="$(cd "$(dirname "$0")/.." && pwd)/docker/registry/certs"
mkdir -p "$dir"
[ -f "$dir/token.key" ] && { echo "déjà présents : $dir"; exit 0; }
openssl ecparam -name prime256v1 -genkey -noout -out "$dir/token.key"
openssl req -x509 -new -key "$dir/token.key" -subj "/CN=betorun-registry-token" -days 3650 -out "$dir/token.crt"
chmod 600 "$dir/token.key"
echo "créés : $dir"
