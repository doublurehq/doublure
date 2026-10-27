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

 pull_request closed ──► doublure serve (async) ── unpins the PR's services, or deletes previews/<slug>/values.yaml ──► ArgoCD prunes
 doublure cleanup (CronJob, planned) ── deletes previews/<slug>/ for stale previews ──► ArgoCD prunes
```

## Components

| Component | Where | Notes |
|---|---|---|
| `doublure serve` | this repo | chi HTTP server: `/healthz`, `/v1/deploy`, `/webhooks/github` |
| `doublure cleanup` | this repo | One-shot, CronJob. Not implemented yet: exits with an error (see [ROADMAP](ROADMAP.md)) |
| `doublure bootstrap-app` / `verify-app` | this repo | Create a GitHub App via the manifest flow / check credentials |
| `preview-env` chart | this repo, [`charts/preview-env`](../charts/preview-env) | Renders AppProject, Namespace, one ArgoCD Application per service; Application annotations `preview.doublure.dev/*` carry the PR metadata |
| ApplicationSet | this repo, [`examples/argocd`](../examples/argocd) | Git directory generator on `previews/*` |
| Service chart | each service repo `k8s/` (uses the `app` chart, [`charts/app`](../charts/app)) | Image tag = `commitSha` or `$ARGOCD_APP_REVISION` |
| `doublure` chart | this repo, [`charts/doublure`](../charts/doublure) | Installs `doublure serve` (Deployment, Service, optional Ingress) and the cleanup CronJob (off by default) |

## Gitops repo contract

```
services.yaml                    # serviceRepos: { <service>: <git url> }, optional preview block
previews/<slug>/values.yaml      # written by doublure
```

```yaml
# services.yaml
serviceRepos:
  backend-1: https://github.com/acme/backend-1.git
  front: https://github.com/acme/front.git
preview:                        # optional: per-preview URLs, off without domain
  domain: 127.0.0.1.nip.io      # lowercase bare domain: no scheme, no path, no leading or trailing dot
  tls: false                    # optional, default true: https or http in the PR-comment links only
  expose:                       # services that get a host and a link
    front: {}                   # entry value: empty, {} or {path: ...}
    backend-1:
      path: /docs               # optional, default "/": PR-comment link only
```

`services.yaml` is a value file of the `preview-env` chart and is read by doublure on every deploy. A malformed `preview` block fails `/v1/deploy` with an error naming the offending key. An `expose` entry for a service missing from `serviceRepos` is ignored.

```yaml
# previews/feature-pricing/values.yaml
services:
  - name: backend-1
    commitSha: "<sha>"          # pinned: targetRevision + image tag
    metadata: { pr-author, pr-url, pr-number, created-at, updated-at, vcs.ref.name, cicd.pipeline.run.url }
  - name: backend-2             # no commitSha: tracks main
  - name: front
```

## Per-preview URLs

Each exposed service of a preview gets the host `<service>-<slug>.<domain>`. One wildcard DNS record (and, for TLS, one wildcard certificate) for `*.<domain>` covers every preview.

- The `preview-env` chart passes it to the service's Application as the Helm parameter `app.ingress.host`.
- doublure appends ` · [<host><path>](<scheme>://<host><path>)` to the service's line in the PR comment.

`tls` only picks the link scheme; ingress TLS, the ingress class and annotations stay in the service chart. Services not in `expose` (non-HTTP services, for example) get no host and no link.

Known limit: a DNS label is at most 63 characters. Until the slug length cap lands (see [ROADMAP](ROADMAP.md)), a long `<service>-<slug>` label makes that service's ingress invalid.

## Pinned and main-tracking services

A preview runs each service in one of two modes, set by `commitSha` in `previews/<slug>/values.yaml`:

- **Pinned** (`commitSha` set): the Application uses `targetRevision: <sha>` and `app.image.tag: <sha>`. It changes only when doublure writes a new SHA.
- **Main-tracking** (no `commitSha`): the Application uses `targetRevision: main` and `app.image.tag: $ARGOCD_APP_REVISION`, which Argo CD resolves to the commit it syncs. It follows new commits on `main`.

A main-tracking service can briefly sit in `ImagePullBackOff`: when Argo CD syncs a new `main` commit before that commit's CI has pushed the image, the image tag does not exist yet. This is accepted because it resolves without intervention:

- The kubelet keeps retrying the pull, with a back-off capped at 300 seconds, and the pod starts once the image is in the registry.
- The Applications sync with `selfHeal: true`, and Argo CD polls Git every 3 minutes by default.
- Only services that are not under review in the preview are affected.

Alternatives considered, and why they were not chosen:

- **doublure re-pins main-tracking services** to the new `main` SHA in every active preview once its image exists: removes the race, but every `main` build has to rewrite every preview, with concurrent writes to the same files.
- **Argo CD webhook from CI** after the image push, instead of polling: needs CI-to-cluster network access and an exposed Argo CD API, and does not help while Argo CD also polls.
- **Argo CD Image Updater**: another controller with registry credentials and write-back config, built for tracking the latest tag rather than previews.
- **Init container that waits for the image**: adds registry access and a wait to every service chart, and still delays startup.

References: [Helm build environment in Argo CD](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/), [Argo CD tracking strategies](https://argo-cd.readthedocs.io/en/latest/user-guide/tracking_strategies/).

## Service repo contract

The `preview-env` chart ([`application.yaml`](../charts/preview-env/templates/application.yaml)) renders one Argo CD Application per service from the service's repo URL in `services.yaml`, and expects each service repo to provide:

- A Helm chart at `k8s/`.
- A `main` branch: main-tracking services use `targetRevision: main`.
- A Helm parameter `app.image.tag`: the service chart declares the [`app`](../charts/app) chart as a dependency aliased `app`.
- Images tagged with the full git commit SHA: pinned services use the `commitSha` sent to `/v1/deploy`, main-tracking services use `$ARGOCD_APP_REVISION`.
- For services in `preview.expose`: ingress enabled and a Helm parameter `app.ingress.host` setting the ingress host. Otherwise the PR-comment link is dead.

These values are fixed in the chart today; making them configurable is on the [roadmap](ROADMAP.md).

The ApplicationSet example sources `preview-env` from an OCI registry (`oci://` with `path: "."`), which requires Argo CD 3.1 or later.

## HTTP API

```
GET  /healthz

POST /v1/deploy
  Authorization: Bearer $DOUBLURE_DEPLOY_TOKEN
  { "service", "head_ref", "commit_sha", "pr_author", "pr_url", "pr_number", "workflow_run_url" }
  → { "preview_slug", "gitops_commit_url", "warnings": [...] }

POST /webhooks/github
  X-Hub-Signature-256 verified (401 on mismatch)
  installation ≠ GITHUB_INSTALLATION_ID (ping exempt) → 403
  pull_request closed → async teardown; other events → structured log only
  parse failures / handler panics → logged, 200
```

Deploy flow: validate `service` against `services.yaml` → slugify `head_ref` → read or bootstrap `previews/<slug>/values.yaml` → write with SHA optimistic locking + 409 retry → PR comment fan-out (best-effort, failures returned as `warnings`).

Deploys are triggered only by CI calling `/v1/deploy`, never by `pull_request` webhooks: CI knows when the image exists (build once, deploy many).

Teardown flow: a `pull_request` `closed` delivery (merged or not) is answered 200 at once, and the teardown runs in the background (20s budget; shutdown waits for it). doublure slugifies the PR's head branch, reads `previews/<slug>/values.yaml` and unpins every service whose `pr-url` points to the closed PR: the entry goes back to a bare main-tracking `{name}`. If other pins remain, the file is updated and the remaining PRs' comments are refreshed; otherwise the file is deleted. Both writes use SHA optimistic locking + 409 retry, deciding update vs delete again on every retry. The closed PR's comment is edited (or created) to say what was removed.

Deleting the file drops `previews/<slug>/` from the git directory generator, and the ApplicationSet deletes the preview's Application and its resources. This requires the ApplicationSet controller policy `sync` (the default) or `create-delete`; with `create-only` or `create-update` a deleted preview keeps running. Previews whose branch was renamed after the deploy, and closes missed while doublure was down, are left for the cleanup sweeper.

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
