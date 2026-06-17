package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	gh "github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"
)

// Manifest flow constants. Values reflect dev defaults; nothing here is a
// secret. The listener picks an ephemeral port at startup and threads it
// through redirectURL; ngrokAPI is the well-known local URL of the ngrok
// inspector.
const (
	manifestServerHost = "127.0.0.1"
	ngrokAPI           = "http://127.0.0.1:4040/api/tunnels"
	manifestTimeout    = 5 * time.Minute
)

// bootstrapAppCmd creates a fresh dev GitHub App via the manifest flow and
// writes its credentials to dev.private-key.pem and .env in the current
// working directory. Requires a running ngrok tunnel on :8080 — the public
// URL becomes the App's webhook endpoint.
//
// Documented at https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest.
func bootstrapAppCmd() *cobra.Command {
	var (
		appName    string
		gitopsRepo string
		envPath    string
		pemPath    string
		owner      string
	)
	cmd := &cobra.Command{
		Use:   "bootstrap-app",
		Short: "Create a dev GitHub App via manifest flow and write .env + PEM",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), manifestTimeout)
			defer cancel()
			return runBootstrap(ctx, cmd.OutOrStdout(), bootstrapOpts{
				appName:    appName,
				gitopsRepo: gitopsRepo,
				envPath:    envPath,
				pemPath:    pemPath,
				owner:      owner,
			})
		},
	}
	host, _ := os.Hostname()
	cmd.Flags().StringVar(&appName, "name", "previo-dev-"+sanitizeHost(host), "App name (must be globally unique)")
	cmd.Flags().StringVar(&gitopsRepo, "gitops-repo", "", "GITOPS_REPO value to write into .env (owner/repo)")
	cmd.Flags().StringVar(&envPath, "env-file", ".env", "Path to write the .env file")
	cmd.Flags().StringVar(&pemPath, "pem-file", "dev.private-key.pem", "Path to write the App private key")
	cmd.Flags().StringVar(&owner, "owner", "", "Org login to install the App under (empty = personal account)")
	return cmd
}

type bootstrapOpts struct {
	appName    string
	gitopsRepo string
	envPath    string
	pemPath    string
	owner      string
}

