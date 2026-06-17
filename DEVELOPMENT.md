# Development

Local setup for previo: ngrok tunnel, GitHub App, env, smoke test.

## Prerequisites

- Go 1.25+
- `ngrok`
- GitHub account with admin access to the gitops repo

## Quickstart

```bash
# Terminal 1 — start the webhook tunnel
ngrok http 8080

# Terminal 2 — from services/previo/
cd services/previo
make dev-bootstrap         # creates the GitHub App, writes .env + dev.private-key.pem
# (browser opens; click "Create GitHub App", then "Install" on the repos you want)
# Paste the installation ID from the install URL into .env (GITHUB_INSTALLATION_ID=...)

make dev-verify            # confirms the credentials are good and shows installation IDs
make dev-serve             # starts the server
```

That's the whole loop. The sections below explain each step in case something goes wrong.

## What `make dev-bootstrap` does

1. Reads the public HTTPS URL of your ngrok tunnel from the local ngrok inspector (`http://127.0.0.1:4040/api/tunnels`).
2. Generates a webhook secret and a deploy token locally.
3. Starts a temp HTTP listener on an ephemeral port and opens your browser to a page that POSTs a GitHub App manifest to `github.com/settings/apps/new`.
4. After you click **Create GitHub App**, GitHub redirects back to the listener with a one-time code.
5. Exchanges the code at `POST /app-manifests/:code/conversions` for the App ID, PEM, and webhook secret.
6. Writes `dev.private-key.pem` and `.env` in the current directory.
7. Opens the App's install page so you can attach it to the gitops repo + service repos.

After install, the browser URL ends with `/settings/installations/<ID>`. Paste that into `.env` as `GITHUB_INSTALLATION_ID`.

Flags worth knowing:

| Flag | Default | Purpose |
|---|---|---|
| `--name` | `previo-dev-<hostname>` | App name (must be globally unique on GitHub) |
| `--owner` | (your personal account) | Org login if the App should live in an org |
| `--gitops-repo` | (empty) | Pre-fills `GITOPS_REPO` in `.env` |
| `--env-file` | `.env` | Where to write env vars |
| `--pem-file` | `dev.private-key.pem` | Where to write the private key |

The `Makefile` target passes `--gitops-repo $(GITOPS_REPO)` (default `amine7536/akpe-gitops`); override with `GITOPS_REPO=other/repo make dev-bootstrap`.

## Smoke the deploy endpoint

With the server running:

```bash
source .env
curl -s -X POST http://localhost:8080/v1/deploy \
  -H "Authorization: Bearer $PREVIO_DEPLOY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service": "backend-1",
    "head_ref": "feature/smoke-test",
    "commit_sha": "0000000000000000000000000000000000000001",
    "pr_author": "amine",
    "pr_url": "https://github.com/amine7536/backend-1/pull/1",
    "pr_number": "1",
    "workflow_run_url": "https://github.com/amine7536/backend-1/actions/runs/1"
  }' | jq
```

Expected:

```json
{
  "preview_slug": "feature-smoke-test",
  "gitops_commit_url": "https://github.com/amine7536/akpe-gitops/commit/<sha>"
}
```

The commit lands on the gitops repo's `main` branch.

## Verify webhook plumbing

GitHub App settings → **Advanced** → **Recent Deliveries** → pick any → **Redeliver**. The server logs `received webhook event=<type> size=N` with a 200. A 401 means the webhook secret in `.env` doesn't match what GitHub has — fix on either side.

## Appendix A — common failures

| Symptom | Cause | Fix |
|---|---|---|
| `ngrok inspector not reachable at http://127.0.0.1:4040/api/tunnels` | ngrok isn't running | Start `ngrok http 8080` |
| `no https tunnel found in ngrok` | ngrok is running but on a non-HTTPS protocol | `ngrok http 8080` (not tcp/tls) |
| `manifest exchange returned empty credentials` | The browser callback fired without a valid code | Retry `make dev-bootstrap`; check the GitHub tab for an error |
| `previo verify-app` returns `401 A JSON web token could not be decoded` | `GITHUB_APP_ID` and `dev.private-key.pem` don't match | Re-run `make dev-bootstrap`, or regenerate the private key from the App settings page and replace `dev.private-key.pem` |
| `previo verify-app` shows `Installations (0)` | App created but not installed anywhere | Open the App settings, click **Install App**, pick repos |
| `previo serve` fails with `PREVIO_DEPLOY_TOKEN is required` | `.env` not sourced | `source .env` first, or use `make dev-serve` which sources for you |

## Appendix B — manual App creation (manifest-flow fallback)

If `make dev-bootstrap` doesn't work in your environment, create the App by hand:

1. https://github.com/settings/apps/new — fill in:
   - **GitHub App name**: `previo-dev-<your-handle>` (globally unique)
   - **Homepage URL**: `http://localhost:8080`
   - **Webhook URL**: `<your-tunnel-url>/webhooks/github`
   - **Webhook secret**: `openssl rand -hex 32` — save the value
   - **Repository permissions**: Contents R/W, Pull requests W, Metadata R
   - **Subscribe to events**: `installation`, `installation_repositories`, `pull_request`
   - **Where can this be installed?**: Only on this account
2. Click **Create GitHub App**. Note the **App ID** at the top of the page.
3. Scroll to **Private keys** → **Generate a private key**. Move the downloaded `.pem` to `services/previo/dev.private-key.pem`.
4. Install the App (left sidebar → **Install App**) on the gitops repo and service repos.
5. Write `.env` manually:

   ```bash
   cat > .env <<EOF
   export GITHUB_APP_ID=<from step 2>
   export GITHUB_PRIVATE_KEY_PATH=./dev.private-key.pem
   export GITHUB_WEBHOOK_SECRET=<from step 1>
   export GITHUB_INSTALLATION_ID=<from the install URL>
   export GITOPS_REPO=amine7536/akpe-gitops
   export PREVIO_DEPLOY_TOKEN=$(openssl rand -hex 32)
   export LISTEN_ADDR=:8080
   EOF
   chmod 600 .env
   ```

The authoritative spec for permissions, events, and the production webhook URL is [`saas/GITHUB-APP.md`](../../saas/GITHUB-APP.md).
