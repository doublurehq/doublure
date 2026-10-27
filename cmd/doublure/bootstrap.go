package main

import (
	"bufio"
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
		assumeYes  bool
	)
	cmd := &cobra.Command{
		Use:   "bootstrap-app",
		Short: "Create a dev GitHub App via manifest flow and write .env + PEM",
		Long: `Create a development GitHub App through the manifest flow.

Prints a preflight plan describing what will be created, what files will
be written, and what will be overwritten — then prompts for confirmation
before doing anything (skip with --yes).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), manifestTimeout)
			defer cancel()
			return runBootstrap(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), bootstrapOpts{
				appName:    appName,
				gitopsRepo: gitopsRepo,
				envPath:    envPath,
				pemPath:    pemPath,
				owner:      owner,
				assumeYes:  assumeYes,
			})
		},
	}
	cmd.Flags().StringVar(&appName, "name", defaultAppName(), "App name (must be globally unique)")
	cmd.Flags().StringVar(&gitopsRepo, "gitops-repo", "", "GITOPS_REPO value to write into .env (owner/repo)")
	cmd.Flags().StringVar(&envPath, "env-file", ".env", "Path to write the .env file")
	cmd.Flags().StringVar(&pemPath, "pem-file", "dev.private-key.pem", "Path to write the App private key")
	cmd.Flags().StringVar(&owner, "owner", "", "Org login to install the App under (empty = personal account)")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

type bootstrapOpts struct {
	appName    string
	gitopsRepo string
	envPath    string
	pemPath    string
	owner      string
	assumeYes  bool
}

func runBootstrap(ctx context.Context, stdin io.Reader, stdout io.Writer, opts bootstrapOpts) error {
	webhookURL, err := readNgrokTunnel()
	if err != nil {
		return err
	}

	printPlan(stdout, opts, webhookURL)
	if !opts.assumeYes {
		ok, confirmErr := confirm(stdin, stdout)
		if confirmErr != nil {
			return confirmErr
		}
		if !ok {
			fmt.Fprintln(stdout, "Aborted.")
			return nil
		}
	}
	fmt.Fprintln(stdout)

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
		// `installation` and `installation_repositories` are App-level events
		// delivered automatically regardless of subscriptions — including them
		// here makes GitHub reject the manifest. Only repository-scoped events
		// belong in default_events.
		"default_events": []string{"pull_request"},
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
	fmt.Fprintln(stdout, "then run `doublure verify-app` to confirm.")
	return nil
}

// printPlan emits a preflight summary describing every state change the
// command is about to make: the App being created, the URL it points at, the
// files that will be written, and whether any of them will be overwritten.
// Reads cleanly in a terminal without color so it's safe in any environment.
func printPlan(w io.Writer, opts bootstrapOpts, webhookURL string) {
	fmt.Fprintln(w, "Preflight — `doublure bootstrap-app` will:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  1. Create a new GitHub App")
	fmt.Fprintf(w, "       name:        %s\n", opts.appName)
	fmt.Fprintf(w, "       webhook URL: %s/webhooks/github\n", webhookURL)
	if opts.owner != "" {
		fmt.Fprintf(w, "       owner:       %s (organization)\n", opts.owner)
	} else {
		fmt.Fprintln(w, "       owner:       your personal GitHub account")
	}
	fmt.Fprintln(w, "       permissions: contents:write, pull_requests:write, metadata:read")
	fmt.Fprintln(w, "       events:      pull_request (installation events fire automatically)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  2. Open a browser tab to github.com/settings/apps/new and submit the manifest")
	fmt.Fprintln(w, "     (one click on \"Create GitHub App\" required from you)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  3. Write files locally (mode 0600):")
	fmt.Fprintf(w, "       %s   %s\n", opts.pemPath, overwriteNote(opts.pemPath))
	fmt.Fprintf(w, "       %s   %s\n", opts.envPath, overwriteNote(opts.envPath))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  4. Open the App install page in your browser so you can attach it to repos")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Nothing is sent to GitHub before step 2. Nothing is written to disk before step 3.")
}

// overwriteNote returns a parenthetical warning if path already exists.
func overwriteNote(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "(will overwrite existing file)"
	}
	return "(new file)"
}

// confirm prompts the user for y/N and returns true only on an explicit
// "y" or "yes" (case-insensitive). Anything else — including just hitting
// enter — is treated as a no.
func confirm(in io.Reader, w io.Writer) (bool, error) {
	fmt.Fprint(w, "\nProceed? [y/N] ")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("reading confirmation: %w", err)
		}
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
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

// defaultAppName returns "doublure-dev-" plus 6 random lowercase hex chars.
// GitHub App names are global, so the suffix avoids collisions without
// publishing anything about the developer's machine.
func defaultAppName() string {
	buf := make([]byte, 3) //nolint:mnd // 3 bytes = 6 hex chars
	_, _ = rand.Read(buf)  // crypto/rand.Read never returns an error
	return "doublure-dev-" + hex.EncodeToString(buf)
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
export DOUBLURE_DEPLOY_TOKEN=%s
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
