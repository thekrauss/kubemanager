-- Variables d'environnement et secrets des conteneurs : chiffrés ici (la clé
-- est hors base), source de vérité du Secret Kubernetes que le worker écrit.
-- Les workflows ne transportent plus que le numéro de révision : aucune valeur
-- dans l'historique Temporal.
CREATE TABLE IF NOT EXISTS workload_variables (
    workload_id UUID NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    name        VARCHAR(128) NOT NULL,
    sealed      BYTEA NOT NULL,
    secret      BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workload_id, name)
);

-- 0 : conteneur antérieur, ses variables sont encore dans le Secret Kubernetes
-- (importées à sa prochaine modification).
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS env_revision BIGINT NOT NULL DEFAULT 0;
