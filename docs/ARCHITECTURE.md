# Architecture

Self-hosted, single-tenant. Everything runs in the user's cluster; doublure talks to GitHub through the user's own GitHub App and never touches the cluster directly: ArgoCD does the deploys.

```
 service repo PR ──► CI builds + pushes image ──► POST /v1/deploy ─┐
                                                                    ▼
 GitHub ── webhooks ──► POST /webhooks/github ─────────────► doublure serve ──► gitops repo
   ▲                                                         │    (GitHub App)    previews/<slug>/values.yaml
   └──────────────── PR comment (fan-out) ◄──────────────────┘                          │
                                                                                         ▼
                                     ArgoCD ApplicationSet (git dir generator: previews/*)
                                       └─ preview-env chart → AppProject + Namespace + one Application per service
                                            └─ service chart (k8s/ in each service repo) at commitSha or main

 doublure cleanup (CronJob) ── deletes previews/<slug>/ for stale previews ──► ArgoCD prunes
```

## Components

| Component | Where | Notes |
|---|---|---|
| `doublure serve` | this repo | chi HTTP server: `/healthz`, `/v1/deploy`, `/webhooks/github` |
| `doublure cleanup` | this repo | One-shot, CronJob. Stub today (see ROADMAP) |
| `doublure bootstrap-app` / `verify-app` | this repo | Create a GitHub App via the manifest flow / check credentials |
| `preview-env` chart | [akpe-charts](https://github.com/amine7536/akpe-charts) (moving into this repo) | Renders AppProject, Namespace, one ArgoCD Application per service |
| ApplicationSet | [demo-gitops](https://github.com/doublurehq/demo-gitops) `applicationset-previews.yaml` (moving into this repo as example) | Git directory generator on `previews/*` |
| Service chart | each service repo `k8s/` (uses the `app` chart from akpe-charts) | Image tag = `commitSha` or `$ARGOCD_APP_REVISION` |

## Gitops repo contract

```
services.yaml                    # serviceRepos: { <service>: <git url> }
previews/<slug>/values.yaml      # written by doublure
```

```yaml
# previews/feature-pricing/values.yaml
services:
  - name: backend-1
    commitSha: "<sha>"          # pinned: targetRevision + image tag
    metadata: { pr-author, pr-url, pr-number, created-at, updated-at, vcs.ref.name, cicd.pipeline.run.url }
  - name: backend-2             # no commitSha: tracks main
  - name: front
```

Pinned vs main-tracking trade-off: [TARGET_REVISION.md](TARGET_REVISION.md).

## HTTP API

```
GET  /healthz

POST /v1/deploy
  Authorization: Bearer $DOUBLURE_DEPLOY_TOKEN
  { "service", "head_ref", "commit_sha", "pr_author", "pr_url", "pr_number", "workflow_run_url" }
  → { "preview_slug", "gitops_commit_url", "warnings": [...] }

POST /webhooks/github
  X-Hub-Signature-256 verified (401 on mismatch)
  ping, installation, installation_repositories, pull_request → structured log only (today)
  parse failures / handler panics → logged, 200
```

Deploy flow: validate `service` against `services.yaml` → slugify `head_ref` → read or bootstrap `previews/<slug>/values.yaml` → write with SHA optimistic locking + 409 retry → PR comment fan-out (best-effort, failures returned as `warnings`).

Deploys are triggered only by CI calling `/v1/deploy`, never by `pull_request` webhooks: CI knows when the image exists (build once, deploy many).

## Configuration

```
GITHUB_APP_ID
GITHUB_PRIVATE_KEY_PATH | GITHUB_PRIVATE_KEY
GITHUB_WEBHOOK_SECRET
GITHUB_INSTALLATION_ID
GITOPS_REPO                 # owner/repo
DOUBLURE_DEPLOY_TOKEN
LISTEN_ADDR                 # default :8080
```

GitHub App permissions: Contents R/W, Pull requests W, Metadata R. Subscribed event: `pull_request` (installation events are delivered automatically).

## Demo setup

Demo services: [demo-backend-1](https://github.com/doublurehq/demo-backend-1), [demo-backend-2](https://github.com/doublurehq/demo-backend-2), [demo-front](https://github.com/doublurehq/demo-front). Gitops repo: [demo-gitops](https://github.com/doublurehq/demo-gitops). Local dev of doublure itself: [DEVELOPMENT.md](../DEVELOPMENT.md).
