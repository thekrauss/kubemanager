package activities

// extractTempPattern : fichier temporaire de l'archive pendant l'extraction.
const extractTempPattern = "site-*.zip"

// ignoredArchiveEntries : répertoires et fichiers parasites jamais publiés
// (métadonnées macOS, dépôt git).
var ignoredArchiveEntries = map[string]bool{
	"__MACOSX":  true,
	".git":      true,
	".DS_Store": true,
}
