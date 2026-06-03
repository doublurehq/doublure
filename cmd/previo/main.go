package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pixelfactoryio/previo/internal/config"
	"github.com/pixelfactoryio/previo/internal/github"
	"github.com/pixelfactoryio/previo/internal/gitops"
	"github.com/pixelfactoryio/previo/internal/server"
)

func main() {
	root := &cobra.Command{
		Use:          "previo",
		Short:        "Preview environment manager",
		SilenceUsage: true,
	}

	root.AddCommand(serveCmd())
	root.AddCommand(cleanupCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			app, err := github.NewApp(github.AppConfig{
				AppID:         cfg.AppID,
				PrivateKey:    cfg.PrivateKey,
				WebhookSecret: cfg.WebhookSecret,
			})
			if err != nil {
				return fmt.Errorf("creating github app: %w", err)
			}

			owner, repo, ok := strings.Cut(cfg.GitOpsRepo, "/")
			if !ok || owner == "" || repo == "" {
				return fmt.Errorf("GITOPS_REPO must be 'owner/repo', got %q", cfg.GitOpsRepo)
			}
			ghClient := app.ClientForInstallation(cfg.InstallationID)
			gitopsClient := gitops.NewClient(ghClient, owner, repo)

			srv := server.NewServer(app, gitopsClient, cfg.DeployToken)
			httpSrv := &http.Server{
				Addr:              cfg.ListenAddr,
				Handler:           srv.Handler(),
				ReadHeaderTimeout: 10 * time.Second, //nolint:mnd // standard timeout
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			go func() {
				slog.InfoContext(ctx, "starting server", "addr", cfg.ListenAddr)
				if listenErr := httpSrv.ListenAndServe(); listenErr != nil && listenErr != http.ErrServerClosed {
					slog.ErrorContext(ctx, "server error", "err", listenErr)
					os.Exit(1)
				}
			}()

			<-ctx.Done()
			slog.InfoContext(ctx, "shutting down server") //nolint:sloglint // no scoped logger yet
			return httpSrv.Shutdown(context.Background())
		},
	}
}

func cleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up stale preview environments",
		RunE: func(_ *cobra.Command, _ []string) error {
			slog.Info("cleanup command called") //nolint:sloglint // no context available in stub
			return nil
		},
	}
}
