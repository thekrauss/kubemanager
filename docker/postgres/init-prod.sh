#!/bin/bash
# Premier démarrage de Postgres en production (volume vide uniquement) : une
# base et un rôle par service. Un service compromis n'atteint pas les autres.
#
# edge_ro reçoit ses droits de lecture plus tard (service edge-db-grants), une
# fois les tables créées par les migrations de l'API.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v keycloak_pw="$KEYCLOAK_DB_PASSWORD" \
  -v temporal_pw="$TEMPORAL_DB_PASSWORD" \
  -v edge_pw="$EDGE_DB_PASSWORD" <<'EOSQL'
CREATE ROLE keycloak LOGIN PASSWORD :'keycloak_pw';
CREATE ROLE temporal LOGIN PASSWORD :'temporal_pw';
CREATE ROLE edge_ro LOGIN PASSWORD :'edge_pw';
CREATE DATABASE keycloak OWNER keycloak;
CREATE DATABASE temporal OWNER temporal;
CREATE DATABASE temporal_visibility OWNER temporal;
EOSQL
