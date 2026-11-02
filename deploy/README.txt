DÉPLOYER BETORUN EN PRODUCTION
==============================

Serveur 1 : API, worker, console, Postgres, Redis, Temporal, Keycloak, Centrifugo,
            registre, stockage objet, static-edge, Caddy (docker compose).
Serveur 2 : k3s avec son Traefik intégré (conteneurs des clients et builds).

Dans ce guide, <domaine> est votre domaine, <IP1> et <IP2> les IP publiques.


0. CE QUI A ÉTÉ VÉRIFIÉ, ET CE QUI NE L'A JAMAIS ÉTÉ
----------------------------------------------------

Vérifié sur une seule machine de développement, le 2026-11-02 :
  - construction et contenu des images API/worker, static-edge, console ;
  - refus de démarrer d'une configuration incomplète ;
  - docker-compose.prod.yaml service par service : Postgres et ses rôles, Redis,
    Temporal (schéma, serveur, espace de noms), Keycloak en mode start et son
    bootstrap, Centrifugo (origines), registre (realm), SeaweedFS, API, worker,
    static-edge, migrations par l'API seule, passage en panne de Redis ;
  - routes du Caddyfile en HTTP sur le réseau interne : blocage des sondes,
    compression, adresse réelle du client ;
  - RBAC, chart, redirection HTTPS, NetworkPolicy sur un k3s 1.36.5 jetable,
    avec un solveur HTTP-01 SIMULÉ (mêmes labels, même forme d'Ingress).

JAMAIS TESTÉ EN VRAI, à surveiller au premier déploiement :
  - HTTPS réel, certificats Let's Encrypt, TLS à la demande de Caddy ;
  - cert-manager réel et son solveur HTTP-01 ;
  - les deux serveurs ensemble : API vers k3s, build, push et pull du registre
    par son nom public, de bout en bout ;
  - tous les services démarrés en même temps, console et Caddy compris : la consommation mémoire réelle ;
  - la console à travers Caddy, avec son extra_hosts ;
  - l'envoi d'emails par un vrai SMTP ;
  - le job deploy de la CI (jamais déclenché) ;
  - la restauration d'une sauvegarde (à répéter avant d'en avoir besoin).


1. PRÉREQUIS
------------

Serveur 1 : Linux, Docker Engine et le plugin compose, 6 Go de RAM conseillés
  (somme des plafonds : environ 5,7 Go ; consommation réelle à mesurer), 8 Go avec
  le profil observability. Prévoir du disque pour les images et les sites.
Serveur 2 : Linux, 4 Go de RAM au moins (un build peut en prendre 2).
Machine de construction de la console : Docker. Pas le serveur 1 : next build
  demande environ 2 Go.
