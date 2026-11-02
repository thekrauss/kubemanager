-- IAM phase B, étape B3 : politiques d'organisation (iam.md §13).
-- Première contrainte : iam.requireMfa (double authentification exigée de
-- toute personne qui agit sur l'organisation ou ses projets).
CREATE TABLE IF NOT EXISTS iam_org_policies (
    resource_name   VARCHAR(100) NOT NULL,
    constraint_name VARCHAR(100) NOT NULL,
    enforced        BOOLEAN      NOT NULL DEFAULT FALSE,
    updated_by      TEXT         NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_name, constraint_name)
);
