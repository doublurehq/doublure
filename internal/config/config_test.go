package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pixelfactoryio/previo/internal/config"
)

func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"GITHUB_APP_ID":          "12345",
		"GITHUB_PRIVATE_KEY":     "fake-pem-key",
		"GITHUB_WEBHOOK_SECRET":  "secret",
		"GITHUB_INSTALLATION_ID": "67890",
		"GITOPS_REPO":            "org/gitops-repo",
	}
}

func TestLoad_MissingAppID(t *testing.T) {
	env := validEnv()
	delete(env, "GITHUB_APP_ID")
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for missing GITHUB_APP_ID")
	}
}

func TestLoad_InvalidAppID(t *testing.T) {
	env := validEnv()
	env["GITHUB_APP_ID"] = "not-a-number"
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for non-integer GITHUB_APP_ID")
	}
}

func TestLoad_MissingPrivateKey(t *testing.T) {
	env := validEnv()
	delete(env, "GITHUB_PRIVATE_KEY")
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for missing private key")
	}
}

func TestLoad_MissingWebhookSecret(t *testing.T) {
	env := validEnv()
	delete(env, "GITHUB_WEBHOOK_SECRET")
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for missing GITHUB_WEBHOOK_SECRET")
	}
}

func TestLoad_Valid(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppID != 12345 {
		t.Errorf("AppID = %d, want 12345", cfg.AppID)
	}
	if string(cfg.PrivateKey) != "fake-pem-key" {
		t.Errorf("PrivateKey = %q, want %q", cfg.PrivateKey, "fake-pem-key")
	}
	if cfg.WebhookSecret != "secret" {
		t.Errorf("WebhookSecret = %q, want %q", cfg.WebhookSecret, "secret")
	}
	if cfg.InstallationID != 67890 {
		t.Errorf("InstallationID = %d, want 67890", cfg.InstallationID)
	}
	if cfg.GitOpsRepo != "org/gitops-repo" {
		t.Errorf("GitOpsRepo = %q, want %q", cfg.GitOpsRepo, "org/gitops-repo")
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
}

func TestLoad_MissingInstallationID(t *testing.T) {
	env := validEnv()
	delete(env, "GITHUB_INSTALLATION_ID")
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for missing GITHUB_INSTALLATION_ID")
	}
}

func TestLoad_InvalidInstallationID(t *testing.T) {
	env := validEnv()
	env["GITHUB_INSTALLATION_ID"] = "not-a-number"
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for non-integer GITHUB_INSTALLATION_ID")
	}
}

func TestLoad_MissingGitOpsRepo(t *testing.T) {
	env := validEnv()
	delete(env, "GITOPS_REPO")
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for missing GITOPS_REPO")
	}
}

func TestLoad_DefaultListenAddr(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
}

func TestLoad_CustomListenAddr(t *testing.T) {
	env := validEnv()
	env["LISTEN_ADDR"] = ":9090"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
}

func TestLoad_PrivateKeyPathTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyFile, []byte("file-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := validEnv()
	env["GITHUB_PRIVATE_KEY_PATH"] = keyFile
	env["GITHUB_PRIVATE_KEY"] = "inline-key"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(cfg.PrivateKey) != "file-key" {
		t.Errorf("PrivateKey = %q, want %q (file should take precedence)", cfg.PrivateKey, "file-key")
	}
}
