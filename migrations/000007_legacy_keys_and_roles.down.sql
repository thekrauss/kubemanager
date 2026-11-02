-- Retour arrière. Les échéances posées sur les clés legacy ne sont pas
-- annulées (la date d'origine n'est pas conservée).

CREATE TABLE IF NOT EXISTS roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(50) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS permissions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       VARCHAR(100) NOT NULL UNIQUE,
    name       VARCHAR(120) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id)
);

INSERT INTO roles (name) VALUES ('OWNER'), ('DEVELOPER'), ('VIEWER') ON CONFLICT DO NOTHING;
INSERT INTO permissions (slug) VALUES
    ('project:view'), ('project:edit'), ('project:delete'), ('project:members:manage'),
    ('k8s:workload:create'), ('k8s:workload:delete'), ('k8s:logs:view'), ('k8s:shell:exec')
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON
       r.name = 'OWNER'
    OR (r.name = 'DEVELOPER' AND p.slug IN ('project:view', 'project:edit', 'k8s:workload:create', 'k8s:logs:view'))
    OR (r.name = 'VIEWER'    AND p.slug IN ('project:view', 'k8s:logs:view'))
ON CONFLICT DO NOTHING;

DELETE FROM iam_bindings WHERE created_by = 'migration:000007';
UPDATE api_keys SET service_account_id = NULL WHERE service_account_id IS NOT NULL;
DELETE FROM service_accounts WHERE created_by = 'migration:000007';
ALTER TABLE IF EXISTS api_keys DROP COLUMN IF EXISTS service_account_id;
