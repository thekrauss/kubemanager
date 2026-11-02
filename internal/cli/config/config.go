// Package config : réglages et session de la CLI, dans
// $XDG_CONFIG_HOME/betorun (~/.config/betorun), fichiers lisibles par le
// seul utilisateur (0600, dossier 0700).
//
// Variables d'environnement (prioritaires, utiles en CI) :
//
//	BETORUN_API_URL  URL de l'API (…/kmanager/v1)
//	BETORUN_PROJECT  projet par défaut
//	BETORUN_TOKEN    jeton d'accès à utiliser tel quel
//	BETORUN_KEY_FILE fichier de clé d'un compte de service (jetons courts)
//	BETORUN_SERVICE_ACCOUNT  compte de service visé par la fédération OIDC
//	                 (GitHub Actions, GitLab : BETORUN_ID_TOKEN) ou par
//	                 l'identité d'un conteneur (BETORUN_TOKEN_FILE)
//
// Ordre des identifiants : voir cli.credentialSource. Avec des identifiants
// machine, aucune session n'est lue ni écrite.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Settings : réglages persistants.
type Settings struct {
	APIURL    string `json:"api_url,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

// Credentials : session obtenue par `betorun login`.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Dir : dossier de configuration (BETORUN_CONFIG_DIR pour les tests).
func Dir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName), nil
}

func load(name string, v any) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// save écrit de façon atomique (fichier temporaire puis renommage), en 0600.
func save(name string, v any) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

func LoadSettings() (Settings, error) {
	var s Settings
	err := load(settingsFileName, &s)
	if v := strings.TrimSpace(os.Getenv(EnvAPIURL)); v != "" {
		s.APIURL = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvProject)); v != "" {
		s.ProjectID = v
	}
	s.APIURL = strings.TrimRight(s.APIURL, "/")
	return s, err
}

// SaveSettings : réglages du fichier seulement (l'environnement n'est pas persisté).
func SaveSettings(update func(*Settings)) error {
	var s Settings
	if err := load(settingsFileName, &s); err != nil {
		return err
	}
	update(&s)
	return save(settingsFileName, s)
}

func LoadCredentials() (*Credentials, error) {
	var c Credentials
	if err := load(credentialsFileName, &c); err != nil {
		return nil, err
	}
	if c.AccessToken == "" {
		return nil, nil
	}
	return &c, nil
}

func SaveCredentials(c *Credentials) error { return save(credentialsFileName, c) }

func DeleteCredentials() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, credentialsFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
