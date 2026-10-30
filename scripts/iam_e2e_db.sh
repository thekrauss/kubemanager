#!/usr/bin/env bash
# Postgres jetable pour les tests d'intégration IAM (internal/iam/...) :
# toutes les migrations, avec les données « legacy » (rôles de projet
# OWNER/DEVELOPER/VIEWER) insérées avant la migration 000003 qui les reprend.
#
#   bash scripts/iam_e2e_db.sh
#   IAM_E2E=1 go test ./internal/iam/... -p 1 -count=1
#
# Recréée à chaque lancement : les tests ajoutent des données.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
docker rm -f iam-e2e-pg >/dev/null 2>&1 || true
docker run -d --rm --name iam-e2e-pg -e POSTGRES_PASSWORD=pw -p 127.0.0.1:55433:5432 postgres:15-alpine >/dev/null
until docker exec iam-e2e-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 0.5; done
sleep 1
run() { docker exec -i iam-e2e-pg psql -q -v ON_ERROR_STOP=1 -U postgres; }
run < migrations/000001_init.up.sql
run < migrations/000002_sites.up.sql
run <<'SQL'
INSERT INTO roles (id, name) VALUES ('00000000-0000-0000-0000-0000000000a1','OWNER'),('00000000-0000-0000-0000-0000000000a2','DEVELOPER'),('00000000-0000-0000-0000-0000000000a3','VIEWER');
INSERT INTO users (id, keycloak_user_id, email, full_name) VALUES
 ('11111111-1111-1111-1111-111111111111','11111111-1111-1111-1111-111111111111','awa@ex.sn','Awa Diop'),
 ('22222222-2222-2222-2222-222222222222','22222222-2222-2222-2222-222222222222','moussa@ex.sn',''),
 ('33333333-3333-3333-3333-333333333333','33333333-3333-3333-3333-333333333333','fatou@ex.sn','Fatou S');
INSERT INTO projects (id, name) VALUES ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','sunupay'),('bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb','orphan');
INSERT INTO project_members (project_id, user_id, role_id, status) VALUES
 ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','11111111-1111-1111-1111-111111111111','00000000-0000-0000-0000-0000000000a1','ACTIVE'),
 ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','22222222-2222-2222-2222-222222222222','00000000-0000-0000-0000-0000000000a2','ACTIVE'),
 ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','33333333-3333-3333-3333-333333333333','00000000-0000-0000-0000-0000000000a3','PENDING');
INSERT INTO api_keys (id, project_id, user_id, kind, name, prefix, key_hash, scopes, revoked_at, is_active, cluster_id) VALUES
 ('c0000000-0000-0000-0000-000000000001','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','11111111-1111-1111-1111-111111111111','backend','ci awa','km_prod_aaaaaaaaaaaa','92e0d04c2cfaea4d1d78b7fb8315f5b9c826ec9c76f0064d8c48d5b3cf3a12ac','{project:view}',NULL,true,''),
 ('c0000000-0000-0000-0000-000000000002','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','22222222-2222-2222-2222-222222222222','cli','cli moussa','km_prod_bbbbbbbbbbbb','647393ec67b9eb27b9ae9e73c1cc631e5fa2dfdf2b2ca3a86a1537576907a7e1','{k8s:workload:create}',NULL,true,''),
 ('c0000000-0000-0000-0000-000000000003','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','11111111-1111-1111-1111-111111111111','cluster','watcher','km_prod_cccccccccccc','x','{cluster:report}',NULL,true,'k3s-dakar'),
 ('c0000000-0000-0000-0000-000000000004','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','11111111-1111-1111-1111-111111111111','backend','old','km_prod_dddddddddddd','y','{}',NOW(),false,''),
 ('c0000000-0000-0000-0000-000000000005','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','11111111-1111-1111-1111-111111111111','cluster','watcher sans cluster','km_prod_eeeeeeeeeeee','z','{cluster:report}',NULL,true,'');
SQL
for f in $(ls migrations/*.up.sql | sort | tail -n +3); do run < "$f"; done
echo "iam-e2e-pg ready on 127.0.0.1:55433"
