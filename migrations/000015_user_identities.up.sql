-- Comptes Google / GitHub liés à un compte betorun (docs/architecture/
-- auth.md §8). betorun parle directement aux fournisseurs ; l'annuaire
-- d'identité n'est plus dans le chemin du navigateur.
CREATE TABLE IF NOT EXISTS user_identities (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      VARCHAR(20)  NOT NULL,
    subject       VARCHAR(255) NOT NULL,
    email         VARCHAR(255) NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (provider, subject),
    UNIQUE (user_id, provider)
);
CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);
