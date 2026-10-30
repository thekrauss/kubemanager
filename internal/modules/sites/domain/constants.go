package domain

// Organisation de l'object store, partagée avec le binaire static-edge
// (internal/edge) — ne pas changer sans lui.
const (
	// ObjectsRoot : racine des fichiers extraits (sites/<site>/<déploiement>/).
	ObjectsRoot = "sites/"
	// UploadsRoot : racine des archives uploadées (uploads/<site>/<déploiement>.zip).
	UploadsRoot = "uploads/"
	// ArchiveExt : extension des archives de déploiement.
	ArchiveExt = ".zip"
)

// Fichiers conventionnels d'un site statique.
const (
	// IndexFile : page servie pour un répertoire, requise à la racine.
	IndexFile = "index.html"
	// NotFoundFile : page d'erreur 404 personnalisée, facultative.
	NotFoundFile = "404.html"
)

// Limites DNS (RFC 1035).
const (
	maxHostnameLen = 253
	maxLabelLen    = 63
)

// Types d'erreur Temporal non rejouables des activities de site.
const (
	// ErrTypeInvalidInput : référence de site/déploiement illisible.
	ErrTypeInvalidInput = "InvalidInput"
	// ErrTypeInvalidArchive : archive refusée ; son message est destiné à
	// l'utilisateur.
	ErrTypeInvalidArchive = "InvalidArchive"
)

// BytesPerMB : conversion des limites d'octets en Mo pour les messages.
const BytesPerMB = 1 << 20

// ArchiveFormField : champ multipart de l'archive lors d'un déploiement
// (API et CLI).
const ArchiveFormField = "archive"
