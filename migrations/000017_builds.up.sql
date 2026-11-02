-- Builds : construction d'une image depuis le code (archive envoyée ou dépôt
-- Git). L'image obtenue reste dans le registre de la plateforme, sous le
-- projet ; la déployer reste une étape distincte.
CREATE TABLE IF NOT EXISTS builds (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        VARCHAR(63) NOT NULL,
    source_type VARCHAR(10) NOT NULL,
    git_url     TEXT NOT NULL DEFAULT '',
    git_ref     TEXT NOT NULL DEFAULT '',
    context_dir TEXT NOT NULL DEFAULT '',
    dockerfile  TEXT NOT NULL DEFAULT 'Dockerfile',
    status      VARCHAR(20) NOT NULL,
    failure     VARCHAR(30) NOT NULL DEFAULT '',
    image       TEXT NOT NULL,
    digest      VARCHAR(80) NOT NULL DEFAULT '',
    created_by  UUID,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_builds_project_created ON builds (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_builds_project_active ON builds (project_id) WHERE status IN ('QUEUED', 'BUILDING');
