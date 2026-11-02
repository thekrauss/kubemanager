-- IAM phase B : identité des workloads (iam.md §7.2). Un workload déclaré
-- « exécuté comme » un compte de service reçoit un jeton Kubernetes projeté,
-- échangé auprès du STS. Les clés publiques qui signent ces jetons sont
-- rapportées par le watcher de chaque cluster (l'apiserver n'est pas joignable
-- depuis l'extérieur).
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS service_account_id UUID
    REFERENCES service_accounts(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS cluster_oidc_keys (
    cluster_id VARCHAR(100) PRIMARY KEY,
    issuer     VARCHAR(255) NOT NULL,
    jwks       JSONB        NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cluster_oidc_keys_issuer ON cluster_oidc_keys(issuer);
