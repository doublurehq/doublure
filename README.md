# doublure

Per-pull-request preview environments for teams running Kubernetes and ArgoCD. Self-hosted, open source.

Every branch gets its own isolated, full-stack environment: the services changed in the PR run at their PR version, every other service runs at `main`. doublure writes the desired state to your gitops repo; your ArgoCD deploys it. doublure never touches your cluster directly.

## How it works

1. CI in a service repo builds and pushes the image, then calls `POST /v1/deploy`.
2. doublure writes `previews/<branch-slug>/values.yaml` to your gitops repo (through your GitHub App) and comments on every PR in that preview.
3. An ArgoCD ApplicationSet picks up `previews/*` and deploys one namespace per preview, one Application per service.
4. When the preview is stale, `doublure cleanup` removes its directory and ArgoCD prunes it.

```
POST /v1/deploy
Authorization: Bearer $DOUBLURE_DEPLOY_TOKEN

{ "service": "backend-1", "head_ref": "feature/pricing", "commit_sha": "<sha>",
  "pr_url": "...", "pr_number": "42", "pr_author": "...", "workflow_run_url": "..." }
```

## Status

Early development. Working: GitHub App auth, `/v1/deploy`, gitops writes with conflict retry, PR comments, webhook verification. Not yet: teardown on PR close, cleanup sweeper, Helm chart, release images.

## Usage

```bash
doublure serve           # API + webhook server
doublure cleanup         # one-shot stale preview cleanup (stub)
doublure bootstrap-app   # create a GitHub App for doublure via the manifest flow
doublure verify-app      # check GitHub App credentials, list installations
```

Configuration (env):

| Variable | Required | |
|---|---|---|
| `GITHUB_APP_ID` | yes | |
| `GITHUB_PRIVATE_KEY_PATH` or `GITHUB_PRIVATE_KEY` | yes | PEM file path or inline PEM |
| `GITHUB_WEBHOOK_SECRET` | yes | |
| `GITHUB_INSTALLATION_ID` | yes | |
| `GITOPS_REPO` | yes | `owner/repo` |
| `DOUBLURE_DEPLOY_TOKEN` | yes | bearer token CI uses for `/v1/deploy` |
| `LISTEN_ADDR` | no | default `:8080` |

Local setup: [DEVELOPMENT.md](DEVELOPMENT.md).

## License

[Apache-2.0](LICENSE)
