-- KubeManager initial schema.
-- Source: internal/modules/auth/domain/entities.go,
--         internal/modules/workloads/domain/entity.go,
--         internal/modules/clusters/entity.go.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─────────────────────────────── users / auth ───────────────────────────────

CREATE TABLE users (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    keycloak_user_id VARCHAR(255) NOT NULL UNIQUE,
    login_id         VARCHAR(50)  UNIQUE,
    email            VARCHAR(255) NOT NULL UNIQUE,
    full_name        VARCHAR(255) NOT NULL,
    avatar_url       VARCHAR(512) NOT NULL DEFAULT '',
    country          CHAR(2)      NOT NULL DEFAULT '',
    phone_number     VARCHAR(20),
    role             TEXT         NOT NULL DEFAULT 'USER',
    status           VARCHAR(20)  NOT NULL DEFAULT 'active',
    is_active        BOOLEAN      NOT NULL DEFAULT TRUE,
    is_verified      BOOLEAN      NOT NULL DEFAULT FALSE,
    last_login_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);
CREATE INDEX idx_users_email           ON users(email);
CREATE INDEX idx_users_keycloak_user   ON users(keycloak_user_id);
CREATE INDEX idx_users_status          ON users(status);
CREATE INDEX idx_users_deleted_at      ON users(deleted_at);

CREATE TABLE user_sessions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash VARCHAR(128) NOT NULL,
    user_agent         VARCHAR(512) NOT NULL DEFAULT '',
    client_ip          VARCHAR(45)  NOT NULL DEFAULT '',
    client_type        VARCHAR(20)  NOT NULL DEFAULT 'web',
    is_blocked         BOOLEAN      NOT NULL DEFAULT FALSE,
    expires_at         TIMESTAMPTZ  NOT NULL,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_user_sessions_user_id             ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_refresh_token_hash  ON user_sessions(refresh_token_hash);

CREATE TABLE revoked_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────── RBAC ───────────────────────────────────────

CREATE TABLE roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(50) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE permissions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       VARCHAR(100) NOT NULL UNIQUE,
    name       VARCHAR(120) NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (role_id, permission_id)
);

-- ─────────────────────────────── projects ───────────────────────────────────

CREATE TABLE projects (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(255) NOT NULL UNIQUE,
    description   TEXT         NOT NULL DEFAULT '',
    cpu_limit     VARCHAR(20)  NOT NULL DEFAULT '2000m',
    memory_limit  VARCHAR(20)  NOT NULL DEFAULT '4Gi',
    storage_limit VARCHAR(20)  NOT NULL DEFAULT '10Gi',
    status        VARCHAR(20)  NOT NULL DEFAULT 'PENDING',
    current_phase VARCHAR(50)  NOT NULL DEFAULT 'DB_INITIALIZING',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
CREATE INDEX idx_projects_status     ON projects(status);
CREATE INDEX idx_projects_deleted_at ON projects(deleted_at);

CREATE TABLE project_members (
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    role_id    UUID        NOT NULL REFERENCES roles(id),
    status     VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX idx_project_members_role_id ON project_members(role_id);
CREATE INDEX idx_project_members_status  ON project_members(status);

-- ─────────────────────────────── api_keys ───────────────────────────────────

CREATE TABLE api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID         NOT NULL,
    user_id     UUID         NOT NULL,
    cluster_id  VARCHAR(100) NOT NULL DEFAULT '',
    kind        VARCHAR(20)  NOT NULL DEFAULT 'backend',
    name        VARCHAR(255) NOT NULL DEFAULT '',
    prefix      VARCHAR(100) NOT NULL,
    key_hash    VARCHAR(128) NOT NULL,
    scopes      TEXT[]       NOT NULL DEFAULT '{}',
    expires_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    is_active   BOOLEAN      NOT NULL DEFAULT TRUE,
    usage_count INTEGER      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX idx_api_keys_project_id ON api_keys(project_id);
CREATE INDEX idx_api_keys_user_id    ON api_keys(user_id);
CREATE INDEX idx_api_keys_cluster_id ON api_keys(cluster_id);
CREATE INDEX idx_api_keys_kind       ON api_keys(kind);
CREATE INDEX idx_api_keys_prefix     ON api_keys(prefix);

-- ─────────────────────────────── workloads ──────────────────────────────────

CREATE TABLE workloads (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          UUID         NOT NULL,
    name                VARCHAR(255) NOT NULL,
    namespace           VARCHAR(255) NOT NULL,
    chart_name          VARCHAR(255) NOT NULL,
    version             VARCHAR(50)  NOT NULL,
    image               VARCHAR(512) NOT NULL,
    status              VARCHAR(50)  NOT NULL DEFAULT 'STARTING',
    current_phase       VARCHAR(50)  NOT NULL DEFAULT 'HELM_CHART_LOADING',
    replicas            INTEGER      NOT NULL DEFAULT 1,
    external_url        TEXT         NOT NULL DEFAULT '',
    values              TEXT         NOT NULL DEFAULT '',
    cpu_limit           VARCHAR(20)  NOT NULL DEFAULT '200m',
    memory_limit        VARCHAR(20)  NOT NULL DEFAULT '256Mi',
    cpu_request         VARCHAR(20)  NOT NULL DEFAULT '100m',
    memory_request      VARCHAR(20)  NOT NULL DEFAULT '128Mi',
    target_port         INTEGER      NOT NULL DEFAULT 8080,
    protocol            VARCHAR(10)  NOT NULL DEFAULT 'TCP',
    tls_enabled         BOOLEAN      NOT NULL DEFAULT FALSE,
    liveness_path       VARCHAR(100) NOT NULL DEFAULT '/',
    labels              TEXT         NOT NULL DEFAULT '',
    last_workflow_id    VARCHAR(100) NOT NULL DEFAULT '',
    persistence_enabled BOOLEAN      NOT NULL DEFAULT FALSE,
    storage_size        VARCHAR(20)  NOT NULL DEFAULT '1Gi',
    storage_class       VARCHAR(50)  NOT NULL DEFAULT 'local-path',
    access_mode         VARCHAR(20)  NOT NULL DEFAULT 'ReadWriteOnce',
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_workloads_project_id ON workloads(project_id);

-- ─────────────────────────────── clusters ───────────────────────────────────

CREATE TABLE clusters (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   VARCHAR(100) NOT NULL UNIQUE,
    version      VARCHAR(50)  NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    enrolled_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
