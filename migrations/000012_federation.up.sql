-- IAM phase B : fédération d'identité OIDC (iam.md §7.2). Une CI (GitHub
-- Actions, GitLab CI…) présente son jeton OIDC au STS ; une relation de
-- confiance l'autorise à agir comme un compte de service, sans secret stocké.
CREATE TABLE IF NOT EXISTS iam_federation_trusts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_account_id UUID         NOT NULL REFERENCES service_accounts(id) ON DELETE CASCADE,
    display_name       VARCHAR(100) NOT NULL DEFAULT '',
    issuer             VARCHAR(255) NOT NULL,
    subject_pattern    VARCHAR(255) NOT NULL,
    conditions         JSONB        NOT NULL DEFAULT '{}',
    last_used_at       TIMESTAMPTZ,
    created_by         TEXT         NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_iam_federation_trusts_issuer ON iam_federation_trusts(issuer);
CREATE INDEX IF NOT EXISTS idx_iam_federation_trusts_sa ON iam_federation_trusts(service_account_id);