À préparer :
  - un compte SMTP (hôte, port, utilisateur, mot de passe, adresse d'expédition) ;
  - les emails du staff (PLATFORM_ADMINS) ;
  - un dépôt d'images (Docker Hub) ; la CI publie thekrauss/kubemanager et
    thekrauss/static-edge sous le tag du commit.


2. DNS
------

  Nom                       Type    Valeur   Rôle
  app.<domaine>             A       <IP1>    console
  api.<domaine>             A       <IP1>    API
  registry.<domaine>        A       <IP1>    registre d'images
  realtime.<domaine>        A       <IP1>    Centrifugo (wss)
  edge.<domaine>            A       <IP1>    cible CNAME des domaines de sites
  *.sites.<domaine>         A       <IP1>    sites statiques

Keycloak n'a aucun nom public, et n'en aura pas.

Conteneurs des clients : <conteneur>-<projet>.<IP2>.sslip.io, sans enregistrement
à créer. Leurs domaines personnalisés pointent vers <IP2>.

PAS D'ENREGISTREMENT AAAA vers le serveur 1 tant que le réseau Docker n'a pas
IPv6 : un client en IPv6 passerait par le proxy de Docker, et Caddy verrait
l'adresse de la passerelle au lieu de la sienne. Tous ces clients partageraient
alors les mêmes limites.

Créer le DNS AVANT de démarrer Caddy : il demande ses certificats au démarrage,
et Let's Encrypt limite les échecs répétés.


3. PARE-FEU
-----------

Serveur 1, ouvert à tous : 80/tcp, 443/tcp, 443/udp. SSH pour l'administration.
  Docker contourne ufw : un port publié par compose est ouvert quoi que dise ufw.
  C'est pourquoi le compose de production ne publie que ceux de Caddy.
  Sortant : 6443 vers <IP2>, SMTP, 443 (Let's Encrypt, GitHub).

Serveur 2 :
  80/tcp, 443/tcp   ouverts à tous (Traefik)
  6443/tcp          ouvert UNIQUEMENT à <IP1>
  SSH               pour l'administration ; rien d'autre.

    sudo ufw default deny incoming
    sudo ufw allow OpenSSH
    sudo ufw allow 80/tcp
    sudo ufw allow 443/tcp
    sudo ufw allow from <IP1> to any port 6443 proto tcp
    sudo ufw enable

Ce que le serveur 2 doit joindre sur le serveur 1 : uniquement 443, par les noms
publics api.<domaine> (archives des builds, jetons du registre et du STS) et
registry.<domaine> (push et pull). Aucun autre port n'est à ouvrir.

Si les deux serveurs sont sur un même réseau privé et que ces noms y résolvent en
adresses privées : les NetworkPolicy des builds bloquent les plages privées, il
faudrait alors BUILDS_EGRESS_ALLOW_CIDRS dans .env.prod. Le plus simple est de garder
les IP publiques.


4. SERVEUR 2 : K3S
------------------

    curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='v1.36.5+k3s1' \
      INSTALL_K3S_EXEC='server --secrets-encryption --tls-san <IP2> --write-kubeconfig-mode 600' sh -

    sudo k3s secrets-encrypt status      # attendu : Enabled
    sudo kubectl -n kube-system rollout status deploy/traefik
    sudo kubectl get crd middlewares.traefik.io

Copier le dossier deploy/k8s sur le serveur 2.

cert-manager et l'émetteur (nom attendu par le chart : letsencrypt-prod) :

    sudo kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.21.3/cert-manager.yaml
    sudo kubectl -n cert-manager rollout status deploy/cert-manager-webhook
    sed 's/ops@REMPLACER.invalid/ops@<domaine>/' cluster-issuer.yaml | sudo kubectl apply -f -
    sudo kubectl get clusterissuer letsencrypt-prod     # READY=True

Compte de la plateforme, à droits réduits (rbac.yaml explique ses limites) :

    sudo kubectl apply -f rbac.yaml

registries.yaml n'est pas nécessaire : le registre a un certificat public et les
pods reçoivent leurs identifiants par des imagePullSecrets.

À valider sur ce serveur (jamais testé) : BuildKit sans root peut être bloqué par
AppArmor sur Ubuntu 23.10 et suivantes (kernel.apparmor_restrict_unprivileged_userns).
Si un build échoue au démarrage, regarder ce réglage en premier.


5. SERVEUR 1 : INSTALLATION
---------------------------

    sudo mkdir -p /opt/betorun && sudo chown $USER /opt/betorun
    git clone <dépôt kubemanager> /opt/betorun/kubemanager
    cd /opt/betorun/kubemanager
    sudo scripts/generate_prod_secrets.sh /opt/betorun/secrets

Le script écrit .env.prod (600), le stockage objet et le certificat des jetons du
registre, sans rien afficher. Il refuse d'écraser un fichier existant.

    !!! IAM_API_KEY_PEPPER et IAM_STS_KEY_ENCRYPTION_KEY SONT DÉFINITIFS dès le
    !!! premier démarrage. Changer le pepper invalide toutes les clés API. Changer
    !!! la clé STS rend illisibles les clés de signature et les variables chiffrées
    !!! des conteneurs. Sauvegarder /opt/betorun/secrets hors du serveur MAINTENANT.

Compléter /opt/betorun/secrets/.env.prod : remplacer example.com partout, puis
SERVER2_IP, PLATFORM_ADMINS, SMTP_*, MAIL_FROM_EMAIL, ACME_EMAIL et les images.
L'API refuse de démarrer tant qu'il reste une valeur d'exemple.

L'application ne lit que les variables KUBEMANAGER_*. docker-compose.prod.yaml les
alimente depuis .env.prod (DB_PASSWORD devient KUBEMANAGER_DATABASE_PASSWORD, etc.) :
une seule valeur à tenir. Tous les secrets sont en hexadécimal, car le DSN de la
base est construit sans échappement ; seule exception, la clé STS, en base64.

Kubeconfig, depuis le poste qui a accès aux deux serveurs :

    ssh serveur2 'sudo bash make-kubeconfig.sh <IP2>' > /tmp/kubeconfig.betorun
    scp /tmp/kubeconfig.betorun serveur1:/opt/betorun/secrets/kubeconfig && rm /tmp/kubeconfig.betorun
    # sur le serveur 1 : l'image tourne sous l'UID 10001
    sudo chown 10001 /opt/betorun/secrets/kubeconfig && sudo chmod 600 /opt/betorun/secrets/kubeconfig

Images. L'API et static-edge viennent de la CI (ou d'un docker build local). Pour
la console, sur une autre machine, depuis kweb ; les valeurs sont figées dans le
JavaScript, donc une image par domaine :

    docker build \
      --build-arg NEXT_PUBLIC_API_BASE_URL=https://api.<domaine>/kmanager/v1 \
      --build-arg NEXT_PUBLIC_SITES_BASE_DOMAIN=sites.<domaine> \
      -t thekrauss/kweb:<sha> .
    docker push thekrauss/kweb:<sha>

Toutes les commandes suivantes passent par deploy/compose.sh, qui charge les
fichiers d'environnement. Si le dossier n'est pas /opt/betorun/secrets, exporter
BETORUN_SECRETS_DIR.


6. ORDRE DE DÉMARRAGE
---------------------

a) Base et Keycloak :

    deploy/compose.sh up -d --wait k8sdb keycloak

