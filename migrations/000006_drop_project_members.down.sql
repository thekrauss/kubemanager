-- Retour arrière : recrée project_members depuis les bindings IAM (rôles
-- legacy seulement), pour une version antérieure du code.
CREATE TABLE IF NOT EXISTS project_members (
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    role_id    UUID        NOT NULL REFERENCES roles(id),
    status     VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_project_members_role_id ON project_members(role_id);
CREATE INDEX IF NOT EXISTS idx_project_members_status  ON project_members(status);

INSERT INTO project_members (project_id, user_id, role_id, joined_at)
SELECT DISTINCT ON (b.project_id, b.user_id) b.project_id, b.user_id, r.id, b.created_at
FROM (
    SELECT substring(resource_name from 10)::uuid AS project_id,
           substring(principal from 6)::uuid AS user_id,
           CASE role WHEN 'roles/owner' THEN 'OWNER' WHEN 'roles/developer' THEN 'DEVELOPER'
                     WHEN 'roles/viewer' THEN 'VIEWER' END AS legacy,
           CASE role WHEN 'roles/owner' THEN 3 WHEN 'roles/developer' THEN 2 ELSE 1 END AS rank,
           created_at
    FROM iam_bindings
    WHERE resource_name LIKE 'projects/%' AND resource_name NOT LIKE 'projects/%/%'
      AND principal LIKE 'user:%' AND condition IS NULL
      AND role IN ('roles/owner', 'roles/developer', 'roles/viewer')
) b
JOIN roles r ON r.name = b.legacy
JOIN users u ON u.id = b.user_id
ORDER BY b.project_id, b.user_id, b.rank DESC
ON CONFLICT DO NOTHING;
