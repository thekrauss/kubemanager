package config

// Variables d'environnement lues par la CLI.
const (
	EnvConfigDir      = "BETORUN_CONFIG_DIR"
	EnvAPIURL         = "BETORUN_API_URL"
	EnvProject        = "BETORUN_PROJECT"
	EnvToken          = "BETORUN_TOKEN"
	EnvKeyFile        = "BETORUN_KEY_FILE"
	EnvTokenFile      = "BETORUN_TOKEN_FILE"
	EnvSTSTokenURI    = "BETORUN_STS_TOKEN_URI"
	EnvServiceAccount = "BETORUN_SERVICE_ACCOUNT"
	EnvIDToken        = "BETORUN_ID_TOKEN"

	// Variables posées par GitHub Actions (permissions: id-token: write).
	EnvGitHubIDTokenRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	EnvGitHubIDTokenRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
	// EnvCI : posée par la plupart des CI ; désactive les interactions.
	EnvCI = "CI"
)

// Fichiers du dossier de configuration.
const (
	appDirName          = "betorun"
	settingsFileName    = "config.json"
	credentialsFileName = "credentials.json"

	dirPerm  = 0o700
	filePerm = 0o600
)
