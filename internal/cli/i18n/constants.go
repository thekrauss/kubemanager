package i18n

// Langues prises en charge par la CLI.
const (
	LangFR = "fr"
	LangEN = "en"
	// defaultLang : langue si l'environnement n'en indique aucune.
	defaultLang = LangFR
)

// langEnvVars : variables consultées, par priorité, pour choisir la langue.
var langEnvVars = []string{"BETORUN_LANG", "LC_ALL", "LC_MESSAGES", "LANG"}
