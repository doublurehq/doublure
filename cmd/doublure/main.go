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
	"sync"
	"syscall"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"

	"github.com/doublurehq/doublure/internal/config"
	"github.com/doublurehq/doublure/internal/github"
	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/metrics"
	"github.com/doublurehq/doublure/internal/server"
)

// shutdownTimeout bounds graceful shutdown: draining in-flight requests and
// then background teardowns share it, inside the default 30s pod grace period.
const shutdownTimeout = 25 * time.Second

// cleanupTimeout bounds one cleanup sweep over every preview.
const cleanupTimeout = 10 * time.Minute

// readHeaderTimeout bounds reading a request's headers on every listener.
const readHeaderTimeout = 10 * time.Second

// minCleanupInterval is the shortest CLEANUP_INTERVAL accepted.
const minCleanupInterval = time.Minute

// sweepAPI is the gitops client with the metrics recorder lifecycle.Sweep
// expects. A nil *metrics.Metrics records nothing.
type sweepAPI struct {
	*gitops.Client
	*metrics.Metrics
}

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

			interval, ttl, err := sweepSchedule()
			if err != nil {
				return err
			}

			var m *metrics.Metrics
			if cfg.MetricsAddr != "" {
				m = metrics.New()
			}

			app, err := newApp(cfg)
			if err != nil {
				return err
			}

			gitopsClient, err := newGitopsClient(app, cfg, gitops.WithMetrics(m))
			if err != nil {
				return err
			}

			srv := server.NewServer(app, gitopsClient, cfg.DeployToken, cfg.InstallationID, server.WithMetrics(m))
			httpSrv := &http.Server{
				Addr:              cfg.ListenAddr,
				Handler:           srv.Handler(),
				ReadHeaderTimeout: readHeaderTimeout,
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			serveInBackground(ctx, "server", httpSrv)
			metricsSrv := startMetricsServer(ctx, cfg.MetricsAddr, m)
			startSweepLoop(ctx, srv, sweepAPI{Client: gitopsClient, Metrics: m}, m, interval, ttl)

			<-ctx.Done()
			slog.InfoContext(ctx, "shutting down server") //nolint:sloglint // no scoped logger yet
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if shutdownErr := shutdownServers(shutdownCtx, httpSrv, metricsSrv); shutdownErr != nil {
				return shutdownErr
			}
			return srv.WaitBackground(shutdownCtx)
		},
	}
}

// startMetricsServer serves GET /metrics from m on addr in the background and
// returns its server. It starts nothing and returns nil when m is nil.
func startMetricsServer(ctx context.Context, addr string, m *metrics.Metrics) *http.Server {
	if m == nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	metricsSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	serveInBackground(ctx, "metrics server", metricsSrv)
	return metricsSrv
}

// serveInBackground runs srv.ListenAndServe in the background, logging it as
// name, and exits the process when it fails for any reason but shutdown.
func serveInBackground(ctx context.Context, name string, srv *http.Server) {
	logger := slog.Default()
	go func() {
		logger.InfoContext(ctx, "starting "+name, "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, name+" error", "err", err)
			os.Exit(1)
		}
	}()
}

// sweepSchedule reads CLEANUP_INTERVAL and, when it is set, CLEANUP_TTL: the
// in-server sweep's interval (0 when off) and TTL.
func sweepSchedule() (time.Duration, time.Duration, error) {
	interval, err := parseCleanupInterval(os.Getenv("CLEANUP_INTERVAL"))
	if err != nil || interval == 0 {
		return 0, 0, err
	}
	ttl, err := parseCleanupTTL(os.Getenv("CLEANUP_TTL"))
	if err != nil {
		return 0, 0, err
	}
	return interval, ttl, nil
}

