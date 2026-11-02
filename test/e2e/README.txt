TESTS DE BOUT EN BOUT
=====================

Deux niveaux, ignorés par un simple go test.

1. VRAIE BASE (Postgres jetable, sans cluster)
----------------------------------------------

Écritures conditionnelles sur un vrai Postgres, avec toutes les migrations :

    bash scripts/iam_e2e_db.sh
    IAM_E2E=1 go test ./internal/modules/workloads/repository/ -run TestE2E -count=1 -v
    docker rm -f iam-e2e-pg

2. PILE COMPLÈTE (API, worker, Temporal, Postgres, cluster)
-----------------------------------------------------------

Contre une pile déjà démarrée. Créent de vrais projets et conteneurs, et les
suppriment à la fin. Lents : plusieurs minutes par scénario. Sur un serveur partagé,
un seul à la fois.

    E2E=1 E2E_URL=http://localhost:8080 E2E_EMAIL=... E2E_PASSWORD=... \
      go test ./test/e2e/ -count=1 -v -p 1 -timeout 90m

Un seul scénario : ajouter -run TestDeleteDuringInstall.

Variables :

    E2E_URL             adresse de l'API, sans /kmanager/v1
    E2E_TOKEN           jeton d'accès, ou E2E_EMAIL et E2E_PASSWORD
                        (compte de test sans second facteur)
    E2E_KUBECONFIG      cluster que le worker utilise ; sans lui, les restes dans le
                        cluster ne sont pas contrôlés
    E2E_IMAGE           image sans root écoutant sur 8080
    E2E_WORKER_KILL     commande qui tue le worker
    E2E_WORKER_START    commande qui le relance
                        (sans ces deux-là, TestWorkerKilledDuringInstall est ignoré)
    E2E_GIT_URL         dépôt Git public dont l'application écoute sur 8080
    E2E_GIT_REF         branche ou tag (sans E2E_GIT_URL, TestBuildFromGit est ignoré)

Exemple pour le worker :

    E2E_WORKER_KILL='docker kill <conteneur du worker>'
    E2E_WORKER_START='docker start <conteneur du worker>'

Ce que chaque scénario vérifie :

    TestLifecycle                 déployer, arrêter, relancer, supprimer, cluster distant
    TestDeleteDuringInstall       réponse rapide, Helm interrompu, aucun reste
    TestPatchDuringDelete         écriture refusée, sans erreur serveur
    TestNotReadyFailsOnce         une seule attente, puis FAILED ; une correction repart
    TestWorkerKilledDuringInstall reprise après la panne, release « pending » débloquée
    TestConcurrentPatches         un seul déploiement, la demande refusée sans trace
    TestBuildFromGit              build, envoi au registre, lancement

Ces scénarios ne valent preuve qu'une fois exécutés sur la pile visée : qu'ils
compilent, ou soient ignorés sans E2E=1, ne prouve rien.
