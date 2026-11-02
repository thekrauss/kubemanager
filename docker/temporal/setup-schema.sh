#!/bin/sh
# Schéma Temporal, étape unique avant le serveur. Sans effet s'il est à jour ;
# applique les nouvelles versions après une montée de version de l'image.
# Le mot de passe vient de SQL_PASSWORD (lu par temporal-sql-tool).
set -eu

SCHEMA=/etc/temporal/schema/postgresql/v12
tool() {
  temporal-sql-tool --plugin postgres12 --endpoint "$POSTGRES_SEEDS" --port "$DB_PORT" \
    --user "$POSTGRES_USER" --database "$1" setup-schema -v 0.0
  temporal-sql-tool --plugin postgres12 --endpoint "$POSTGRES_SEEDS" --port "$DB_PORT" \
    --user "$POSTGRES_USER" --database "$1" update-schema -d "$2"
}
tool temporal "$SCHEMA/temporal/versioned"
tool temporal_visibility "$SCHEMA/visibility/versioned"
