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
	"err.step_up":         {"cette action exige une double authentification récente : confirmez votre identité dans la console, ou relancez `betorun login`", "this action requires recent two-factor authentication: confirm your identity in the console, or run `betorun login` again"},
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

	"logout.short": {"Se déconnecter (révoque la session)", "Log out (revokes the session)"},
	"logout.ok":    {"Déconnecté.", "Logged out."},
	"logout.none":  {"Aucune session ouverte.", "No open session."},

	"whoami.short":     {"Afficher le compte et le projet courants", "Show the current account and project"},
	"whoami.account":   {"Compte      %s", "Account     %s"},
	"whoami.principal": {"Identité    %s", "Identity    %s"},
	"whoami.project":   {"Projet      %s", "Project     %s"},
	"whoami.mfa_on":    {"Double auth.  oui", "Two-factor  yes"},
	"whoami.mfa_off":   {"Double auth.  non", "Two-factor  no"},
	"whoami.api":       {"API         %s", "API         %s"},

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
	"flag.wl.sa":           {"compte de service sous lequel il s'exécute (identité sans clé)", "service account it runs as (keyless identity)"},
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

	"version.short": {"Afficher la version", "Show the version"},

	"flag.api":        {"URL de l'API (…/kmanager/v1)", "API URL (…/kmanager/v1)"},
	"flag.project":    {"projet (identifiant)", "project (ID)"},
	"flag.no_browser": {"ne pas ouvrir le navigateur", "do not open the browser"},
	"flag.site":       {"nom du site", "site name"},
	"flag.create":     {"créer le site s'il n'existe pas", "create the site if it does not exist"},
	"flag.spa":        {"application monopage : index.html pour les chemins inconnus (à la création)", "single-page app: index.html for unknown paths (on creation)"},
	"flag.no_wait":    {"ne pas attendre la fin de la publication", "do not wait for publishing to finish"},
}
