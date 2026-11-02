-- Domaines personnalisés des conteneurs. Une demande ne bloque personne : le
-- nom n'est réservé qu'une fois la propriété prouvée (enregistrement TXT).
CREATE TABLE IF NOT EXISTS workload_domains (
    id          UUID PRIMARY KEY,
    workload_id UUID NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    hostname    VARCHAR(253) NOT NULL,
    token       VARCHAR(64) NOT NULL,
    verified    BOOLEAN NOT NULL DEFAULT false,
    verified_at TIMESTAMPTZ,
    checked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workload_id, hostname)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_workload_domains_verified ON workload_domains (hostname) WHERE verified;
