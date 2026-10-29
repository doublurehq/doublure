package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
)

// Config holds all application configuration.
type Config struct {
	AppID          int64
	PrivateKey     []byte //nolint:gosec // intentional field name
	WebhookSecret  string
	InstallationID int64
	GitOpsRepo     string
	ListenAddr     string
	DeployToken    string
	// MetricsAddr is the listen address of the metrics server, "" when it is
	// disabled.
	MetricsAddr string
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	appID, err := requireEnvInt64("GITHUB_APP_ID")
	if err != nil {
		return nil, err
	}

	privateKey, err := LoadPrivateKey()
	if err != nil {
		return nil, err
	}

	webhookSecret, err := requireEnv("GITHUB_WEBHOOK_SECRET")
	if err != nil {
		return nil, err
	}

	installationID, err := requireEnvInt64("GITHUB_INSTALLATION_ID")
	if err != nil {
		return nil, err
	}

	gitOpsRepo, err := requireEnv("GITOPS_REPO")
	if err != nil {
		return nil, err
	}

	deployToken, err := requireEnv("DOUBLURE_DEPLOY_TOKEN")
	if err != nil {
		return nil, err
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	metricsAddr, err := loadMetricsAddr()
	if err != nil {
		return nil, err
	}

	return &Config{
		AppID:          appID,
		PrivateKey:     privateKey,
		WebhookSecret:  webhookSecret,
		InstallationID: installationID,
		GitOpsRepo:     gitOpsRepo,
		ListenAddr:     listenAddr,
		DeployToken:    deployToken,
		MetricsAddr:    metricsAddr,
	}, nil
}

// loadMetricsAddr reads METRICS_ADDR: unset means ":9090", set to "" means
// disabled, and any other value must be a host:port.
func loadMetricsAddr() (string, error) {
	addr, ok := os.LookupEnv("METRICS_ADDR")
	if !ok {
		return ":9090", nil
	}
	if addr == "" {
		return "", nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", fmt.Errorf("METRICS_ADDR must be host:port, got %q", addr)
	}
	return addr, nil
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

func requireEnvInt64(key string) (int64, error) {
	s, err := requireEnv(key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return v, nil
}

// LoadPrivateKey reads the GitHub App PEM from GITHUB_PRIVATE_KEY_PATH or,
// falling back, from inline GITHUB_PRIVATE_KEY.
func LoadPrivateKey() ([]byte, error) {
	if path := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // path from trusted env var
		if err != nil {
			return nil, fmt.Errorf("reading private key file: %w", err)
		}
		return data, nil
	}
	if raw := os.Getenv("GITHUB_PRIVATE_KEY"); raw != "" {
		return []byte(raw), nil
	}
	return nil, errors.New("GITHUB_PRIVATE_KEY or GITHUB_PRIVATE_KEY_PATH is required")
}
