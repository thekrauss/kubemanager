-- IAM : clés de cluster → comptes de service système (iam.md §7.4).
--
-- 1. Un compte de service est rattaché soit à un projet, soit à un cluster
--    (compte système du watcher), jamais aux deux.
-- 2. Chaque cluster qui a des clés km_… actives reçoit son compte
--    « cluster-agent » avec roles/platform.clusterAgent sur "platform" ; ses
--    clés y sont rattachées et ne valent plus que pour ce cluster.
-- 3. Les clés de cluster sans cluster_id sont révoquées : sans compte, elles
--    agissaient comme l'administrateur qui les avait créées, sur toute l'API.
-- 4. Les clés km_… de cluster expirent au plus tard dans 90 jours :
--    remplacement par des clés v2 (POST /admin/cluster-keys/{id}/rotate).

ALTER TABLE service_accounts ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE service_accounts ADD COLUMN IF NOT EXISTS cluster_id VARCHAR(100);
ALTER TABLE service_accounts ADD CONSTRAINT chk_service_accounts_owner
    CHECK ((project_id IS NULL) <> (cluster_id IS NULL));
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_accounts_cluster
    ON service_accounts(cluster_id) WHERE deleted_at IS NULL AND cluster_id IS NOT NULL;

UPDATE api_keys
SET revoked_at = NOW(), is_active = FALSE
WHERE kind = 'cluster' AND cluster_id = '' AND revoked_at IS NULL;

INSERT INTO service_accounts (cluster_id, account_id, display_name, description, created_by)
SELECT c.cluster_id, 'cluster-agent', left('Agent du cluster ' || c.cluster_id, 100),
       'Compte système du watcher : rend compte de ce seul cluster.', 'migration:000008'
FROM (SELECT DISTINCT cluster_id FROM api_keys
      WHERE kind = 'cluster' AND cluster_id <> '' AND revoked_at IS NULL AND is_active) c
WHERE NOT EXISTS (
    SELECT 1 FROM service_accounts sa WHERE sa.cluster_id = c.cluster_id AND sa.deleted_at IS NULL);

UPDATE api_keys k
SET service_account_id = sa.id
FROM service_accounts sa
WHERE k.kind = 'cluster' AND k.service_account_id IS NULL
  AND sa.cluster_id = k.cluster_id AND sa.deleted_at IS NULL;

INSERT INTO iam_bindings (resource_name, role, principal, created_by)
SELECT 'platform', 'roles/platform.clusterAgent', 'serviceAccount:' || sa.id, 'migration:000008'
FROM service_accounts sa
WHERE sa.created_by = 'migration:000008'
ON CONFLICT DO NOTHING;

INSERT INTO iam_policies (resource_name, etag, updated_by)
SELECT 'platform', md5(random()::text || 'platform'), 'migration:000008'
WHERE EXISTS (SELECT 1 FROM iam_bindings WHERE created_by = 'migration:000008')
ON CONFLICT (resource_name) DO UPDATE
    SET etag = EXCLUDED.etag, version = iam_policies.version + 1,
        updated_at = NOW(), updated_by = EXCLUDED.updated_by;

UPDATE api_keys
SET expires_at = LEAST(COALESCE(expires_at, NOW() + INTERVAL '90 days'), NOW() + INTERVAL '90 days')
WHERE kind = 'cluster' AND revoked_at IS NULL;