func runBootstrap(ctx context.Context, stdout io.Writer, opts bootstrapOpts) error {
	webhookURL, err := readNgrokTunnel()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Using webhook URL: %s/webhooks/github\n", webhookURL)

	state, err := randomHex(16)
	if err != nil {
		return fmt.Errorf("generating state: %w", err)
	}

	ln, err := net.Listen("tcp", manifestServerHost+":0")
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	defer ln.Close()
	localAddr := ln.Addr().String()
	redirectURL := fmt.Sprintf("http://%s/callback", localAddr)
	startURL := fmt.Sprintf("http://%s/start", localAddr)

	manifest := map[string]any{
		"name": opts.appName,
		"url":  "http://localhost:8080",
		"hook_attributes": map[string]any{
			"url":    webhookURL + "/webhooks/github",
			"active": true,
		},
		"redirect_url": redirectURL,
		"public":       false,
		"default_permissions": map[string]string{
			"contents":      "write",
			"pull_requests": "write",
			"metadata":      "read",
		},
		"default_events": []string{
			"installation",
			"installation_repositories",
			"pull_request",
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{
		Handler:           bootstrapMux(string(manifestJSON), state, opts.owner, codeCh, errCh),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	fmt.Fprintf(stdout, "Opening browser: %s\n", startURL)
	if openErr := openBrowser(startURL); openErr != nil {
		fmt.Fprintf(stdout, "  (could not open browser automatically: %v)\n  Open this URL manually.\n", openErr)
	}

	var code string
	select {
	case code = <-codeCh:
	case bErr := <-errCh:
		return bErr
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for GitHub callback: %w", ctx.Err())
	}

	client := gh.NewClient(nil)
	conv, _, err := client.Apps.CompleteAppManifest(ctx, code)
	if err != nil {
		return fmt.Errorf("exchanging manifest code: %w", err)
	}
	if conv.GetPEM() == "" || conv.GetID() == 0 {
		return errors.New("manifest exchange returned empty credentials")
	}

	if writeErr := os.WriteFile(opts.pemPath, []byte(conv.GetPEM()), 0o600); writeErr != nil {
		return fmt.Errorf("writing PEM: %w", writeErr)
	}
	deployToken, err := randomHex(32)
	if err != nil {
		return fmt.Errorf("generating deploy token: %w", err)
	}
	envBody := renderEnv(envValues{
		AppID:         conv.GetID(),
		PEMPath:       opts.pemPath,
		WebhookSecret: conv.GetWebhookSecret(),
		GitOpsRepo:    opts.gitopsRepo,
		DeployToken:   deployToken,
	})
	if writeErr := os.WriteFile(opts.envPath, []byte(envBody), 0o600); writeErr != nil {
		return fmt.Errorf("writing .env: %w", writeErr)
	}

	fmt.Fprintln(stdout, "\nApp created.")
	fmt.Fprintf(stdout, "  id:    %d\n", conv.GetID())
	fmt.Fprintf(stdout, "  slug:  %s\n", conv.GetSlug())
	fmt.Fprintf(stdout, "  url:   %s\n", conv.GetHTMLURL())
	fmt.Fprintf(stdout, "  pem:   %s\n", opts.pemPath)
	fmt.Fprintf(stdout, "  env:   %s\n", opts.envPath)

	installURL := conv.GetHTMLURL() + "/installations/new"
	fmt.Fprintf(stdout, "\nNext: install the App on the gitops repo + service repos:\n  %s\n", installURL)
	_ = openBrowser(installURL)
	fmt.Fprintln(stdout, "\nAfter install, paste the installation ID into .env (GITHUB_INSTALLATION_ID),")
	fmt.Fprintln(stdout, "then run `previo verify-app` to confirm.")
	return nil
}

// bootstrapMux serves /start (renders the self-submitting manifest form) and
// /callback (captures ?code= and validates state). Both routes are single-use.
func bootstrapMux(manifest, state, org string, codeCh chan<- string, errCh chan<- error) http.Handler {
	mux := http.NewServeMux()

	action := "https://github.com/settings/apps/new?state=" + state
	if org != "" {
		action = fmt.Sprintf("https://github.com/organizations/%s/settings/apps/new?state=%s", org, state)
	}

	startPage := template.Must(template.New("start").Parse(`<!doctype html>
<html><body>
<p>Redirecting to GitHub to create the App. Click the button if it does not redirect automatically.</p>
<form id="f" action="{{.Action}}" method="post">
  <input type="hidden" name="manifest" value='{{.Manifest}}'>
  <button type="submit">Continue to GitHub</button>
</form>
<script>document.getElementById('f').submit();</script>
</body></html>`))

	mux.HandleFunc("/start", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = startPage.Execute(w, map[string]string{
			"Action":   action,
			"Manifest": manifest,
		})
	})

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		gotState := r.URL.Query().Get("state")
		if gotState != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- errors.New("state mismatch on callback")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- errors.New("callback missing code")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
<p>GitHub App created. You can close this tab and return to the terminal.</p>
</body></html>`)
		codeCh <- code
	})
	return mux
}

// readNgrokTunnel queries the local ngrok inspector for the first https
// public_url. Fails fast with an actionable message if ngrok isn't running.
func readNgrokTunnel() (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ngrokAPI)
	if err != nil {
		return "", fmt.Errorf("ngrok inspector not reachable at %s — is `ngrok http 8080` running? (%w)", ngrokAPI, err)
	}
	defer resp.Body.Close()
	var body struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
			Proto     string `json:"proto"`
		} `json:"tunnels"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&body); decodeErr != nil {
		return "", fmt.Errorf("decoding ngrok tunnels: %w", decodeErr)
	}
	for _, t := range body.Tunnels {
		if t.Proto == "https" && strings.HasPrefix(t.PublicURL, "https://") {
			return t.PublicURL, nil
		}
	}
	return "", errors.New("no https tunnel found in ngrok — start one with `ngrok http 8080`")
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// sanitizeHost trims the macOS-style ".local" / ".lan" / ".home" suffix and
// lowercases so the default --name flag yields something GitHub will accept.
func sanitizeHost(h string) string {
	h = strings.ToLower(h)
	for _, suffix := range []string{".local", ".lan", ".home"} {
		h = strings.TrimSuffix(h, suffix)
	}
	if h == "" {
		return "host"
	}
	return h
}

type envValues struct {
	AppID         int64
	PEMPath       string
	WebhookSecret string
	GitOpsRepo    string
	DeployToken   string
}

func renderEnv(v envValues) string {
	gitops := v.GitOpsRepo
	if gitops == "" {
		gitops = "# set this — owner/repo of your gitops repository"
	}
	return fmt.Sprintf(`export GITHUB_APP_ID=%d
export GITHUB_PRIVATE_KEY_PATH=%s
export GITHUB_WEBHOOK_SECRET=%s
export GITHUB_INSTALLATION_ID= # set after installing the App
export GITOPS_REPO=%s
export PREVIO_DEPLOY_TOKEN=%s
export LISTEN_ADDR=:8080
`, v.AppID, v.PEMPath, v.WebhookSecret, gitops, v.DeployToken)
}

// openBrowser launches the platform-default browser for url. Failure is
// non-fatal; the URL is printed so the user can open it by hand.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