b) Bootstrap de Keycloak (realm, clients). Rejouable. Il écrit les secrets des
   clients dans keycloak.env sans les afficher :

    deploy/compose.sh --profile bootstrap up keycloak-bootstrap

   Keycloak n'envoie aucun email en production (KEYCLOAK_SMTP_ENABLED=false) : c'est
   le backend qui s'en charge. Ne jamais lancer scripts/keycloak/bootstrap_platform.sh
   avec ses valeurs par défaut : localhost, MailHog, admin/admin.

c) Tout le reste. compose respecte les dépendances : schéma Temporal, Temporal,
   espace de noms, API (seule à appliquer les migrations), worker, droits de
   static-edge, static-edge, console, Caddy :

    deploy/compose.sh up -d
    deploy/compose.sh ps

Profils, désactivés par défaut. Accès par tunnel SSH uniquement (ports limités à
la boucle locale), par exemple ssh -L 3000:127.0.0.1:3000 serveur1 :

    deploy/compose.sh --profile observability up -d   # Grafana, Prometheus, Loki, Jaeger (~1,5 Go)
    deploy/compose.sh --profile tools up -d           # temporal-ui, MailHog

Pour activer les traces : TRACING_JAEGER_ENDPOINT=jaeger:4317 dans .env.prod.


7. VÉRIFICATIONS
----------------

Serveur 1 :

    deploy/compose.sh ps                 # tout healthy ; les étapes uniques en Exited (0)
    deploy/compose.sh exec api wget -qO- http://127.0.0.1:8080/api/v1/ready
    deploy/compose.sh logs caddy | grep -i 'certificate obtained'

