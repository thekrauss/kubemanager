#!/usr/bin/env bash
# docker compose de production avec les fichiers d'environnement hors dépôt.
# Usage : deploy/compose.sh up -d   (BETORUN_SECRETS_DIR : défaut /opt/betorun/secrets)
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
secrets="${BETORUN_SECRETS_DIR:-/opt/betorun/secrets}"
args=(-f "$root/docker-compose.prod.yaml" --project-directory "$root" --env-file "$secrets/.env.prod")
for extra in images.env keycloak.env; do
  [ -f "$secrets/$extra" ] && args+=(--env-file "$secrets/$extra")
done
exec docker compose "${args[@]}" "$@"
