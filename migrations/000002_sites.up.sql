-- Sites statiques : un site appartient à un projet, sert la version de son
-- déploiement actif (fichiers dans l'object store sous sites/<site>/<deploy>/)
-- sur <subdomain>.<base_domain> et sur ses domaines personnalisés vérifiés.

CREATE TABLE IF NOT EXISTS sites (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id           UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                 VARCHAR(63)  NOT NULL,
    subdomain            VARCHAR(63)  NOT NULL,
    spa_fallback         BOOLEAN      NOT NULL DEFAULT FALSE,
    -- EMPTY (aucun déploiement actif) | LIVE | DELETING
    status               VARCHAR(20)  NOT NULL DEFAULT 'EMPTY',
    active_deployment_id UUID,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_sites_project_name UNIQUE (project_id, name),
    CONSTRAINT uq_sites_subdomain UNIQUE (subdomain)
);
CREATE INDEX IF NOT EXISTS idx_sites_project_id ON sites(project_id);

CREATE TABLE IF NOT EXISTS site_deployments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id     UUID         NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    -- PENDING (archive reçue) | PROCESSING | READY | FAILED
    status      VARCHAR(20)  NOT NULL DEFAULT 'PENDING',
    error       TEXT         NOT NULL DEFAULT '',
    archive_key TEXT         NOT NULL DEFAULT '',
    file_count  INTEGER      NOT NULL DEFAULT 0,
    size_bytes  BIGINT       NOT NULL DEFAULT 0,
    created_by  UUID,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_site_deployments_site_id ON site_deployments(site_id, created_at DESC);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_sites_active_deployment') THEN
        ALTER TABLE sites
            ADD CONSTRAINT fk_sites_active_deployment
            FOREIGN KEY (active_deployment_id) REFERENCES site_deployments(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS site_domains (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id     UUID          NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    hostname    VARCHAR(253)  NOT NULL,
    -- Servi par l'edge (et certificat émis) uniquement une fois vérifié.
    verified    BOOLEAN       NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMPTZ,
    checked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_site_domains_hostname UNIQUE (hostname)
);
CREATE INDEX IF NOT EXISTS idx_site_domains_site_id ON site_domains(site_id);