Depuis l'extérieur :

    curl -I  https://app.<domaine>/                       # 200, en-tête HSTS
    curl -sI https://api.<domaine>/api/v1/health          # 200
    curl -sI https://api.<domaine>/metrics                # 404
    curl -sI https://api.<domaine>/api/v1/ready           # 404
    curl -sI -H 'Accept-Encoding: zstd' https://api.<domaine>/kmanager/v1/openapi.json   # content-encoding: zstd
    curl -sI https://registry.<domaine>/v2/               # 401, realm en https://api.<domaine>/...
    curl -sI https://realtime.<domaine>/api               # 404
    # doivent échouer, de l'extérieur et depuis le serveur 2 :
    nc -zw3 <IP1> 5432 ; nc -zw3 <IP1> 7233 ; nc -zw3 <IP1> 8080 ; nc -zw3 <IP1> 9100
    # doit échouer sauf depuis le serveur 1 :
    nc -zw3 <IP2> 6443

Adresse réelle des clients (conditionne les limites de connexion) : se connecter
à la console, ouvrir la page des sessions actives. L'IP affichée doit être la vôtre,
pas 172.30.0.x ni celle du serveur.

Chaîne complète : créer un compte (email reçu ?), déployer un site, un conteneur
avec TLS, un build ; surveiller sur le serveur 2 :

    sudo kubectl get certificate,challenge,order -A

Le solveur HTTP-01 doit passer malgré la redirection : son Ingress a un chemin plus
long, donc un routeur prioritaire ; son pod n'est pas sélectionné par la NetworkPolicy
du chart. Vérifié sur k3s avec un solveur simulé, À CONFIRMER avec le vrai cert-manager.
Le quota du projet peut aussi refuser le pod du solveur s'il est plein : regarder
les événements de l'espace de noms.

Limites Let's Encrypt : sslip.io est partagé avec le monde entier, ses certificats
peuvent être refusés par manque de quota. Si cela arrive, c'est sur les domaines
personnalisés qu'il faut s'appuyer.


8. ADRESSE DES CLIENTS ET RATE LIMIT
------------------------------------

