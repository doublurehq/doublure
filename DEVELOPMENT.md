# Development

End-to-end local setup for previo: GitHub App, webhook tunnel, env vars, smoke test.

## Prerequisites

- Go 1.25+
- `gh` CLI (optional but useful)
- `ngrok` or `smee.io` for inbound webhook tunneling
- A GitHub account where you can create Apps and have admin access to one or more repos

## 1. Tunnel inbound webhooks

GitHub posts webhooks to a public URL. Two options:

**Option A — ngrok (recommended for HTTP debugging via the dashboard):**

```bash
brew install ngrok
ngrok config add-authtoken <token>     # one-time, from dashboard.ngrok.com
ngrok http 8080
```

Note the forwarding URL (`https://<random>.ngrok-free.app`). Free-tier URLs change on every restart. To avoid editing the App webhook URL repeatedly, claim a free static domain at `dashboard.ngrok.com/domains` and run:

```bash
ngrok http 8080 --url=<your>.ngrok-free.app
```

**Option B — smee.io (stable URL with zero account setup):**

```bash
# Create channel: visit https://smee.io and click "Start a new channel"
npx smee-client --url https://smee.io/<channel> --target http://localhost:8080/webhooks/github
```

## 2. Create the GitHub App

Open https://github.com/settings/apps/new and fill in:

| Field | Value |
|---|---|
| GitHub App name | `previo-dev-<your-handle>` (globally unique) |
| Homepage URL | `http://localhost:8080` (not validated) |
| Webhook URL | `https://<your-tunnel>/webhooks/github` |
| Webhook secret | `openssl rand -hex 32` — save it |
| Repository permissions | Contents R/W · Pull requests W · Metadata R |
| Subscribe to events | `installation`, `installation_repositories`, `pull_request` |
| Where can this be installed? | Only on this account |

The authoritative spec for permissions and events is [`saas/GITHUB-APP.md`](../../saas/GITHUB-APP.md).

After clicking **Create**:
- Note the **App ID** at the top of the settings page.
- Scroll down to **Private keys** → **Generate a private key**. A `.pem` file downloads. Move it to `services/previo/dev.private-key.pem` (overwriting any stale file).

## 3. Install the App

On the App settings page, click **Install App** in the left sidebar, then **Install** next to your account. Choose **Only select repositories** and pick at least:

- `akpe-gitops` (or your gitops repo) — required for the values.yaml writes
- `backend-1`, `backend-2`, `front` — optional, only if you want PR-comment fan-out to reach them

After install, the browser URL ends with `/settings/installations/<ID>`. That number is your **installation ID**.

## 4. Wire `.env`

```bash
cat > services/previo/.env <<'EOF'
export GITHUB_APP_ID=<from step 2>
export GITHUB_PRIVATE_KEY_PATH=./dev.private-key.pem
export GITHUB_WEBHOOK_SECRET=<from step 2>
export GITHUB_INSTALLATION_ID=<from step 3>
export GITOPS_REPO=amine7536/akpe-gitops
export PREVIO_DEPLOY_TOKEN=$(openssl rand -hex 32)
export LISTEN_ADDR=:8080
EOF
chmod 600 services/previo/.env
```

`.env` is ignored by git via the global `.gitignore`. Do not commit it.

## 5. Verify the App credentials

Before running the server, sanity-check that the (App ID, PEM) pair is valid and the App is installed where you expect:

```bash
cd services/previo
source .env
go run ./cmd/previo verify-app
```

Expected:

```
App credentials OK
  id:    123456
  slug:  previo-dev-yourhandle
  name:  previo-dev-yourhandle
  owner: yourhandle
  url:   https://github.com/apps/previo-dev-yourhandle

Installations (1):
  - id=87654321  account=yourhandle (User)
```

Common failures:
- `401 A JSON web token could not be decoded` → the PEM does not match this App ID. Regenerate the private key from the App settings page and replace `dev.private-key.pem`, or fix `GITHUB_APP_ID`.
- `Installations (0)` → the App exists but isn't installed anywhere. Go back to step 3.

## 6. Run the server

```bash
cd services/previo
source .env
go run ./cmd/previo serve
```

Logs: `starting server addr=:8080`.

## 7. Verify the webhook plumbing

In the GitHub App settings, go to **Advanced** → **Recent Deliveries**. The install creates a `ping` delivery. Click any delivery → **Redeliver**. previo logs should show:

```
received webhook event=ping size=N
```

with a 200 response. A 401 means the webhook secret in `.env` doesn't match what GitHub has — fix on either side.

## 8. Smoke the deploy endpoint

```bash
curl -s -X POST http://localhost:8080/v1/deploy \
  -H "Authorization: Bearer $PREVIO_DEPLOY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "service": "backend-1",
    "head_ref": "feature/smoke-test",
    "commit_sha": "0000000000000000000000000000000000000001",
    "pr_author": "yourhandle",
    "pr_url": "https://github.com/yourhandle/backend-1/pull/1",
    "pr_number": "1",
    "workflow_run_url": "https://github.com/yourhandle/backend-1/actions/runs/1"
  }' | jq
```

Expected response:

```json
{
  "preview_slug": "feature-smoke-test",
  "gitops_commit_url": "https://github.com/amine7536/akpe-gitops/commit/<sha>"
}
```

Verify the commit landed at `github.com/<your-gitops-repo>/commits/main`.

## Teardown

- Stop `previo serve` (Ctrl+C)
- Stop ngrok / smee
- The preview values file in the gitops repo can be left in place; it will be cleaned up later by the cleanup binary (Phase 5) or manually with `rm -rf previews/feature-smoke-test`.
