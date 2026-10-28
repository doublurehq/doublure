# doublure

Per-pull-request preview environments for teams running Kubernetes and ArgoCD. Self-hosted, open source.

Every branch gets its own isolated, full-stack environment: the services changed in the PR run at their PR version, every other service runs at `main`. doublure writes the desired state to your gitops repo; your ArgoCD deploys it. doublure never touches your cluster directly.

## Why

When code is written at machine speed, the bottleneck is validating and integrating it, and a shared staging environment becomes a queue. doublure applies the practices from [AI Writes Code in Hours. It Still Takes Weeks to Ship.](https://medium.com/@aminebenseddik/ai-writes-code-in-hours-it-still-takes-weeks-to-ship-7fb07f87cc07):

- One isolated preview per branch, across repos: PRs on the same branch share one preview.
- Changed services run at their PR commit, everything else at `main`.
- CI builds once; doublure only writes desired state. It never builds images and never touches the cluster.
- Argo CD deploys from git.
- Previews are torn down when their PR closes or the label is removed, and swept so they don't accumulate.

## How it works

1. CI in a service repo builds and pushes the image.
2. Adding the `preview` label to the PR makes doublure write `previews/<slug>/values.yaml` (every catalog service, the PR's at its head SHA) and comment on the PR; a push to the labeled PR updates it.
3. An ArgoCD ApplicationSet picks up `previews/*` and deploys one namespace per preview, one Application per service.
4. Removing the label or closing the PR unpins its service, or deletes the file when no PR remains, and ArgoCD prunes the preview.
5. `doublure cleanup` (an hourly CronJob) sweeps every preview: it deletes previews with no pinned service, or with no deploy within `CLEANUP_TTL` when set, and tears down the pins of closed PRs that step 4 missed. ArgoCD prunes what it removes.

Manual trigger:

```
POST /v1/deploy
Authorization: Bearer $DOUBLURE_DEPLOY_TOKEN

{ "service": "backend-1", "head_ref": "feature/pricing", "commit_sha": "<sha>",
  "pr_url": "...", "pr_number": "42", "pr_author": "...", "workflow_run_url": "..." }
```

## Status

Early development. Working: GitHub App auth, label-triggered previews, `/v1/deploy`, gitops writes with conflict retry, PR comments, webhook verification.

## Usage

```bash
doublure serve           # API + webhook server
doublure cleanup         # stale preview cleanup
doublure bootstrap-app   # create a dev GitHub App (needs an ngrok tunnel; see DEVELOPMENT.md)
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
| `DOUBLURE_DEPLOY_TOKEN` | yes | bearer token for manual `/v1/deploy` calls |
| `LISTEN_ADDR` | no | default `:8080` |

Local setup: [DEVELOPMENT.md](DEVELOPMENT.md).

## Charts

| Chart | Purpose |
|---|---|
| [`charts/doublure`](charts/doublure) | Installs the doublure server; the cleanup CronJob is disabled by default |
| [`charts/preview-env`](charts/preview-env) | One preview: AppProject, Namespace, one ArgoCD Application per service |
| [`charts/app`](charts/app) | Generic service chart; service charts wrap it as a dependency aliased `app` |

ArgoCD wiring and install steps: [examples/argocd](examples/argocd/README.md). Charts are published to `oci://ghcr.io/doublurehq/charts`; see [CONTRIBUTING.md](CONTRIBUTING.md#releases) for how versions are set.

## Docs

- [Architecture](docs/ARCHITECTURE.md): components, gitops and service repo contracts, API
- [Roadmap](docs/ROADMAP.md): what's left for v1

## License

[Apache-2.0](LICENSE)
