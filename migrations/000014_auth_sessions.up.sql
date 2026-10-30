-- Authentification gérée par betorun (docs/architecture/auth.md) : Keycloak
-- garde les comptes et les mots de passe, betorun les sessions, le second
-- facteur et les emails. Aucun jeton ni secret n'est stocké en clair.

-- Sessions : une par connexion (navigateur ou CLI). Le jeton de
-- rafraîchissement tourne à chaque usage ; le précédent est conservé pour
-- détecter sa réutilisation (vol présumé : session révoquée).
CREATE TABLE IF NOT EXISTS auth_sessions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind             VARCHAR(10)  NOT NULL,
    refresh_hash     BYTEA        NOT NULL UNIQUE,
    previous_hash    BYTEA,
    rotated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    amr              VARCHAR(100) NOT NULL DEFAULT '',  -- méthodes (RFC 8176), séparées par des virgules
    auth_time        TIMESTAMPTZ  NOT NULL,
    mfa_at           TIMESTAMPTZ,
    ip               VARCHAR(64)  NOT NULL DEFAULT '',
    user_agent       VARCHAR(512) NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_used_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    idle_expires_at  TIMESTAMPTZ  NOT NULL,
    expires_at       TIMESTAMPTZ  NOT NULL,
    revoked_at       TIMESTAMPTZ,
    revoked_reason   VARCHAR(40)
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_auth_sessions_previous ON auth_sessions(previous_hash) WHERE previous_hash IS NOT NULL;

-- Seconds facteurs. TOTP : secret chiffré (AES-256-GCM, clé hors base) ;
-- last_used_step interdit de rejouer un code déjà accepté.
CREATE TABLE IF NOT EXISTS auth_factors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind            VARCHAR(20) NOT NULL,
    secret_enc      BYTEA       NOT NULL,
    confirmed_at    TIMESTAMPTZ,
    last_used_step  BIGINT      NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_auth_factors_user_kind ON auth_factors(user_id, kind);

-- Codes de secours : à usage unique, hachés (HMAC avec le pepper serveur).
CREATE TABLE IF NOT EXISTS auth_recovery_codes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  BYTEA       NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_auth_recovery_codes_user ON auth_recovery_codes(user_id);

-- Liens envoyés par email (vérification d'adresse, mot de passe oublié) :
-- à usage unique, hachés. Le compte n'a pas toujours de profil local à ce
-- stade : il est désigné par son identifiant d'annuaire.
CREATE TABLE IF NOT EXISTS auth_email_tokens (
    token_hash  BYTEA PRIMARY KEY,
    subject     VARCHAR(255) NOT NULL,
    email       VARCHAR(255) NOT NULL,
    purpose     VARCHAR(20)  NOT NULL,
    expires_at  TIMESTAMPTZ  NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_auth_email_tokens_subject ON auth_email_tokens(subject, purpose);