// startSweepLoop runs runSweepLoop in the background of srv, so shutdown
// waits for it. It starts nothing when interval is 0.
func startSweepLoop(
	ctx context.Context,
	srv *server.Server,
	api lifecycle.SweepAPI,
	m *metrics.Metrics,
	interval, ttl time.Duration,
) {
	if interval == 0 {
		return
	}
	slog.Default().InfoContext(ctx, "starting cleanup loop",
		slog.String("interval", interval.String()),
		slog.String("ttl", ttl.String()),
	)
	srv.Go(func() {
		runSweepLoop(ctx, api, m, interval, ttl, time.Now)
	})
}

// runSweepLoop sweeps every interval until ctx is done, starting one interval
// from now. Runs never overlap: a tick that comes during a run waits for it.
// Each run is bounded by cleanupTimeout and cancelled with ctx.
func runSweepLoop(
	ctx context.Context,
	api lifecycle.SweepAPI,
	m *metrics.Metrics,
	interval, ttl time.Duration,
	now func() time.Time,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		runSweep(ctx, api, m, ttl, now)
	}
}

// runSweep runs one sweep and records it, unless ctx was done: a sweep
// cancelled by shutdown is not recorded, one that hit cleanupTimeout is
// recorded as failed.
func runSweep(
	ctx context.Context,
	api lifecycle.SweepAPI,
	m *metrics.Metrics,
	ttl time.Duration,
	now func() time.Time,
) {
	sctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	err := lifecycle.Sweep(sctx, api, now().UTC(), ttl)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		slog.Default().ErrorContext(ctx, "cleanup failed", slog.String("err", err.Error()))
	}
	m.SweepRun(err == nil, now())
}

// shutdownServers shuts every non-nil server down concurrently, within ctx,
// and returns their joined errors.
func shutdownServers(ctx context.Context, servers ...*http.Server) error {
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		if srv == nil {
			continue
		}
		wg.Go(func() {
			errs[i] = srv.Shutdown(ctx)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// newGitopsClient builds the gitops repo client from cfg, authenticated as
// the GitHub App installation, with opts.
func newGitopsClient(app *github.App, cfg *config.Config, opts ...gitops.Option) (*gitops.Client, error) {
	owner, repo, ok := strings.Cut(cfg.GitOpsRepo, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("GITOPS_REPO must be 'owner/repo', got %q", cfg.GitOpsRepo)
	}
	return gitops.NewClient(app.ClientForInstallation(cfg.InstallationID), owner, repo, opts...), nil
}

// newApp builds the GitHub App from the loaded config.
func newApp(cfg *config.Config) (*github.App, error) {
	app, err := github.NewApp(github.AppConfig{
		AppID:         cfg.AppID,
		PrivateKey:    cfg.PrivateKey,
		WebhookSecret: cfg.WebhookSecret,
	})
	if err != nil {
		return nil, fmt.Errorf("creating github app: %w", err)
	}
	return app, nil
}

// cleanupCmd runs one sweep over the gitops repo's previews: previews with no
// pin are deleted, previews idle for longer than CLEANUP_TTL (when set) are
// deleted, and pins of closed PRs are torn down.
func cleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Delete stale and expired preview environments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			ttl, err := parseCleanupTTL(os.Getenv("CLEANUP_TTL"))
			if err != nil {
				return err
			}
			app, err := newApp(cfg)
			if err != nil {
				return err
			}
			client, err := newGitopsClient(app, cfg)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), cleanupTimeout)
			defer cancel()
			return lifecycle.Sweep(ctx, sweepAPI{Client: client}, time.Now().UTC(), ttl)
		},
	}
}

// parseCleanupInterval parses CLEANUP_INTERVAL. Empty means no in-server
// sweep (0); otherwise it must be a Go duration of at least
// minCleanupInterval.
func parseCleanupInterval(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < minCleanupInterval {
		return 0, fmt.Errorf("CLEANUP_INTERVAL must be a duration of at least 1m, got %q", raw)
	}
	return interval, nil
}

// parseCleanupTTL parses CLEANUP_TTL. Empty means no expiry (0); otherwise it
// must be a positive Go duration.
func parseCleanupTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return 0, fmt.Errorf("CLEANUP_TTL must be a positive duration, got %q", raw)
	}
	return ttl, nil
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
