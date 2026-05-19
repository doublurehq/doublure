package github

import (
	"context"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v71/github"
)

// AppConfig holds the configuration for a GitHub App.
type AppConfig struct {
	AppID         int64
	PrivateKey    []byte //nolint:gosec // intentional field name for PEM-encoded RSA private key
	WebhookSecret string
}

// App provides GitHub App authentication and webhook handling.
type App struct {
	webhookSecret []byte
	transport     *ghinstallation.AppsTransport
}

// NewApp creates a new App from the given configuration.
func NewApp(cfg AppConfig) (*App, error) {
	tr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, cfg.AppID, cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &App{
		webhookSecret: []byte(cfg.WebhookSecret),
		transport:     tr,
	}, nil
}

// NewAppFromTransport creates a new App using a pre-configured AppsTransport.
// This is useful for testing with custom base URLs.
func NewAppFromTransport(tr *ghinstallation.AppsTransport, webhookSecret string) (*App, error) {
	return &App{
		webhookSecret: []byte(webhookSecret),
		transport:     tr,
	}, nil
}

// GetInstallationToken returns an installation access token for the given installation ID.
func (a *App) GetInstallationToken(ctx context.Context, installationID int64) (string, error) {
	itr := ghinstallation.NewFromAppsTransport(a.transport, installationID)
	return itr.Token(ctx)
}

// ClientForInstallation returns an authenticated GitHub client for the given installation.
func (a *App) ClientForInstallation(installationID int64) *gh.Client {
	itr := ghinstallation.NewFromAppsTransport(a.transport, installationID)
	return gh.NewClient(&http.Client{Transport: itr})
}
