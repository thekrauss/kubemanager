-- IAM phase A, étape A6 : journal d'audit (iam.md §11).
-- Append-only : un trigger refuse toute modification ou suppression, même
-- par le rôle applicatif. La purge de rétention (400 jours) passera par une
-- fonction dédiée exécutée par un rôle de maintenance, pas par l'application.

CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL    PRIMARY KEY,
    at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    principal   TEXT         NOT NULL,
    auth_method VARCHAR(20)  NOT NULL DEFAULT '',  -- bearer | apikey | apikey_v2
    api_key_id  VARCHAR(64)  NOT NULL DEFAULT '',
    action      TEXT         NOT NULL,             -- "POST /kmanager/v1/iam/policy"
    permission  TEXT         NOT NULL DEFAULT '',
    resource    TEXT         NOT NULL DEFAULT '',
    project_id  UUID,
    decision    VARCHAR(8)   NOT NULL,             -- ALLOW | DENY
    status      INTEGER      NOT NULL DEFAULT 0,   -- statut HTTP de la réponse
    client_ip   VARCHAR(45)  NOT NULL DEFAULT '',
    user_agent  TEXT         NOT NULL DEFAULT '',
    trace_id    VARCHAR(64)  NOT NULL DEFAULT '',
    metadata    JSONB        NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_audit_log_project   ON audit_log(project_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource  ON audit_log(resource text_pattern_ops, at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_principal ON audit_log(principal, at DESC);

CREATE OR REPLACE FUNCTION audit_log_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_audit_log_append_only ON audit_log;
CREATE TRIGGER trg_audit_log_append_only
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();

-- TRUNCATE contourne les triggers de ligne : on l'interdit aussi.
DROP TRIGGER IF EXISTS trg_audit_log_no_truncate ON audit_log;
CREATE TRIGGER trg_audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();
