package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"

	"github.com/doublurehq/doublure/internal/config"
	"github.com/doublurehq/doublure/internal/github"
	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/server"
)

// shutdownTimeout bounds graceful shutdown: draining in-flight requests and
// then background teardowns share it, inside the default 30s pod grace period.
const shutdownTimeout = 25 * time.Second

func main() {
	root := &cobra.Command{
		Use:          "doublure",
		Short:        "Preview environment manager",
		SilenceUsage: true,
	}

	root.AddCommand(serveCmd())
	root.AddCommand(cleanupCmd())
	root.AddCommand(verifyAppCmd())
	root.AddCommand(bootstrapAppCmd())

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

			srv := server.NewServer(app, gitopsClient, cfg.DeployToken, cfg.InstallationID)
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
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if shutdownErr := httpSrv.Shutdown(shutdownCtx); shutdownErr != nil {
				return shutdownErr
			}
			return srv.WaitBackground(shutdownCtx)
		},
	}
}

func cleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up stale preview environments (not implemented yet)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("cleanup is not implemented yet")
		},
	}
}

// verifyAppCmd hits GET /app with App-JWT auth using GITHUB_APP_ID and the
// PEM at GITHUB_PRIVATE_KEY_PATH (or inline GITHUB_PRIVATE_KEY). On success
// it prints the App's identity and lists every installation reachable with
// these credentials — the installation IDs needed for GITHUB_INSTALLATION_ID.
//
// Intended for operator use after creating or rotating a GitHub App: confirms
// that the (App ID, private key) pair is valid before standing up the server.
func verifyAppCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify-app",
		Short: "Verify GitHub App credentials and list installations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			appIDStr := os.Getenv("GITHUB_APP_ID")
			if appIDStr == "" {
				return errors.New("GITHUB_APP_ID is required")
			}
			appID, err := strconv.ParseInt(appIDStr, 10, 64)
			if err != nil {
				return fmt.Errorf("GITHUB_APP_ID must be numeric: %w", err)
			}
			pem, err := config.LoadPrivateKey()
			if err != nil {
				return err
			}

			tr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, pem)
			if err != nil {
				return fmt.Errorf("loading PEM: %w", err)
			}
			client := gh.NewClient(&http.Client{Transport: tr})

			app, _, err := client.Apps.Get(cmd.Context(), "")
			if err != nil {
				return fmt.Errorf("GET /app: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "App credentials OK")
			fmt.Fprintf(cmd.OutOrStdout(), "  id:    %d\n", app.GetID())
			fmt.Fprintf(cmd.OutOrStdout(), "  slug:  %s\n", app.GetSlug())
			fmt.Fprintf(cmd.OutOrStdout(), "  name:  %s\n", app.GetName())
			fmt.Fprintf(cmd.OutOrStdout(), "  owner: %s\n", app.GetOwner().GetLogin())
			fmt.Fprintf(cmd.OutOrStdout(), "  url:   %s\n", app.GetHTMLURL())

			insts, _, err := client.Apps.ListInstallations(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("list installations: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nInstallations (%d):\n", len(insts))
			if len(insts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "  (none — install the App on an account before running `doublure serve`)")
			}
			for _, ins := range insts {
				fmt.Fprintf(cmd.OutOrStdout(), "  - id=%d  account=%s (%s)\n",
					ins.GetID(), ins.GetAccount().GetLogin(), ins.GetAccount().GetType())
			}
			return nil
		},
	}
}
