#!/bin/bash
# Exécuté une seule fois au premier boot de k8sdb (volume vide).
# Si tu modifies cette logique après un premier boot, il faut `docker compose
# down -v` pour recréer le volume — les scripts initdb.d ne s'exécutent pas
# sur un volume déjà initialisé.
#
# But : provisionner une database `keycloak` séparée dans l'instance Postgres
# existante, utilisée par le service Keycloak pour persister realm/users/etc.

set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE DATABASE keycloak;
    GRANT ALL PRIVILEGES ON DATABASE keycloak TO $POSTGRES_USER;
EOSQL

echo "==> Postgres: database 'keycloak' created and owned by $POSTGRES_USER"
