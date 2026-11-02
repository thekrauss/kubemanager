-- Spec déclarative des conteneurs : ce que la personne a demandé (sans
-- variables d'environnement ni secrets, qui restent dans le Secret
-- Kubernetes). Chaque déploiement, création ou mise à jour, est reconstruit
-- à partir d'elle : rien n'est perdu d'une version à l'autre.
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS spec JSONB;

-- Un nom par projet : il sert de release Helm dans l'espace du projet.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM workloads GROUP BY project_id, name HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'workloads : noms en double dans un même projet (release Helm partagée). Supprimez ou renommez les doublons avant cette migration : SELECT project_id, name, count(*) FROM workloads GROUP BY 1, 2 HAVING count(*) > 1';
    END IF;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS uq_workloads_project_name ON workloads (project_id, name);
