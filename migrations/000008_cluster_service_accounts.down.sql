-- Retour arrière : les clés km_… de cluster ne sont plus rattachées à un
-- compte. Les révocations et échéances posées par la montée sont conservées.
UPDATE api_keys SET service_account_id = NULL
WHERE kind = 'cluster' AND service_account_id IN (SELECT id FROM service_accounts WHERE cluster_id IS NOT NULL);

DELETE FROM iam_bindings
WHERE principal IN (SELECT 'serviceAccount:' || id FROM service_accounts WHERE cluster_id IS NOT NULL);

-- Les clés v2 des comptes système partent avec eux (ON DELETE CASCADE).
DELETE FROM service_accounts WHERE cluster_id IS NOT NULL;

DROP INDEX IF EXISTS uq_service_accounts_cluster;
ALTER TABLE service_accounts DROP CONSTRAINT IF EXISTS chk_service_accounts_owner;
ALTER TABLE service_accounts DROP COLUMN IF EXISTS cluster_id;
ALTER TABLE service_accounts ALTER COLUMN project_id SET NOT NULL;
