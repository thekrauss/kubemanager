package edge

import "github.com/thekrauss/kubemanager/internal/modules/sites/domain"

// Routes du port interne (jamais exposé publiquement).
const (
	pathHealthz = "/healthz"
	// pathCheck : contrôle « ask » du TLS à la demande de Caddy.
	pathCheck = "/check"
	// checkDomainParam : paramètre de requête du domaine à contrôler.
	checkDomainParam = "domain"
)

// Fichiers et extensions des sites servis.
const (
	indexFile    = domain.IndexFile
	notFoundFile = domain.NotFoundFile
	htmlExt      = ".html"
)

// En-têtes de réponse.
const (
	allowedMethods = "GET, HEAD"
	// cacheControlHTML : HTML toujours revalidé, un nouveau déploiement est
	// visible tout de suite.
	cacheControlHTML = "public, max-age=0, must-revalidate"
	// cacheControlAsset : autres fichiers, une heure.
	cacheControlAsset = "public, max-age=3600"
)

// defaultResolverMaxEntries : taille maximale du cache des noms d'hôte.
const defaultResolverMaxEntries = 100000
