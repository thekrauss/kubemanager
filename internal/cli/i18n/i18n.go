// Package i18n : messages de la CLI en français et en anglais. La langue
// vient de BETORUN_LANG, sinon de LC_ALL / LC_MESSAGES / LANG ; français
// par défaut. Elle est aussi transmise à l'API (Accept-Language), qui
// traduit ses propres messages.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Lang : "fr" ou "en".
func Lang() string {
	for _, v := range []string{"BETORUN_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		val := strings.ToLower(os.Getenv(v))
		switch {
		case strings.HasPrefix(val, "en"):
			return "en"
		case strings.HasPrefix(val, "fr"):
			return "fr"
		}
	}
	return "fr"
}

// T : message traduit (formaté avec args si fourni).
func T(key string, args ...any) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	text := m[0]
	if Lang() == "en" {
		text = m[1]
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

// messages : clé → {français, anglais}.
var messages = map[string][2]string{
	"root.short": {"Hébergez vos sites et vos conteneurs sur betorun", "Host your sites and containers on betorun"},

	"err.no_api":          {"URL de l'API inconnue : lancez `betorun login --api <url>` ou définissez BETORUN_API_URL", "unknown API URL: run `betorun login --api <url>` or set BETORUN_API_URL"},
	"err.not_logged_in":   {"vous n'êtes pas connecté : lancez `betorun login`", "you are not logged in: run `betorun login`"},
	"err.session_expired": {"votre session a expiré : lancez `betorun login`", "your session has expired: run `betorun login`"},
	"err.no_project":      {"aucun projet : précisez --project ou lancez `betorun login`", "no project: pass --project or run `betorun login`"},
	"err.step_up":         {"cette action exige une double authentification récente : lancez `betorun login --mfa` puis recommencez", "this action requires recent two-factor authentication: run `betorun login --mfa`, then try again"},
	"err.forbidden":       {"accès refusé : %s", "access denied: %s"},
	"err.api":             {"l'API a répondu %d : %s", "the API answered %d: %s"},
	"err.network":         {"impossible de joindre l'API (%s) : %v", "cannot reach the API (%s): %v"},

	"login.short":      {"Se connecter (code à valider dans le navigateur)", "Log in (code to confirm in your browser)"},
	"login.open":       {"Pour vous connecter, ouvrez :\n\n    %s\n\net vérifiez que le code affiché est :  %s\n", "To log in, open:\n\n    %s\n\nand check that the code shown is:  %s\n"},
	"login.waiting":    {"En attente de votre validation…", "Waiting for your confirmation…"},
	"login.expired":    {"le code a expiré : relancez `betorun login`", "the code has expired: run `betorun login` again"},
	"login.denied":     {"connexion refusée dans le navigateur", "login denied in the browser"},
	"login.disabled":   {"la connexion de la CLI n'est pas activée sur cette plateforme", "CLI login is not enabled on this platform"},
	"login.ok":         {"Connecté en tant que %s.", "Logged in as %s."},
	"login.project":    {"Projet par défaut : %s", "Default project: %s"},
	"login.no_project": {"Vous n'avez pas encore de projet : créez-le dans la console.", "You have no project yet: create it in the console."},

	"login.step_up":       {"Cette action exige de confirmer votre identité (double authentification).", "This action requires you to confirm your identity (two-factor authentication)."},
	"login.browser_open":  {"Confirmez dans votre navigateur :\n\n    %s\n", "Confirm in your browser:\n\n    %s\n"},
	"login.browser_done":  {"Identité confirmée. Vous pouvez fermer cet onglet et revenir au terminal.", "Identity confirmed. You can close this tab and go back to the terminal."},
	"login.browser_state": {"réponse de connexion inattendue (state invalide) : recommencez", "unexpected login response (invalid state): try again"},
	"login.browser_error": {"connexion refusée : %s (%s)", "login refused: %s (%s)"},
	"flag.mfa":            {"se connecter avec double authentification (navigateur), pour les actions sensibles", "log in with two-factor authentication (browser), for sensitive actions"},

	"logout.short": {"Se déconnecter (révoque la session)", "Log out (revokes the session)"},
	"logout.ok":    {"Déconnecté.", "Logged out."},
	"logout.none":  {"Aucune session ouverte.", "No open session."},

	"whoami.short":       {"Afficher le compte et le projet courants", "Show the current account and project"},
	"whoami.account":     {"Compte      %s", "Account     %s"},
	"whoami.credentials": {"Accès       %s", "Credentials %s"},
	"whoami.principal":   {"Identité    %s", "Identity    %s"},
	"whoami.project":     {"Projet      %s", "Project     %s"},
	"whoami.mfa_on":      {"Double auth.  oui", "Two-factor  yes"},
	"whoami.mfa_off":     {"Double auth.  non", "Two-factor  no"},
	"whoami.api":         {"API         %s", "API         %s"},

	"sites.short":             {"Sites statiques", "Static sites"},
	"sites.list.short":        {"Lister les sites du projet", "List the project's sites"},
	"sites.list.empty":        {"Aucun site. Publiez-en un : betorun sites deploy ./dist --site mon-site --create", "No site. Publish one: betorun sites deploy ./dist --site my-site --create"},
	"sites.deploy.short":      {"Publier un dossier (ex. ./dist) sur un site", "Publish a folder (e.g. ./dist) to a site"},
	"sites.deploy.packing":    {"Préparation de %s…", "Packing %s…"},
	"sites.deploy.packed":     {"%d fichiers, %s compressés", "%d files, %s compressed"},
	"sites.deploy.creating":   {"Création du site %s…", "Creating site %s…"},
	"sites.deploy.uploading":  {"Envoi…", "Uploading…"},
	"sites.deploy.processing": {"Publication en cours…", "Publishing…"},
	"sites.deploy.ok":         {"En ligne : %s", "Live: %s"},
	"sites.deploy.failed":     {"la publication a échoué : %s", "publishing failed: %s"},
	"sites.deploy.timeout":    {"la publication prend plus de temps que prévu ; suivez-la avec `betorun sites list`", "publishing is taking longer than expected; follow it with `betorun sites list`"},
	"sites.deploy.not_found":  {"le site %q n'existe pas : ajoutez --create pour le créer", "site %q does not exist: add --create to create it"},
	"sites.deploy.empty":      {"le dossier ne contient aucun fichier à publier", "the folder contains no file to publish"},
	"sites.deploy.need_site":  {"précisez le site : --site <nom>", "specify the site: --site <name>"},

	"arg.dir": {"<dossier>", "<folder>"},

	"arg.name":   {"<nom>", "<name>"},
	"arg.host":   {"<domaine>", "<domain>"},
	"arg.member": {"<membre>", "<member>"},
	"arg.role":   {"<rôle>", "<role>"},
	"flag.yes":   {"ne pas demander de confirmation", "do not ask for confirmation"},

	"wl.short":          {"Conteneurs (images Docker)", "Containers (Docker images)"},
	"wl.not_found":      {"aucun conteneur nommé %q dans ce projet", "no container named %q in this project"},
	"wl.list.short":     {"Lister les conteneurs du projet", "List the project's containers"},
	"wl.list.empty":     {"Aucun conteneur. Déployez-en un : betorun workloads deploy --name api --image ghcr.io/vous/api:1.0", "No container. Deploy one: betorun workloads deploy --name api --image ghcr.io/you/api:1.0"},
	"wl.deploy.short":   {"Déployer une image Docker", "Deploy a Docker image"},
	"wl.deploy.need":    {"précisez --name et --image", "specify --name and --image"},
	"wl.deploy.sending": {"Déploiement de %s (%s)…", "Deploying %s (%s)…"},
	"wl.deploy.started": {"Déploiement lancé (%s).", "Deployment started (%s)."},
	"wl.deploy.waiting": {"En attente du démarrage…", "Waiting for startup…"},
	"wl.deploy.ok":      {"En service.", "Running."},
	"wl.deploy.ok_url":  {"En service : %s", "Running: %s"},
	"wl.deploy.failed":  {"le déploiement a échoué (%s, étape %s) : consultez `betorun workloads logs %s`", "deployment failed (%s, step %s): see `betorun workloads logs %s`"},
	"wl.deploy.timeout": {"le démarrage prend plus de temps que prévu ; suivez-le avec `betorun workloads status %s`", "startup is taking longer than expected; follow it with `betorun workloads status %s`"},
	"wl.update.short":   {"Publier une nouvelle version (image, variables)", "Publish a new version (image, variables)"},
	"wl.update.need":    {"précisez --image et/ou --env", "specify --image and/or --env"},
	"wl.update.sending": {"Mise à jour de %s…", "Updating %s…"},
	"wl.env.invalid":    {"variable invalide %q : attendu CLE=valeur", "invalid variable %q: expected KEY=value"},
	"wl.logs.short":     {"Afficher les logs d'un conteneur", "Show a container's logs"},
	"wl.status.short":   {"État d'un conteneur", "Container status"},
	"wl.restart.short":  {"Redémarrer un conteneur", "Restart a container"},
	"wl.restart.ok":     {"%s redémarre.", "%s is restarting."},
	"wl.stop.short":     {"Arrêter un conteneur (0 réplique)", "Stop a container (0 replicas)"},
	"wl.stop.ok":        {"%s est arrêté.", "%s is stopped."},
	"wl.start.short":    {"Relancer un conteneur arrêté", "Start a stopped container"},
	"wl.start.ok":       {"%s redémarre avec ses répliques.", "%s is starting with its replicas."},
	"wl.delete.short":   {"Supprimer un conteneur", "Delete a container"},
	"wl.delete.confirm": {"Supprimer %s et ses données ? Retapez son nom pour confirmer :", "Delete %s and its data? Type its name to confirm:"},
	"wl.delete.aborted": {"suppression annulée", "deletion cancelled"},
	"wl.delete.ok":      {"Suppression de %s lancée.", "Deletion of %s started."},
	"wl.col.name":       {"Nom", "Name"},
	"wl.col.status":     {"État", "Status"},
	"wl.col.image":      {"Image", "Image"},
	"wl.col.replicas":   {"Répliques", "Replicas"},
	"wl.col.url":        {"URL", "URL"},
	"wl.col.identity":   {"Identité", "Identity"},

	"flag.wl.name":         {"nom du conteneur (3 à 30 caractères)", "container name (3 to 30 characters)"},
	"flag.wl.image":        {"image Docker (ex. ghcr.io/vous/api:1.0)", "Docker image (e.g. ghcr.io/you/api:1.0)"},
	"flag.wl.port":         {"port d'écoute du conteneur", "container listening port"},
	"flag.wl.replicas":     {"nombre de répliques", "number of replicas"},
	"flag.wl.cpu":          {"limite CPU (ex. 500m)", "CPU limit (e.g. 500m)"},
	"flag.wl.memory":       {"limite mémoire (ex. 512Mi)", "memory limit (e.g. 512Mi)"},
	"flag.wl.env":          {"variable d'environnement CLE=valeur (répétable)", "environment variable KEY=value (repeatable)"},
	"flag.wl.env_replace":  {"variable CLE=valeur (répétable) ; remplace toutes les variables", "environment variable KEY=value (repeatable); replaces all variables"},
	"flag.wl.sa":           {"compte de service sous lequel il s'exécute, ex. api-runner (identité sans clé)", "service account it runs as, e.g. api-runner (keyless identity)"},
	"flag.wl.tls":          {"certificat HTTPS", "HTTPS certificate"},
	"flag.logs.follow":     {"suivre les nouveaux logs (Ctrl-C pour arrêter)", "follow new logs (Ctrl-C to stop)"},
	"flag.logs.tail":       {"nombre de lignes depuis la fin", "number of lines from the end"},
	"flag.logs.since":      {"depuis (ex. 10m, 2h)", "since (e.g. 10m, 2h)"},
	"flag.logs.previous":   {"logs du conteneur précédent (après un crash)", "logs of the previous container (after a crash)"},
	"flag.logs.timestamps": {"afficher l'heure de chaque ligne", "show each line's time"},

	"sites.not_found":    {"le site %q n'existe pas", "site %q does not exist"},
	"dom.short":          {"Domaines personnalisés d'un site", "A site's custom domains"},
	"dom.not_found":      {"le domaine %q n'est pas rattaché à ce site", "domain %q is not attached to this site"},
	"dom.list.short":     {"Lister les domaines du site", "List the site's domains"},
	"dom.list.empty":     {"Aucun domaine personnalisé.", "No custom domain."},
	"dom.pending":        {"à vérifier", "to verify"},
	"dom.verified":       {"vérifié", "verified"},
	"dom.add.short":      {"Ajouter un domaine (ex. www.exemple.sn)", "Add a domain (e.g. www.example.com)"},
	"dom.add.ok":         {"Domaine %s ajouté. Chez votre registrar, créez :\n\n    %s  CNAME  %s\n\npuis lancez : betorun sites domains verify --site %s %s", "Domain %s added. At your registrar, create:\n\n    %s  CNAME  %s\n\nthen run: betorun sites domains verify --site %s %s"},
	"dom.verify.short":   {"Vérifier le DNS d'un domaine", "Check a domain's DNS"},
	"dom.verify.ok":      {"%s est vérifié : il est servi en HTTPS.", "%s is verified: it is served over HTTPS."},
	"dom.verify.pending": {"%s ne pointe pas encore vers %s (le DNS peut mettre quelques minutes)", "%s does not point to %s yet (DNS may take a few minutes)"},
	"dom.remove.short":   {"Retirer un domaine", "Remove a domain"},
	"dom.remove.ok":      {"Domaine %s retiré.", "Domain %s removed."},

	"acc.short":         {"Accès au projet (rôles)", "Project access (roles)"},
	"acc.list.short":    {"Lister qui a accès au projet", "List who has access to the project"},
	"acc.grant.short":   {"Accorder un rôle (email, serviceAccount:…, group:…)", "Grant a role (email, serviceAccount:…, group:…)"},
	"acc.grant.ok":      {"Rôle %s accordé à %s.", "Role %s granted to %s."},
	"acc.revoke.short":  {"Retirer les rôles d'un membre", "Remove a member's roles"},
	"acc.revoke.ok":     {"Accès de %s retirés (%d).", "Access of %s removed (%d)."},
	"acc.revoke.none":   {"%s n'a pas ce rôle sur le projet", "%s does not have this role on the project"},
	"acc.roles.short":   {"Lister les rôles attribuables", "List the roles that can be granted"},
	"acc.until.invalid": {"--until attend une date AAAA-MM-JJ", "--until expects a date YYYY-MM-DD"},
	"acc.until.title":   {"Expire le %s", "Expires on %s"},
	"flag.until":        {"accès temporaire jusqu'à cette date (AAAA-MM-JJ)", "temporary access until this date (YYYY-MM-DD)"},
	"flag.role":         {"ne retirer que ce rôle", "only remove this role"},

	// ── Identités : groupes, rôles, comptes de service ──
	"arg.group":    {"<groupe>", "<group>"},
	"arg.sa":       {"<compte>", "<account>"},
	"arg.role_id":  {"<identifiant-du-rôle>", "<role-id>"},
	"arg.key_id":   {"<identifiant-de-clé>", "<key-id>"},
	"arg.trust_id": {"<identifiant-de-relation>", "<trust-id>"},
	"op.aborted":   {"opération annulée", "operation cancelled"},
	"token.short":  {"Afficher le jeton d'accès courant (pour curl, scripts)", "Print the current access token (for curl, scripts)"},

	"flag.display_name": {"nom affiché", "display name"},
	"flag.description":  {"description", "description"},

	"iam.no_org":          {"ce projet n'est rattaché à aucune organisation", "this project belongs to no organization"},
	"iam.group.not_found": {"aucun groupe %q dans l'organisation (voir `betorun iam groups list`)", "no group %q in the organization (see `betorun iam groups list`)"},
	"iam.sa.not_found":    {"aucun compte de service %q dans ce projet (voir `betorun iam sa list`)", "no service account %q in this project (see `betorun iam sa list`)"},
	"iam.role.not_found":  {"aucun rôle personnalisé %q (voir `betorun iam roles`)", "no custom role %q (see `betorun iam roles`)"},

	"grp.short":          {"Groupes de l'organisation (accès donnés à une équipe)", "Organization groups (access given to a team)"},
	"grp.list.short":     {"Lister les groupes", "List groups"},
	"grp.list.empty":     {"Aucun groupe. Créez-en un : betorun iam groups create equipe-paiement", "No group. Create one: betorun iam groups create payments-team"},
	"grp.members_count":  {"%d membre(s)", "%d member(s)"},
	"grp.create.short":   {"Créer un groupe", "Create a group"},
	"grp.create.ok":      {"Groupe %s créé.", "Group %s created."},
	"grp.create.next":    {"Ensuite : betorun iam groups add-member %[1]s <email> puis betorun iam grant group:%[1]s <rôle>", "Next: betorun iam groups add-member %[1]s <email>, then betorun iam grant group:%[1]s <role>"},
	"grp.delete.short":   {"Supprimer un groupe (et ses accès partout)", "Delete a group (and its access everywhere)"},
	"grp.delete.confirm": {"Supprimer le groupe %s et tous ses accès ? Retapez son nom pour confirmer :", "Delete group %s and all its access? Type its name to confirm:"},
	"grp.delete.ok":      {"Groupe %s supprimé.", "Group %s deleted."},
	"grp.members.short":  {"Lister les membres d'un groupe", "List a group's members"},
	"grp.members.empty":  {"Le groupe %s n'a aucun membre.", "Group %s has no members."},
	"grp.add.short":      {"Ajouter un membre (email ou serviceAccount:<compte>)", "Add a member (email or serviceAccount:<account>)"},
	"grp.add.ok":         {"%s ajouté au groupe %s.", "%s added to group %s."},
	"grp.remove.short":   {"Retirer un membre (effet immédiat)", "Remove a member (immediate)"},
	"grp.remove.ok":      {"%s retiré du groupe %s.", "%s removed from group %s."},

	"roles.custom_header":   {"Rôles personnalisés :", "Custom roles:"},
	"roles.scope.org":       {"organisation", "organization"},
	"roles.scope.project":   {"projet", "project"},
	"roles.describe.short":  {"Détails d'un rôle personnalisé (permissions)", "Custom role details (permissions)"},
	"roles.col.name":        {"Nom", "Name"},
	"roles.col.title":       {"Titre", "Title"},
	"roles.col.description": {"Description", "Description"},
	"roles.col.permissions": {"Permissions :", "Permissions:"},
	"roles.create.short":    {"Créer un rôle personnalisé", "Create a custom role"},
	"roles.create.need":     {"précisez au moins une --permission (voir `betorun iam permissions`)", "specify at least one --permission (see `betorun iam permissions`)"},
	"roles.create.ok":       {"Rôle %s créé (%d permission(s)).", "Role %s created (%d permission(s))."},
	"roles.create.next":     {"Ensuite : betorun iam grant <membre> %s", "Next: betorun iam grant <member> %s"},
	"roles.update.short":    {"Modifier un rôle personnalisé (effet immédiat pour ses détenteurs)", "Update a custom role (immediate for its holders)"},
	"roles.update.ok":       {"Rôle %s mis à jour (%d permission(s)).", "Role %s updated (%d permission(s))."},
	"roles.delete.short":    {"Supprimer un rôle personnalisé (et ses attributions)", "Delete a custom role (and its grants)"},
	"roles.delete.confirm":  {"Supprimer le rôle %s et toutes ses attributions ? Retapez son nom pour confirmer :", "Delete role %s and all its grants? Type its name to confirm:"},
	"roles.delete.ok":       {"Rôle %s supprimé.", "Role %s deleted."},
	"flag.role.title":       {"titre du rôle", "role title"},
	"flag.role.permission":  {"permission incluse (répétable, ou séparées par des virgules)", "included permission (repeatable, or comma-separated)"},
	"flag.role.add":         {"permission à ajouter (répétable)", "permission to add (repeatable)"},
	"flag.role.remove":      {"permission à retirer (répétable)", "permission to remove (repeatable)"},
	"flag.role.org":         {"définir le rôle sur l'organisation (utilisable dans tous ses projets)", "define the role on the organization (usable in all its projects)"},
	"perms.short":           {"Lister les permissions utilisables dans un rôle personnalisé", "List permissions usable in a custom role"},
	"flag.perms.filter":     {"ne garder que les permissions contenant ce texte (ex. workloads)", "only keep permissions containing this text (e.g. workloads)"},
	"flag.perms.all":        {"toutes les permissions, y compris celles réservées aux rôles prédéfinis", "all permissions, including those reserved to predefined roles"},

	"sa.short":              {"Comptes de service (identités des CI, scripts et conteneurs)", "Service accounts (identities for CI, scripts and containers)"},
	"sa.list.short":         {"Lister les comptes de service du projet", "List the project's service accounts"},
	"sa.list.empty":         {"Aucun compte de service. Créez-en un : betorun iam sa create ci-deployer", "No service account. Create one: betorun iam sa create ci-deployer"},
	"sa.disabled":           {"désactivé", "disabled"},
	"sa.create.short":       {"Créer un compte de service (sans rôle)", "Create a service account (no role)"},
	"sa.create.ok":          {"Compte %s créé (%s).", "Account %s created (%s)."},
	"sa.create.next":        {"Ensuite : betorun iam grant serviceAccount:%[1]s <rôle>, puis une CI sans secret (betorun iam sa trust github %[1]s …) ou une clé (betorun iam sa keys create %[1]s)", "Next: betorun iam grant serviceAccount:%[1]s <role>, then a secretless CI (betorun iam sa trust github %[1]s …) or a key (betorun iam sa keys create %[1]s)"},
	"sa.delete.short":       {"Supprimer un compte de service (clés et accès compris)", "Delete a service account (keys and access included)"},
	"sa.delete.confirm":     {"Supprimer le compte %s, ses clés et ses accès ? Retapez son nom pour confirmer :", "Delete account %s, its keys and access? Type its name to confirm:"},
	"sa.delete.ok":          {"Compte %s supprimé.", "Account %s deleted."},
	"sa.keys.short":         {"Clés des comptes de service (préférez une CI sans secret : trust)", "Service account keys (prefer a secretless CI: trust)"},
	"sa.keys.list.short":    {"Lister les clés d'un compte", "List an account's keys"},
	"sa.keys.empty":         {"Le compte %s n'a aucune clé.", "Account %s has no keys."},
	"sa.keys.until":         {"valide jusqu'au %s", "valid until %s"},
	"sa.keys.disabled":      {"désactivée", "disabled"},
	"sa.keys.never_used":    {"jamais utilisée", "never used"},
	"sa.keys.used":          {"utilisée le %s", "used on %s"},
	"sa.keys.create.short":  {"Créer une clé et l'enregistrer dans un fichier (remise une seule fois)", "Create a key and save it to a file (handed over once)"},
	"sa.keys.output_exists": {"le fichier %s existe déjà : choisissez-en un autre avec --output", "file %s already exists: choose another with --output"},
	"sa.keys.no_file":       {"l'API n'a pas remis de fichier de clé", "the API returned no key file"},
	"sa.keys.create.ok":     {"Clé %s enregistrée dans %s (valide jusqu'au %s).", "Key %s saved to %s (valid until %s)."},
	"sa.keys.create.warn":   {"Ce fichier donne les accès du compte : ne le commitez pas, stockez-le comme un secret.", "This file grants the account's access: do not commit it, store it as a secret."},
	"sa.keys.create.use":    {"Utilisation : BETORUN_KEY_FILE=%s betorun whoami", "Usage: BETORUN_KEY_FILE=%s betorun whoami"},
	"sa.keys.disable.short": {"Désactiver une clé (effet sous 30 s)", "Disable a key (effective within 30 s)"},
	"sa.keys.disable.ok":    {"Clé %s du compte %s désactivée.", "Key %s of account %s disabled."},
	"flag.sa.output":        {"fichier de clé à créer (défaut : <compte>.json)", "key file to create (default: <account>.json)"},
	"flag.sa.days":          {"durée de validité en jours (1 à 365)", "validity in days (1 to 365)"},

	"trust.short":            {"CI sans secret : relations de confiance OIDC (GitHub Actions, GitLab)", "Secretless CI: OIDC trust relationships (GitHub Actions, GitLab)"},
	"trust.list.short":       {"Lister les relations de confiance d'un compte", "List an account's trust relationships"},
	"trust.list.empty":       {"Aucune relation de confiance pour %s.", "No trust relationship for %s."},
	"trust.github.short":     {"Autoriser un dépôt GitHub à agir comme ce compte", "Allow a GitHub repository to act as this account"},
	"trust.gitlab.short":     {"Autoriser un projet GitLab à agir comme ce compte", "Allow a GitLab project to act as this account"},
	"trust.github.repo":      {"--repo attend propriétaire/dépôt (ex. sunupay/api)", "--repo expects owner/repo (e.g. sunupay/api)"},
	"trust.gitlab.project":   {"--project attend groupe/projet (ex. sunupay/api)", "--project expects group/project (e.g. sunupay/api)"},
	"trust.one_target":       {"choisissez une seule cible : --branch, --tag, --environment ou --pull-requests", "choose a single target: --branch, --tag, --environment or --pull-requests"},
	"trust.create.ok":        {"Relation de confiance créée : %s ← %s", "Trust relationship created: %s ← %s"},
	"trust.create.roles":     {"Le compte %s n'a que les rôles qui lui sont accordés (betorun iam grant serviceAccount:… <rôle>).", "Account %s only has the roles granted to it (betorun iam grant serviceAccount:… <role>)."},
	"trust.create.snippet":   {"À ajouter à votre CI :", "Add this to your CI:"},
	"trust.delete.short":     {"Supprimer une relation de confiance", "Delete a trust relationship"},
	"trust.delete.ok":        {"Relation de confiance %s supprimée.", "Trust relationship %s deleted."},
	"flag.trust.repo":        {"dépôt GitHub propriétaire/dépôt", "GitHub repository owner/repo"},
	"flag.trust.gl_project":  {"projet GitLab groupe/projet", "GitLab project group/project"},
	"flag.trust.branch":      {"branche autorisée (défaut : main)", "allowed branch (default: main)"},
	"flag.trust.tag":         {"tags autorisés (ex. 'v*')", "allowed tags (e.g. 'v*')"},
	"flag.trust.environment": {"environnement GitHub autorisé (ex. production)", "allowed GitHub environment (e.g. production)"},
	"flag.trust.pr":          {"autoriser les pull requests", "allow pull requests"},
	"flag.trust.owner_id":    {"identifiant numérique du propriétaire (protège d'un dépôt recréé sous le même nom)", "numeric owner ID (protects against a repo recreated under the same name)"},
	"flag.trust.issuer":      {"GitLab auto-hébergé : URL de l'instance", "self-hosted GitLab: instance URL"},

	"version.short": {"Afficher la version", "Show the version"},

	"flag.api":        {"URL de l'API (…/kmanager/v1)", "API URL (…/kmanager/v1)"},
	"flag.project":    {"projet (identifiant)", "project (ID)"},
	"flag.no_browser": {"ne pas ouvrir le navigateur", "do not open the browser"},
	"flag.site":       {"nom du site", "site name"},
	"flag.create":     {"créer le site s'il n'existe pas", "create the site if it does not exist"},
	"flag.spa":        {"application monopage : index.html pour les chemins inconnus (à la création)", "single-page app: index.html for unknown paths (on creation)"},
	"flag.no_wait":    {"ne pas attendre la fin de la publication", "do not wait for publishing to finish"},
}
