-- IAM phase A, étape A5 : comptes de service et clés API v2 (iam.md §7, §8).

CREATE TABLE IF NOT EXISTS service_accounts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    -- Identifiant lisible, unique dans le projet : "ci-deployer".
    account_id   VARCHAR(30)  NOT NULL,
    display_name VARCHAR(100) NOT NULL DEFAULT '',
    description  TEXT         NOT NULL DEFAULT '',
    disabled     BOOLEAN      NOT NULL DEFAULT FALSE,
    created_by   TEXT         NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_accounts_project_account
    ON service_accounts(project_id, account_id) WHERE deleted_at IS NULL;

-- Clés API v2 : toujours rattachées à un compte de service. Le secret n'est
-- jamais stocké : seulement HMAC-SHA256(pepper serveur, secret).
CREATE TABLE IF NOT EXISTS api_keys_v2 (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_id             VARCHAR(16)  NOT NULL UNIQUE,  -- partie publique, affichable
    service_account_id UUID         NOT NULL REFERENCES service_accounts(id) ON DELETE CASCADE,
    display_name       VARCHAR(100) NOT NULL DEFAULT '',
    env                VARCHAR(8)   NOT NULL,         -- live | test
    secret_hmac        VARCHAR(64)  NOT NULL,
    restrictions       JSONB        NOT NULL DEFAULT '{}',
    expires_at         TIMESTAMPTZ  NOT NULL,
    revoked_at         TIMESTAMPTZ,
    last_used_at       TIMESTAMPTZ,
    last_used_ip       VARCHAR(45)  NOT NULL DEFAULT '',
    created_by         TEXT         NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_keys_v2_service_account ON api_keys_v2(service_account_id);
