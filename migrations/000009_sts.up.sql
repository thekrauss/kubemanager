-- IAM phase B, étape B1 : STS et clés de compte de service asymétriques
-- (iam.md §7.2, §7.3).

-- Clés de signature du STS. La clé privée est chiffrée (AES-256-GCM, clé
-- hors base) ; seule la clé publique est publiée (JWKS). Rotation
-- automatique : une nouvelle clé est publiée avant de servir, l'ancienne
-- reste publiée tant que des jetons signés par elle peuvent circuler.
CREATE TABLE IF NOT EXISTS iam_signing_keys (
    kid             VARCHAR(40)  PRIMARY KEY,
    algorithm       VARCHAR(10)  NOT NULL,
    public_jwk      JSONB        NOT NULL,
    private_key_enc BYTEA        NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    activate_at     TIMESTAMPTZ  NOT NULL,
    retire_at       TIMESTAMPTZ
);

-- Clés asymétriques des comptes de service : betorun ne garde que la clé
-- publique. Le client signe une assertion (RFC 7523) avec la clé privée.
CREATE TABLE IF NOT EXISTS service_account_keys (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_id             VARCHAR(40)  NOT NULL UNIQUE,
    service_account_id UUID         NOT NULL REFERENCES service_accounts(id) ON DELETE CASCADE,
    algorithm          VARCHAR(10)  NOT NULL,   -- ES256 | RS256
    public_key_pem     TEXT         NOT NULL,
    origin             VARCHAR(10)  NOT NULL,   -- generated | uploaded
    display_name       VARCHAR(100) NOT NULL DEFAULT '',
    valid_after        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    valid_before       TIMESTAMPTZ  NOT NULL,
    disabled_at        TIMESTAMPTZ,
    last_used_at       TIMESTAMPTZ,
    created_by         TEXT         NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_service_account_keys_sa ON service_account_keys(service_account_id);

-- Anti-rejeu des assertions : un jti ne sert qu'une fois, jusqu'à son expiration.
CREATE TABLE IF NOT EXISTS iam_assertion_jti (
    jti        VARCHAR(200) PRIMARY KEY,
    expires_at TIMESTAMPTZ  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_iam_assertion_jti_expires ON iam_assertion_jti(expires_at);
