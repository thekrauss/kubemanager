-- IAM phase A, étape A9 (iam.md §8.4).
--
-- 1. Les clés API utilisateur (km_…, hors clés de cluster) sont rattachées à
--    un compte de service « legacy-<hash utilisateur> » par projet, qui reçoit
--    exactement les rôles de l'utilisateur sur ce projet. Une clé ne vaut plus
--    que pour son projet (avant : elle agissait comme l'utilisateur partout).
-- 2. Elles expirent au plus tard dans 90 jours : remplacement par des clés v2.
-- 3. Les tables du RBAC legacy (roles, permissions, role_permissions) sont
--    supprimées : les rôles vivent dans le code (internal/iam).

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS service_account_id UUID
    REFERENCES service_accounts(id) ON DELETE SET NULL;

INSERT INTO service_accounts (project_id, account_id, display_name, description, created_by)
SELECT DISTINCT k.project_id,
       'legacy-' || left(md5(k.user_id::text), 8),
       left('Clés legacy de ' || COALESCE(NULLIF(u.full_name, ''), u.email), 100),
       'Créé automatiquement pour les anciennes clés km_… de cet utilisateur. Remplacez-les par des clés de compte de service.',
       'migration:000007'
FROM api_keys k
JOIN users u    ON u.id = k.user_id
JOIN projects p ON p.id = k.project_id
WHERE k.kind <> 'cluster' AND k.revoked_at IS NULL AND k.is_active
  AND NOT EXISTS (
      SELECT 1 FROM service_accounts sa
      WHERE sa.project_id = k.project_id AND sa.deleted_at IS NULL
        AND sa.account_id = 'legacy-' || left(md5(k.user_id::text), 8));

UPDATE api_keys k
SET service_account_id = sa.id
FROM service_accounts sa
WHERE k.kind <> 'cluster' AND k.service_account_id IS NULL AND k.revoked_at IS NULL
  AND sa.project_id = k.project_id AND sa.deleted_at IS NULL
  AND sa.account_id = 'legacy-' || left(md5(k.user_id::text), 8);

-- Rôles : ceux de l'utilisateur sur le projet de la clé, sans condition.
INSERT INTO iam_bindings (resource_name, role, principal, created_by)
SELECT DISTINCT b.resource_name, b.role, 'serviceAccount:' || sa.id, 'migration:000007'
FROM service_accounts sa
JOIN api_keys k     ON k.service_account_id = sa.id
JOIN iam_bindings b ON b.resource_name = 'projects/' || sa.project_id
                   AND b.principal = 'user:' || k.user_id
                   AND b.condition IS NULL
WHERE sa.created_by = 'migration:000007'
ON CONFLICT DO NOTHING;

INSERT INTO iam_policies (resource_name, etag, updated_by)
SELECT r.resource_name, md5(random()::text || r.resource_name), 'migration:000007'
FROM (SELECT DISTINCT resource_name FROM iam_bindings WHERE created_by = 'migration:000007') r
ON CONFLICT (resource_name) DO UPDATE
    SET etag = EXCLUDED.etag, version = iam_policies.version + 1,
        updated_at = NOW(), updated_by = EXCLUDED.updated_by;

-- Échéance : 90 jours au plus.
UPDATE api_keys
SET expires_at = LEAST(COALESCE(expires_at, NOW() + INTERVAL '90 days'), NOW() + INTERVAL '90 days')
WHERE kind <> 'cluster' AND revoked_at IS NULL;

-- RBAC legacy.
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
