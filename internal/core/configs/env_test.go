package configs

import (
	"os"
	"path/filepath"
	"testing"
)

// Une clé absente du fichier doit quand même être lue depuis l'environnement
// (la prod configure les secrets IAM uniquement par variables).
func TestLoad_EnvWithoutFileKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("service_name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBEMANAGER_IAM_API_KEY_PEPPER", "from-env-0123456789abcdef0123456789")
	t.Setenv("KUBEMANAGER_IAM_PLATFORM_ADMINS", "a@x.sn,b@x.sn")
	t.Setenv("KUBEMANAGER_SERVER_HTTP_PORT", "9090")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IAM.APIKeyPepper != "from-env-0123456789abcdef0123456789" {
		t.Fatalf("pepper = %q", cfg.IAM.APIKeyPepper)
	}
	if len(cfg.IAM.PlatformAdmins) != 2 || cfg.IAM.PlatformAdmins[1] != "b@x.sn" {
		t.Fatalf("platform admins = %v", cfg.IAM.PlatformAdmins)
	}
	if cfg.ServiceName != "test" {
		t.Fatalf("service name = %q", cfg.ServiceName)
	}
}

func TestLoad_MFAFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("service_name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBEMANAGER_IAM_MFA_ACR_VALUES", "gold")
	t.Setenv("KUBEMANAGER_IAM_STEP_UP_MAX_AGE", "15m")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.IAM.MFAACRValues) != 1 || cfg.IAM.MFAACRValues[0] != "gold" || cfg.IAM.StepUpMaxAge.Minutes() != 15 {
		t.Fatalf("mfa = %v, max age = %v", cfg.IAM.MFAACRValues, cfg.IAM.StepUpMaxAge)
	}
}
