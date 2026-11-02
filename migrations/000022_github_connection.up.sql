-- Connexion GitHub : installations de l'application par projet, liens
-- dépôt ↔ conteneur, idempotence des webhooks, builds déclenchés par le serveur.
CREATE TABLE IF NOT EXISTS github_installations (
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    installation_id BIGINT NOT NULL,
    account_login   VARCHAR(255) NOT NULL,
    account_type    VARCHAR(30) NOT NULL DEFAULT '',
    suspended       BOOLEAN NOT NULL DEFAULT false,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, installation_id)
);
CREATE INDEX IF NOT EXISTS idx_github_installations_installation ON github_installations (installation_id);

-- Le dépôt est identifié par repo_id : un nom change (renommage, transfert) et
-- peut être repris par un tiers. repo_full_name n'est qu'une copie d'affichage.
CREATE TABLE IF NOT EXISTS workload_git_links (
    workload_id     UUID PRIMARY KEY REFERENCES workloads(id) ON DELETE CASCADE,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    installation_id BIGINT NOT NULL,
    repo_id         BIGINT NOT NULL,
    repo_full_name  VARCHAR(255) NOT NULL,
    repo_private    BOOLEAN NOT NULL DEFAULT false,
    branch          VARCHAR(200) NOT NULL,
    context_dir     VARCHAR(200) NOT NULL DEFAULT '',
    dockerfile      VARCHAR(200) NOT NULL DEFAULT '',
    auto_deploy     BOOLEAN NOT NULL DEFAULT true,
    -- Vide : lien actif. Sinon : pourquoi le lien ne fonctionne plus
    -- (installation retirée ou suspendue, dépôt retiré de l'installation).
    disabled_reason VARCHAR(40) NOT NULL DEFAULT '',
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_workload_git_links_push ON workload_git_links (installation_id, repo_id, branch);

-- GitHub rejoue les livraisons : une livraison n'est traitée qu'une fois.
CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
    delivery_id VARCHAR(100) PRIMARY KEY,
    event       VARCHAR(50) NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_github_webhook_deliveries_received ON github_webhook_deliveries (received_at);

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS git_commit VARCHAR(40) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS git_repo_id BIGINT,
    ADD COLUMN IF NOT EXISTS git_installation_id BIGINT,
    ADD COLUMN IF NOT EXISTS deploy_workload_id UUID REFERENCES workloads(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS "trigger" VARCHAR(10) NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS deploy_state VARCHAR(20) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_builds_deploy_workload ON builds (deploy_workload_id, created_at DESC) WHERE deploy_workload_id IS NOT NULL;
