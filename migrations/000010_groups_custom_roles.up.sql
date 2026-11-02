-- IAM phase B, étape B2 : groupes et rôles personnalisés (iam.md §4, §6.1).

-- Rôles personnalisés, définis par un client sur une organisation ou un
-- projet : "organizations/{o}/roles/{id}" ou "projects/{p}/roles/{id}".
-- Seules les permissions marquées « utilisables en rôle personnalisé ».
CREATE TABLE IF NOT EXISTS iam_custom_roles (
    name            VARCHAR(200) PRIMARY KEY,
    parent_resource VARCHAR(100) NOT NULL,
    role_id         VARCHAR(64)  NOT NULL,
    title           VARCHAR(100) NOT NULL,
    description     TEXT         NOT NULL DEFAULT '',
    permissions     TEXT[]       NOT NULL,
    etag            VARCHAR(40)  NOT NULL,
    created_by      TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_iam_custom_roles_parent ON iam_custom_roles(parent_resource);

-- Groupes d'une organisation : principal "group:{uuid}". Membres : personnes
-- et comptes de service (pas de groupes imbriqués).
CREATE TABLE IF NOT EXISTS iam_groups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    group_id        VARCHAR(63)  NOT NULL,
    display_name    VARCHAR(100) NOT NULL DEFAULT '',
    description     TEXT         NOT NULL DEFAULT '',
    created_by      TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_iam_groups_org_group
    ON iam_groups(organization_id, group_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS iam_group_members (
    group_id   UUID         NOT NULL REFERENCES iam_groups(id) ON DELETE CASCADE,
    principal  VARCHAR(100) NOT NULL,
    created_by TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, principal)
);
CREATE INDEX IF NOT EXISTS idx_iam_group_members_principal ON iam_group_members(principal);
