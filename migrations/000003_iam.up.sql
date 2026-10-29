-- IAM phase A (docs/architecture/iam.md) : organisations, policies, bindings.
-- Reprend l'existant : organisation personnelle par utilisateur, projets
-- rattachés à l'organisation de leur propriétaire, project_members → bindings.

-- ── Organisations ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS organizations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name  VARCHAR(255) NOT NULL,
    -- PERSONAL : créée à l'inscription, une par utilisateur. TEAM : partagée.
    kind          VARCHAR(20)  NOT NULL DEFAULT 'TEAM',
    -- Utilisateur à l'origine d'une organisation PERSONAL (pas un droit :
    -- les droits sont dans iam_bindings).
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_organizations_personal_owner
    ON organizations(owner_user_id) WHERE kind = 'PERSONAL';

ALTER TABLE projects ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS idx_projects_organization_id ON projects(organization_id);

-- ── Policies et bindings ────────────────────────────────────────────────────
-- iam_policies : une ligne par ressource ayant une policy ; etag pour le
-- read-modify-write de setIamPolicy.
CREATE TABLE IF NOT EXISTS iam_policies (
    resource_name TEXT        PRIMARY KEY,
    etag          TEXT        NOT NULL,
    version       INTEGER     NOT NULL DEFAULT 1,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by    TEXT        NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS iam_bindings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_name TEXT        NOT NULL,
    role          TEXT        NOT NULL,
    principal     TEXT        NOT NULL,
    condition     JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by    TEXT        NOT NULL DEFAULT ''
);
-- Même rôle + principal autorisé plusieurs fois s'il porte des conditions différentes.
CREATE UNIQUE INDEX IF NOT EXISTS uq_iam_bindings
    ON iam_bindings(resource_name, role, principal, md5(COALESCE(condition::text, '')));
CREATE INDEX IF NOT EXISTS idx_iam_bindings_principal ON iam_bindings(principal);
CREATE INDEX IF NOT EXISTS idx_iam_bindings_resource  ON iam_bindings(resource_name);

-- ── Reprise de l'existant ───────────────────────────────────────────────────
-- 1. Une organisation personnelle par utilisateur.
INSERT INTO organizations (display_name, kind, owner_user_id)
SELECT COALESCE(NULLIF(u.full_name, ''), u.email), 'PERSONAL', u.id
FROM users u
WHERE NOT EXISTS (
    SELECT 1 FROM organizations o WHERE o.kind = 'PERSONAL' AND o.owner_user_id = u.id
);

INSERT INTO iam_bindings (resource_name, role, principal, created_by)
SELECT 'organizations/' || o.id, 'roles/owner', 'user:' || o.owner_user_id, 'migration:000003'
FROM organizations o
WHERE o.kind = 'PERSONAL' AND o.owner_user_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- 2. Chaque projet rejoint l'organisation personnelle de son plus ancien OWNER.
UPDATE projects p
SET organization_id = o.id
FROM (
    SELECT DISTINCT ON (pm.project_id) pm.project_id, pm.user_id
    FROM project_members pm
    JOIN roles r ON r.id = pm.role_id
    WHERE r.name = 'OWNER'
    ORDER BY pm.project_id, pm.joined_at, pm.user_id
) owner_of
JOIN organizations o ON o.kind = 'PERSONAL' AND o.owner_user_id = owner_of.user_id
WHERE p.id = owner_of.project_id AND p.organization_id IS NULL;

-- 3. Projet sans OWNER : organisation dédiée sans aucun droit (le staff la
--    réattribue). Même UUID que le projet pour rester traçable.
INSERT INTO organizations (id, display_name, kind)
SELECT p.id, 'Projet ' || p.name, 'TEAM'
FROM projects p
WHERE p.organization_id IS NULL
ON CONFLICT (id) DO NOTHING;

UPDATE projects SET organization_id = id WHERE organization_id IS NULL;
ALTER TABLE projects ALTER COLUMN organization_id SET NOT NULL;

-- 4. project_members → bindings sur le projet (correspondance exacte, cf. iam.md §6.3).
INSERT INTO iam_bindings (resource_name, role, principal, created_by)
SELECT 'projects/' || pm.project_id,
       CASE r.name WHEN 'OWNER' THEN 'roles/owner'
                   WHEN 'DEVELOPER' THEN 'roles/developer'
                   WHEN 'VIEWER' THEN 'roles/viewer' END,
       'user:' || pm.user_id,
       'migration:000003'
FROM project_members pm
JOIN roles r ON r.id = pm.role_id
WHERE r.name IN ('OWNER', 'DEVELOPER', 'VIEWER')
  AND pm.status IN ('ACTIVE', 'APPROVED')
ON CONFLICT DO NOTHING;

-- 5. Une policy (etag) par ressource portant des bindings.
INSERT INTO iam_policies (resource_name, etag, updated_by)
SELECT r.resource_name, md5(random()::text || r.resource_name), 'migration:000003'
FROM (SELECT DISTINCT resource_name FROM iam_bindings) r
ON CONFLICT (resource_name) DO NOTHING;
