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

	"version.short": {"Afficher la version", "Show the version"},

	"flag.api":        {"URL de l'API (…/kmanager/v1)", "API URL (…/kmanager/v1)"},
	"flag.project":    {"projet (identifiant)", "project (ID)"},
	"flag.no_browser": {"ne pas ouvrir le navigateur", "do not open the browser"},
	"flag.site":       {"nom du site", "site name"},
	"flag.create":     {"créer le site s'il n'existe pas", "create the site if it does not exist"},
	"flag.spa":        {"application monopage : index.html pour les chemins inconnus (à la création)", "single-page app: index.html for unknown paths (on creation)"},
	"flag.no_wait":    {"ne pas attendre la fin de la publication", "do not wait for publishing to finish"},
}