server.trusted_proxies vaut 172.30.0.10 (Caddy) et 172.30.0.11 (console), IP fixes
du sous-réseau 172.30.0.0/24 défini dans le compose. Ne jamais y mettre 0.0.0.0/0 :
un client choisirait son IP et contournerait toutes les limites (l'application le
refuse, comme toute plage plus large qu'un /16).

Ordre dans l'API : proxies de confiance, puis ClientIP, puis rate limit. Sans proxies
corrects, tous les clients partageraient un seul compteur. Défaut : 600 requêtes par
minute et par IP, sur toutes les routes publiques comprises (/registry/token,
/builds/:id/source, /iam/token, webhook GitHub) ; réglable par RATE_LIMIT_PER_MINUTE.

Deux situations partagent une même IP, à surveiller (réponses 429) :
  - les abonnés d'un opérateur mobile derrière un même NAT ;
  - tous les pods et nœuds du serveur 2 : un conteneur client très bavard vers l'API
    pourrait épuiser le compteur utilisé par les pulls d'images et les builds.


9. SAUVEGARDES
--------------

À sauvegarder hors du serveur, et chiffré :
  - /opt/betorun/secrets : sans lui, les données sont inutilisables ;
  - Postgres, chaque jour (betorun, keycloak, temporal) :

    deploy/compose.sh exec -T k8sdb sh -c 'pg_dumpall -U "$POSTGRES_USER"' \
      | gzip > /var/backups/betorun/pg-$(date +%F).sql.gz

  - le stockage objet (sites, archives) et le registre. Volumes à copier à froid :
    arrêter le service, ou accepter une copie incohérente pour les fichiers en cours :

    deploy/compose.sh stop seaweedfs
    docker run --rm -v betorun_s3data:/data:ro -v /var/backups/betorun:/out alpine:3.23.7 \
      tar czf /out/s3-$(date +%F).tgz -C /data .
    deploy/compose.sh start seaweedfs

    Même commande pour betorun_registry-data (images reconstructibles) et
    betorun_caddy-data (certificats, pour éviter de les redemander).

Restauration, à répéter à blanc au moins une fois (jamais testée) : volume Postgres
vide, puis psql -U "$POSTGRES_USER" -d postgres < sauvegarde ; volumes restaurés par
tar ; mêmes fichiers de secrets. Le script d'initialisation ne rejoue pas sur un
volume déjà rempli : changer un mot de passe de service demande un ALTER ROLE.


10. MISES À JOUR
----------------

Automatique : mettre la variable de dépôt GitHub DEPLOY_ENABLED à true et les secrets
VPS_HOST, VPS_USER, VPS_SSH_KEY, DOCKER_USERNAME, DOCKER_PASSWORD. Tant que
DEPLOY_ENABLED n'est pas à true, la CI publie les images mais ne déploie rien.
Le dépôt doit être cloné dans /opt/betorun/kubemanager et l'utilisateur VPS_USER
doit pouvoir écrire /opt/betorun/secrets/images.env et lire .env.prod.

Manuelle : écrire le tag du commit dans /opt/betorun/secrets/images.env :

    KUBEMANAGER_IMAGE=thekrauss/kubemanager:<sha>
    STATIC_EDGE_IMAGE=thekrauss/static-edge:<sha>

puis, API d'abord (elle applique les migrations) :

    git checkout <sha>
    deploy/compose.sh pull api worker static-edge
    deploy/compose.sh up -d --no-deps api
    deploy/compose.sh up -d --no-deps worker static-edge

Retour arrière : remettre l'ancien tag. Les migrations ne sont pas annulées : une
sauvegarde avant toute mise à jour qui en contient est indispensable.

!!! VERSION INCOMPATIBLE AVEC LES EXÉCUTIONS EN COURS !!!
Le code des workflows de déploiement de cette version a changé de façon incompatible
avec les exécutions en cours. Aucun workflow workload-deploy-* ou workload-update-*
ne doit tourner au moment de la mise à jour vers cette version. Vérifier avec :

    deploy/compose.sh exec temporal sh -c 'temporal workflow list --address "$(hostname -i | cut -d" " -f1):7233" --query "ExecutionStatus=\"Running\""'

Sans objet pour une première installation.

Images d'infrastructure : versions exactes dans docker-compose.prod.yaml, à monter
une par une, sauvegarde faite. Temporal : temporal-schema rejoue les migrations de
schéma à chaque up. Keycloak est en 25.0.6, comme le développement ; une version plus
récente existe et mérite d'être prévue. Postgres : un changement de version majeure
demande pg_dump et restauration.


11. LIMITES CONNUES
-------------------

- UNE SEULE INSTANCE D'API pour l'instant : la liste de révocation des jetons par JTI
  est locale à chaque processus (cache Ristretto, router/dependencies.go). Avec
  plusieurs instances, une déconnexion ne serait pas vue par les autres.
- Pas de plafond de taille de requête vers les conteneurs des clients sous Traefik.
  Le seul moyen, le middleware buffering (ingress.traefik.maxBodyBytes du chart),
  retient aussi les réponses jusqu'à leur fin. Mesuré sur k3s : un flux SSE arrive
  d'un bloc à la fin. Il reste donc désactivé.
- Les conteneurs clients voient l'adresse de Traefik plutôt que celle du visiteur
  tant que le service Traefik n'est pas en externalTrafficPolicy: Local (non testé).
- Sites : un certificat par sous-domaine, limite Let's Encrypt de 50 par semaine sur
  le domaine. Pour aller au-delà : certificat joker et image Caddy avec module DNS
  (docker/caddy/Caddyfile.prod.example).
- Le code retombe sur nginx si KUBEMANAGER_KUBERNETES_INGRESS_CLASS manque
  (activities/constants.go) ; le compose de production et la validation l'imposent.
- Le watcher du cluster (dépôt watcher) ne fait pas partie de ce guide. CLUSTER_ID reste
  vide tant qu'il n'est pas enrôlé : l'identité des workloads est alors désactivée.
