-- project_members n'a jamais cessé d'être écrit pendant la phase A : le
-- retour arrière ne perd aucun droit.
DROP TABLE IF EXISTS iam_bindings;
DROP TABLE IF EXISTS iam_policies;
ALTER TABLE IF EXISTS projects DROP COLUMN IF EXISTS organization_id;
DROP TABLE IF EXISTS organizations;
