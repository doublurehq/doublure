# Development

Local setup for doublure: ngrok tunnel, GitHub App, env, smoke test.

## Prerequisites

- Go 1.25+
- `ngrok`
- `gh` CLI, authenticated (`gh auth status`)
- GitHub account with admin access to the gitops repo

## Quickstart

```bash
# Terminal 1 — start the webhook tunnel
ngrok http 8080

# Terminal 2 — from services/doublure/
cd services/doublure
make dev-bootstrap         # shows a preflight plan, then (on y) creates the App + writes .env + PEM
# (browser opens; click "Create GitHub App", then "Install" on the repos you want)
# Paste the installation ID from the install URL into .env (GITHUB_INSTALLATION_ID=...)

make dev-verify            # confirms the credentials are good and shows installation IDs
make dev-serve             # starts the server
```

That's the whole loop. The sections below explain each step in case something goes wrong.

`make dev-bootstrap` prints a preflight plan describing every state change it's about to make — App name, webhook URL, file paths (with explicit "will overwrite" warnings) — and prompts before touching anything. Pass `-y` / `--yes` if you want to skip the prompt in scripts.

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
| `--name` | `doublure-dev-<hostname>` | App name (must be globally unique on GitHub) |
| `--owner` | (your personal account) | Org login if the App should live in an org |
| `--gitops-repo` | (empty) | Pre-fills `GITOPS_REPO` in `.env` |
| `--env-file` | `.env` | Where to write env vars |
| `--pem-file` | `dev.private-key.pem` | Where to write the private key |

The `Makefile` target forwards `GITOPS_REPO` if set, otherwise the `.env` is written with `GITOPS_REPO=` and a comment reminding you to fill it in:

```bash
GITOPS_REPO=myorg/my-gitops make dev-bootstrap
```

## Smoke the deploy endpoint

With the server running, paste this verbatim — `$ORG` resolves to your GitHub login via `gh`:

```bash
source .env
ORG=$(gh api /user --jq .login)

curl -s -X POST http://localhost:8080/v1/deploy \
  -H "Authorization: Bearer $DOUBLURE_DEPLOY_TOKEN" \
  -H "Content-Type: application/json" \
  -d @- <<EOF | jq
{
  "service": "backend-1",
  "head_ref": "feature/smoke-test",
  "commit_sha": "0000000000000000000000000000000000000001",
  "pr_author": "$ORG",
  "pr_url": "https://github.com/$ORG/backend-1/pull/1",
  "pr_number": "1",
  "workflow_run_url": "https://github.com/$ORG/backend-1/actions/runs/1"
}
EOF
```

The deploy itself is what's being smoked — a successful response means the gitops commit landed. Expect a `warnings` entry about PR-comment fan-out failing: the smoke uses a fake `pull/1` URL that doesn't exist, so the comment POST gets a 404. That's the test's design (best-effort fan-out, load-bearing write).

```json
{
  "preview_slug": "feature-smoke-test",
  "gitops_commit_url": "https://github.com/.../commit/...",
  "warnings": ["service backend-1 ...: 404 Not Found"]
}
```

Open the gitops repo on GitHub to confirm the commit landed on `main`.

## Verify webhook plumbing

GitHub App settings → **Advanced** → **Recent Deliveries** → pick any → **Redeliver**. The server answers 200 and logs one line per event type (`webhook ping`, `webhook installation`, `webhook installation_repositories`, `webhook pull_request`); other event types log `unhandled webhook event` (or `webhook parse failed` if go-github doesn't know the type). A 401 means the webhook secret in `.env` doesn't match what GitHub has — fix on either side.

To exercise the handler without GitHub, sign the body yourself. The HMAC covers the exact body bytes, so use `printf '%s'` (no trailing newline), never `echo`:

```bash
source .env
sign() { printf 'sha256=%s' "$(printf '%s' "$1" | openssl dgst -sha256 -hmac "$GITHUB_WEBHOOK_SECRET" | awk '{print $NF}')"; }
hook() { curl -s -o /dev/null -w "HTTP %{http_code}\n" -X POST http://localhost:8080/webhooks/github \
  -H "X-Github-Event: $1" -H "X-Hub-Signature-256: $2" -H "Content-Type: application/json" -d "$3"; }

hook ping 'sha256=deadbeef' '{"zen":"forged"}'                          # 401, handler never runs
B='{not valid json'; hook pull_request "$(sign "$B")" "$B"               # 200 + "webhook parse failed"
B='{"zen":"manual test","hook_id":1}'; hook ping "$(sign "$B")" "$B"     # 200 + "webhook ping"
```

A signed-but-unparseable delivery is logged and accepted with 200 so GitHub doesn't keep redelivering it.

## Appendix A — common failures

| Symptom | Cause | Fix |
|---|---|---|
| `ngrok inspector not reachable at http://127.0.0.1:4040/api/tunnels` | ngrok isn't running | Start `ngrok http 8080` |
| `no https tunnel found in ngrok` | ngrok is running but on a non-HTTPS protocol | `ngrok http 8080` (not tcp/tls) |
| `manifest exchange returned empty credentials` | The browser callback fired without a valid code | Retry `make dev-bootstrap`; check the GitHub tab for an error |
| `doublure verify-app` returns `401 A JSON web token could not be decoded` | `GITHUB_APP_ID` and `dev.private-key.pem` don't match | Re-run `make dev-bootstrap`, or regenerate the private key from the App settings page and replace `dev.private-key.pem` |
| `doublure verify-app` shows `Installations (0)` | App created but not installed anywhere | Open the App settings, click **Install App**, pick repos |
| `doublure serve` fails with `DOUBLURE_DEPLOY_TOKEN is required` | `.env` not sourced | `source .env` first, or use `make dev-serve` which sources for you |

## Appendix B — manual App creation (manifest-flow fallback)

If `make dev-bootstrap` doesn't work in your environment, create the App by hand:

1. https://github.com/settings/apps/new — fill in:
   - **GitHub App name**: `doublure-dev-<your-handle>` (globally unique)
   - **Homepage URL**: `http://localhost:8080`
   - **Webhook URL**: `<your-tunnel-url>/webhooks/github`
   - **Webhook secret**: `openssl rand -hex 32` — save the value
   - **Repository permissions**: Contents R/W, Pull requests W, Metadata R
   - **Subscribe to events**: `pull_request` (installation / installation_repositories fire automatically — do not subscribe to them explicitly)
   - **Where can this be installed?**: Only on this account
2. Click **Create GitHub App**. Note the **App ID** at the top of the page.
3. Scroll to **Private keys** → **Generate a private key**. Move the downloaded `.pem` to `services/doublure/dev.private-key.pem`.
4. Install the App (left sidebar → **Install App**) on the gitops repo and service repos.
5. Write `.env` manually:

   ```bash
   cat > .env <<EOF
   export GITHUB_APP_ID=<from step 2>
   export GITHUB_PRIVATE_KEY_PATH=./dev.private-key.pem
   export GITHUB_WEBHOOK_SECRET=<from step 1>
   export GITHUB_INSTALLATION_ID=<from the install URL>
   export GITOPS_REPO=<your-org>/<your-gitops-repo>
   export DOUBLURE_DEPLOY_TOKEN=$(openssl rand -hex 32)
   export LISTEN_ADDR=:8080
   EOF
   chmod 600 .env
   ```
