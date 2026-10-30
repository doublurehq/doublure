# Architecture

Self-hosted, single-tenant. Everything runs in the user's cluster; doublure talks to GitHub through the user's own GitHub App and never touches the cluster directly: ArgoCD does the deploys.

```
 service repo CI ── builds + pushes the image (no deploy call)

 manual trigger ──► POST /v1/deploy ──────────────────────────────────────────────────────────┐
 GitHub ── pull_request (PR label or push) ──► POST /webhooks/github ── async reconcile ──► doublure serve ──► gitops repo
   ▲                                                                                          │ (GitHub App)   previews/<slug>/values.yaml
   └──────────────── PR comment (fan-out) ◄───────────────────────────────────────────────────┘                  ▼
                                     ArgoCD ApplicationSet (git dir generator: previews/*)
                                       └─ preview-env chart → AppProject + Namespace + one Application per service
                                            └─ service chart (path per serviceCharts, default k8s/) at commitSha or its branch (default main)

 label removed or pull_request closed ──► doublure serve (async) ── unpins the PR's services, or deletes previews/<slug>/values.yaml ──► ArgoCD prunes
 doublure cleanup (CronJob) ── deletes stale previews/<slug>/, unpins closed PRs, and open PRs whose label moved, conflicts or is gone ──► ArgoCD prunes
```

## Components

| Component | Where | Notes |
|---|---|---|
| `doublure serve` | this repo | chi HTTP server: `/healthz`, `/v1/deploy`, `/webhooks/github`, `/webhooks/argocd` (optional) |
| `doublure cleanup` | this repo | One-shot, CronJob (hourly). Per preview, the first matching rule: no pinned service → delete; `CLEANUP_TTL` set and no deploy within it → delete; otherwise unpin closed PRs, and open PRs whose label moved, conflicts or is gone (see [Cleanup sweep](#cleanup-sweep)) |
| `doublure bootstrap-app` / `verify-app` | this repo | Create a GitHub App via the manifest flow / check credentials |
| `preview-env` chart | this repo, [`charts/preview-env`](../charts/preview-env) | Renders AppProject, Namespace, one ArgoCD Application per service; Application annotations `preview.doublure.dev/*` carry the PR metadata |
| ApplicationSet | this repo, [`examples/argocd`](../examples/argocd) | Git directory generator on `previews/*` |
| Service chart | each service repo, at `k8s/` unless `serviceCharts` sets `path` (uses the `app` chart, [`charts/app`](../charts/app)) | Image tag = `commitSha` or `$ARGOCD_APP_REVISION` |
| `doublure` chart | this repo, [`charts/doublure`](../charts/doublure) | Installs `doublure serve` (Deployment, Service, optional Ingress) and the cleanup CronJob (off by default) |

## Gitops repo contract

```
services.yaml                    # serviceRepos: { <service>: <git url> }, optional preview block, optional serviceCharts block
previews/<slug>/values.yaml      # written by doublure
```

```yaml
# services.yaml
serviceRepos:
  backend-1: https://github.com/acme/backend-1.git
  front: https://github.com/acme/front.git
serviceCharts:                  # optional: per-service chart settings, every key optional
  backend-1:
    branch: master              # default "main": targetRevision of unpinned services, and the PR-comment badge
    path: deploy/helm           # default "k8s/": chart path in the service repo
    imageTagParam: image.tag    # default "app.image.tag": Helm parameter set to the image tag
    hostParam: ingress.hostname # default "app.ingress.host": Helm parameter set to the preview host
    valueFiles:                 # default none: extra values files, applied in order
      - values-preview.yaml
preview:                        # optional: preview label and per-preview URLs (URLs off without domain)
  label: preview                # optional, default "preview": the PR label that creates the preview
  domain: 127.0.0.1.nip.io      # lowercase bare domain: no scheme, no path, no leading or trailing dot
  tls: false                    # optional, default true: https or http in the PR-comment links only
  expose:                       # services that get a host and a link
    front: {}                   # entry value: empty, {} or {path: ...}
    backend-1:
      path: /docs               # optional, default "/": PR-comment link only
```

`services.yaml` is a value file of the `preview-env` chart and is read by doublure on every deploy. A malformed `preview` or `serviceCharts` block fails `/v1/deploy` and makes every reconcile log `preview reconcile failed`, with an error naming the offending key. An `expose` entry for a service missing from `serviceRepos` is ignored.

- `preview.label` must be a non-empty string; it is matched case-insensitively, like GitHub label names. `<label>:<name>` labels name the preview (see the label flow below).
- Every `serviceRepos` value must be a string. A PR's repo maps to the services whose URL is `https://github.com/<owner>/<repo>`, compared case-insensitively, with or without a trailing `/` or `.git`. Other URL forms (SSH, GitHub Enterprise) never match, so the label flow never deploys those services.
- Every `serviceCharts` key must be a service of `serviceRepos`, and unknown fields are rejected. A null entry, or a null field, uses the defaults. `branch`, `path`, `imageTagParam` and `hostParam` must be non-empty strings, and `valueFiles` a list of non-empty strings; unquoted YAML 1.1 booleans (`yes`, `no`, `on`, `off`, `y`, `n`) must be quoted. Since `preview-env` 0.4.1 the chart also refuses to render a non-string value (Argo CD shows a sync error), because doublure's check only blocks its own writes while Argo CD re-renders existing previews from the same `services.yaml`. doublure only uses `branch` (for the PR-comment badge); the chart reads every field. The block needs `preview-env` 0.4.0 or later and doublure 0.11.0 or later: an older chart ignores it and applies the defaults, and an older doublure neither validates it nor shows the branch.

```yaml
# previews/feature-pricing/values.yaml
services:
  - name: backend-1
    commitSha: "<sha>"          # pinned: targetRevision + image tag
    host: backend-1-feature-pricing.127.0.0.1.nip.io   # exposed services only: ingress host written by doublure
    metadata: { pr-author, pr-url, pr-number, created-at, updated-at, vcs.ref.name, cicd.pipeline.run.url }
                                # cicd.pipeline.run.url is "" for label deploys
  - name: backend-2             # no commitSha: tracks its branch (default main)
  - name: front
```

## Per-preview URLs

Each exposed service of a preview gets the host `<service>-<slug>.<domain>` (the `<service>-<slug>` label capped at 63 characters, see below). One wildcard DNS record (and, for TLS, one wildcard certificate) for `*.<domain>` covers every preview.

- doublure writes the host into each exposed entry's `host` in `values.yaml`, and `preview-env` passes it to the service's Application as the Helm parameter `app.ingress.host` (or the service's `hostParam`).
- doublure appends ` · [<host><path>](<scheme>://<host><path>)` to the service's line in the PR comment.

`tls` only picks the link scheme; ingress TLS, the ingress class and annotations stay in the service chart. Services not in `expose` (non-HTTP services, for example) get no host and no link.

Length caps: a slug longer than 53 characters (Helm's release-name limit, which Argo CD hits through the ApplicationSet's `releaseName`) and a `<service>-<slug>` host label longer than 63 characters (the DNS label limit) are cut, and a hash of the full value (6 hex characters) is appended. For example, the branch `feature/very-long-branch-name-that-keeps-going-and-going-past-the-helm-limit` gets the slug `feature-very-long-branch-name-that-keeps-going-5bec34`, and the service `front-office-web` in it gets the host `front-office-web-feature-very-long-branch-name-that-keep-395ff3.<domain>`.

Upgrade order: upgrade doublure (0.10.0 or later) first, then `preview-env` (0.3.0 or later). `preview-env` 0.3.0 reads only `host`, so an entry without it gets no ingress host. Preview files written by an older doublure carry no `host` until their next deploy write.

## Pinned and branch-tracking services

A preview runs each service in one of two modes, set by `commitSha` in `previews/<slug>/values.yaml`:

- **Pinned** (`commitSha` set): the Application uses `targetRevision: <sha>` and sets the image tag parameter (default `app.image.tag`) to `<sha>`. It changes only when doublure writes a new SHA.
- **Branch-tracking** (no `commitSha`): the Application uses `targetRevision: <branch>` (default `main`) and sets the image tag parameter (default `app.image.tag`) to `$ARGOCD_APP_REVISION`, which Argo CD resolves to the commit it syncs. It follows new commits on that branch.

A branch-tracking service can briefly sit in `ImagePullBackOff`: when Argo CD syncs a new commit on that branch before that commit's CI has pushed the image, the image tag does not exist yet. This is accepted because it resolves without intervention:

- The kubelet keeps retrying the pull, with a back-off capped at 300 seconds, and the pod starts once the image is in the registry.
- The Applications sync with `selfHeal: true`, and Argo CD polls Git every 3 minutes by default.
- Only services that are not under review in the preview are affected.

Alternatives considered, and why they were not chosen:

- **doublure re-pins branch-tracking services** to the branch's new SHA in every active preview once its image exists: removes the race, but every build of that branch has to rewrite every preview, with concurrent writes to the same files.
- **Argo CD webhook from CI** after the image push, instead of polling: needs CI-to-cluster network access and an exposed Argo CD API, and does not help while Argo CD also polls.
- **Argo CD Image Updater**: another controller with registry credentials and write-back config, built for tracking the latest tag rather than previews.
- **Init container that waits for the image**: adds registry access and a wait to every service chart, and still delays startup.

References: [Helm build environment in Argo CD](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/), [Argo CD tracking strategies](https://argo-cd.readthedocs.io/en/latest/user-guide/tracking_strategies/).

## Service repo contract

The `preview-env` chart ([`application.yaml`](../charts/preview-env/templates/application.yaml)) renders one Argo CD Application per service from the service's repo URL in `services.yaml`, and expects each service repo to provide:

- A Helm chart at `k8s/` by default, or at the service's `serviceCharts` `path`.
- A `main` branch by default, or the service's `serviceCharts` `branch`: branch-tracking services use it as `targetRevision`.
- A Helm parameter `app.image.tag` by default (the service chart declares the [`app`](../charts/app) chart as a dependency aliased `app`), or the service's `serviceCharts` `imageTagParam`.
- Images tagged with the full git commit SHA: pinned services use the PR head SHA (label flow) or the `commit_sha` sent to `/v1/deploy`, branch-tracking services use `$ARGOCD_APP_REVISION`.
- For services in `preview.expose`: ingress enabled and a Helm parameter setting the ingress host, `app.ingress.host` by default or the service's `serviceCharts` `hostParam`. Otherwise the PR-comment link is dead.
- A service whose image is not built per commit (for example a database) may ignore the image tag parameter.
- Extra values files, from the service's `serviceCharts` `valueFiles`, applied in order. Argo CD joins a relative path to the chart path and a path starting with `/` to the repo root; a missing file fails the sync.

`hostParam` is passed as a Helm `--set`, key verbatim. A list-indexed name such as `ingress.hosts[0].host` replaces the chart's default list, so the full list (with its `paths`) and `ingress.enabled: true` must come from a file in `valueFiles`; the parameter then only sets that element's field.

Override them per service in `serviceCharts` (`preview-env` 0.4.0 or later); image tags must still be the full commit SHA.

The ApplicationSet example sources `preview-env` from an OCI registry (`oci://` with `path: "."`), which requires Argo CD 3.1 or later.

## Preview data

doublure never reads, writes or drops data stores.

- A preview's identity is its slug: both the Helm release name of `preview-env` and the preview namespace.
- Each service's release name is the service name, so per-preview data is keyed by namespace.
- Data that lives in the preview namespace is pruned with it on teardown, sweep or expiry.
- Data outside the namespace is owned, and dropped, by whatever provisions it.
- The [`app`](../charts/app) chart only offers `envFrom` and `initContainers` to wire a data provider.

## HTTP API

```
GET  /healthz

GET  /metrics               # on METRICS_ADDR only (default :9090), not on LISTEN_ADDR

POST /v1/deploy
  Authorization: Bearer $DOUBLURE_DEPLOY_TOKEN
  { "service", "head_ref", "commit_sha", "pr_author", "pr_url", "pr_number", "workflow_run_url" }
  → { "preview_slug", "gitops_commit_url", "warnings": [...] }

POST /webhooks/github
  X-Hub-Signature-256 verified (401 on mismatch)
  installation ≠ GITHUB_INSTALLATION_ID (ping exempt) → 403
  pull_request opened/reopened/synchronize/labeled/unlabeled/closed → async reconcile; other actions → log only
  other events → structured log only
  parse failures / handler panics → logged, 200

POST /webhooks/argocd       # only when ARGOCD_WEBHOOK_TOKEN is set (404 otherwise)
  Authorization: Bearer $ARGOCD_WEBHOOK_TOKEN (401 on mismatch)
  { "app", "service", "preview", "revision", "updatedAt" }   # at most 64 KiB; invalid → 400
  → 204, recorded or ignored (see Spin-up endpoint)
```

Manual deploy flow (`/v1/deploy`): validate `service` against `services.yaml` → slugify `head_ref` → read or bootstrap `previews/<slug>/values.yaml` → write with SHA optimistic locking + 409 retry → PR comment fan-out (best-effort, failures returned as `warnings`).

Label flow: CI only builds and pushes images; the `preview` label (`preview.label`) on a PR decides whether it has a preview, and in which one. doublure answers `opened`, `reopened`, `synchronize`, `labeled`, `unlabeled` and `closed` deliveries 200 at once and reconciles the PR in the background:

1. A `labeled` or `unlabeled` delivery for another label, or an `opened`, `reopened` or `synchronize` delivery whose payload lacks a preview label, is skipped without calling GitHub. The preview labels are the plain label and the named labels below with a non-empty slug.
2. A repo not in the catalog is skipped with a log. Neither step applies to `closed`.
3. Otherwise doublure re-reads the PR from GitHub and acts on its current state, labels and head SHA, never the payload's, so late or out-of-order deliveries, `closed` included, converge on the current truth: a `closed` delivery running after the PR was reopened and labeled deploys it. Fork PRs (head repo different from the base repo) are skipped: CI can't push a fork's image; a closed fork PR is still torn down.
4. The PR's labels resolve its target preview (see Target preview below).
5. The PR is torn down from every other preview it may still be pinned in (the cleanup set below), before anything is deployed.
6. An open PR with a target deploys every catalog service of its repo, in catalog order, at the PR's head SHA, to the target slug, through the manual flow's write and comment fan-out, unless another PR holds one of its services there (see One PR per service below). A failed service is logged and the next one still deploys. The deploy fan-out is the last write to the PR's comment, so a PR that moved shows its new preview.
7. An open PR with conflicting named labels gets no preview. Its comment is replaced last by:

   ```
   ## ⚠️ Preview: conflicting labels

   Keep one `preview:` label. This PR has `preview:a`, `preview:b`.
   ```

Target preview: a label is **named** when it starts with `<label>:` (case-insensitively, for example `preview:pricing` or `Preview:Pricing`). Its name is the rest, trimmed of spaces, and its slug is the slugified name. A named label whose slug is empty (`preview:`, `preview:--`) is ignored and logged (`preview label ignored`). The **plain** label is `<label>` itself. `preview-x` and `previewx:y` are neither.

- Exactly one distinct named slug: the target is that slug. A named label wins over the plain label. Two named labels with the same slug count as one.
- Two or more distinct named slugs: a conflict, with no target.
- No valid named label, and the plain label: the target is the slugified head branch.
- Otherwise: no target.

Several PRs, from different repos and branches, labeled `preview:pricing` share the preview `pricing`: each pins its own services, and the rest run at their branch (default `main`). A named preview and a branch preview with the same slug are the same preview: `preview:main` joins the preview of a branch called `main`. doublure never creates labels: labels are per repo, so `preview:<name>` must be created in each repo.

The **cleanup set** of a reconcile is, deduplicated, without empty slugs, and without the target when the PR is deployed there: the slugified head branch, every named slug, and, on `unlabeled`, the removed label's slug (the head branch's for the plain label). Each is torn down with one reason, the first that applies:

1. `PR closed`, when the PR is closed;
2. ``label `<removed label>` removed``, on `unlabeled`;
3. ``conflicting `<label>:` labels``, on a conflict;
4. ``moved to preview `<target>` ``, when the PR is deployed elsewhere;
5. ``label `<label>` removed`` otherwise.

A slug where the PR has no pin costs one read and is otherwise a no-op, with no comment.

A renamed or deleted label triggers nothing until the PR's next event or the next sweep, which unpins without redeploying. In that missed-event case the PR's comment can stay stale until its next event.

One PR per service: a preview holds one PR per service. Before deploying, a reconcile reads the target's `values.yaml` (a missing file means no holder). An entry for one of the PR's services is held when it is pinned (`commitSha` set), has metadata, and its `pr-url` names another PR. Any held entry refuses the whole PR, for every target, branch or named, since two PRs can also share a head branch. The first held service is reported in the PR's comment:

```
## ⚠️ Preview: `<slug>`

`<service>` is already pinned in this preview by <holder PR URL>. Once that pull request leaves this preview, push to this pull request or re-add its label. If that pull request is closed, or now carries a different `<label>:` label, it is released within the next cleanup run.
```

and the reconcile logs `preview reconcile skipped` with reason `service held by another pr` and a `holder` attribute. A read error other than 404 logs `preview reconcile failed` and deploys nothing. The holder PR is never read: a holder left by a missed event is released by the sweeper. A refused PR is not retried when the holder leaves, and since the cleanup runs first, it has already left its previous preview, as its labels ask.

The check assumes `replicaCount: 1` (`charts/doublure/values.yaml`): reconciles are serialized within the replica, so no other reconcile writes between the read and the write. `/v1/deploy` runs outside that queue and skips the check: it stays last-writer-wins and can overwrite a holder's pin.

doublure does not check the registry: an image not pushed yet leaves the pod in `ImagePullBackOff` until CI pushes it (kubelet back-off capped at 300s).

Background reconciles run one at a time, in no guaranteed order; a reconcile reads the PR only once it runs. A reconcile has a 20s budget, counted from when it starts running and shared by its cleanup teardowns, the holder check, then its services; a service not reached in time is logged as failed. Shutdown waits up to 25s for queued and running jobs; jobs still queued then are dropped, and the cleanup sweeper catches missed teardowns of closed PRs.

A labeled PR with no push for longer than `CLEANUP_TTL` is expired by the sweeper and not recreated while the label stays: push, or remove and re-add the label, to recreate it. A missed `unlabeled` of a named label, or a label that moved, is unpinned by the sweeper; a missed `unlabeled` of the plain label at the branch slug still waits for close or TTL.

Teardown flow: a reconcile tears the PR down from each slug of its cleanup set: the PR is closed (merged or not), lost its label (for example after `unlabeled`), has conflicting labels, or moved to another preview. A `closed` delivery whose reconcile can't read the catalog or the PR falls back to tearing down, as closed, the payload's head branch and the payload's named labels, logged as a warning, so a close still tears down when GitHub reads fail. For each slug (the slugified head branch or named label), doublure reads `previews/<slug>/values.yaml` and unpins every service whose `pr-url` points to the PR: the entry goes back to a bare branch-tracking `{name}`. If other pins remain, the file is updated and the remaining PRs' comments are refreshed; otherwise the file is deleted. Both writes use SHA optimistic locking + 409 retry, deciding update vs delete again on every retry. The PR's comment is edited (or created) to say what was removed and why: `PR closed`, ``label `<label>` removed``, ``label `<label>:<name>` removed``, ``conflicting `<label>:` labels`` or ``moved to preview `<slug>` ``.

Deleting the file drops `previews/<slug>/` from the git directory generator, and the ApplicationSet deletes the preview's Application and its resources. This requires the ApplicationSet controller policy `sync` (the default) or `create-delete`; with `create-only` or `create-update` a deleted preview keeps running. Closes missed while doublure was down, and pins written by a deploy after the PR closed, are caught by the cleanup sweeper. A labeled PR whose branch was renamed after the deploy is unpinned from its old slug by the sweeper, without a comment; push to redeploy it at the new slug.

## Cleanup sweep

The sweep runs in the server every `CLEANUP_INTERVAL` when it is set, starting one interval after startup; runs never overlap and shutdown cancels a run in progress. Otherwise `doublure cleanup` runs it once per CronJob schedule (hourly by default). Use one or the other: both run the same idempotent sweep, so running both only wastes work. Only the in-server sweep exports the sweep metrics. The sweep lists `previews/*` and applies to each preview the first matching rule:

1. **No pinned service**: the file is deleted (`chore(preview): delete <slug> preview`). No comment.
2. **Expired** (only when `CLEANUP_TTL` is set): the newest `updated-at` of the pinned services is older than `CLEANUP_TTL`. The file is deleted (`chore(preview): expire <slug> preview`) and its open PRs get a comment saying the preview expired. A pinned service without a parseable `updated-at` never counts, so such a preview never expires.
3. **Closed or moved PRs**: every distinct PR pinning a service is looked up on GitHub. A PR whose state can't be read (404, 403, any error) keeps its pins and is logged as a warning.
   - A closed (or merged) PR goes through the same teardown as a `pull_request` `closed` delivery: unpin, update or delete, comments.
   - An open PR's labels are resolved to its target preview (see the label flow). It is unpinned from this preview, in this order, when its labels conflict (``conflicting `<label>:` labels``), or target another preview (``moved to preview `<target>` ``): both without touching its comment, which shows where it lives now. When it has no target and this preview is not its branch slug, it goes through the teardown, and its comment says ``label `<label>` removed``. Any other open PR is left alone, including an unlabeled PR pinned at its branch slug.

   This catches missed `unlabeled` deliveries, conflicts, and renamed or deleted labels. The catalog is read once per sweep for the label; when it can't be read, `cleanup catalog unavailable` is logged as a warning, it is not counted as a failure, and only closed PRs are torn down.

   This changes manual deploys: a `/v1/deploy` pin now survives the sweep only at the PR's target preview (its single `<label>:<name>` slug, or its branch slug when it has only the plain label), or at its branch slug when the PR has no target. Anywhere else it is unpinned (``moved to preview `<target>` `` or ``label `<label>` removed``); a PR carrying `<label>:a` loses a pin at its branch slug. A PR with conflicting named labels is unpinned from every preview (``conflicting `<label>:` labels``). A webhook reconcile of the PR also unpins it from its branch slug and its named-label slugs other than its target (`cleanupSlugs`). Such pins used to be left alone.

Rules 1 and 2 re-check their condition on the freshly read file, and again after every 409, so a deploy landing during the sweep cancels the delete. A failing preview read or rule 1–2 delete is logged and the sweep goes on; the command then exits 1 and the Job fails without retrying (`backoffLimit: 0`). A failing closed-PR teardown is only logged. The next scheduled run retries both. Comment fan-outs (deploy, teardown, expiry) skip closed PRs, so a closed PR keeps its teardown comment.

Known limit: the GitHub contents API lists at most 1,000 entries per directory, so the sweep sees at most 1,000 previews.

doublure never touches data stores; see [Preview data](#preview-data).

## Configuration

```
GITHUB_APP_ID
GITHUB_PRIVATE_KEY_PATH | GITHUB_PRIVATE_KEY
GITHUB_WEBHOOK_SECRET
GITHUB_INSTALLATION_ID
GITOPS_REPO                 # owner/repo
DOUBLURE_DEPLOY_TOKEN
LISTEN_ADDR                 # default :8080
METRICS_ADDR                # default :9090 when unset; set to "" to disable /metrics
ARGOCD_WEBHOOK_TOKEN        # optional; enables /webhooks/argocd
CLEANUP_INTERVAL            # serve, optional: Go duration of at least 1m (e.g. 1h); unset disables the in-server sweep
CLEANUP_TTL                 # cleanup, and serve when CLEANUP_INTERVAL is set, optional: Go duration (e.g. 168h); unset disables expiry
```

GitHub App permissions: Contents R/W, Pull requests W, Metadata R. Subscribed event: `pull_request` (installation events are delivered automatically).

## Metrics

`GET /metrics` serves Prometheus metrics on `METRICS_ADDR`, a listener separate from `LISTEN_ADDR`, so the Ingress never exposes it. The chart adds a `metrics` port to the Service and, with `metrics.serviceMonitor.enabled`, a ServiceMonitor.

### Metric list

Every label has a closed set of values. Go runtime (`go_*`) and process (`process_*`) metrics are exported too.

| Metric | Type | Labels | |
|---|---|---|---|
| `doublure_webhook_deliveries_total` | counter | `event`, `status` | Webhook deliveries. `event` is `ping`, `installation`, `installation_repositories`, `pull_request` or `other` once the signature is verified, `unverified` before. `status` is the HTTP status: `200`, `401` or `403`. |
| `doublure_reconciles_total` | counter | `outcome` | One per pull request reconcile: `failed` (catalog, PR or preview read error, or every service write failed), else `skipped`, else `deployed` (a service write succeeded), else `torn_down`. |
| `doublure_reconcile_skips_total` | counter | `reason` | Skipped reconciles by reason: `other label`, `not labeled`, `repo not in catalog`, `fork pr`, `service held by another pr`, `conflicting preview labels`. |
| `doublure_teardowns_total` | counter | `trigger`, `result` | Teardowns of a PR from a preview. `trigger` is `webhook` or `sweep`; `result` is `deleted`, `unpinned`, `noop` (nothing pinned) or `error`. |
| `doublure_gitops_operation_duration_seconds` | histogram | `op`, `result` | Duration of gitops `write` and `teardown` operations, 409 retries included; `result` is `ok` or `error`. |
| `doublure_sweep_runs_total` | counter | `result` | In-server sweep runs, `ok` or `error`. A run cancelled by shutdown is not counted. |
| `doublure_sweep_last_success_timestamp_seconds` | gauge | | Unix time of the last successful in-server sweep. |
| `doublure_sweep_previews_deleted_total` | counter | `rule` | Previews deleted by the sweep: `no_pin` (rule 1) or `ttl` (rule 2). |
| `doublure_previews` | gauge | | Previews listed by the last in-server sweep, minus those it deleted. |
| `doublure_preview_spinup_seconds` | histogram | `catalog_service` | Spin-up time of a pinned service, from doublure's gitops write to its Application synced and Healthy (see [Spin-up endpoint](#spin-up-endpoint)). Buckets: 15, 30, 60, 120, 180, 300, 600, 900, 1800 s. `catalog_service` is a catalog service; it is not named `service` because that collides with the `service` target label Prometheus adds when scraping through a ServiceMonitor. |
| `doublure_argocd_events_total` | counter | `result` | Events accepted by `/webhooks/argocd`: `recorded` (a spin-up was observed) or `ignored`. 400 and 401 answers are not counted. |

`doublure_previews` and `doublure_sweep_last_success_timestamp_seconds` are always exported and read 0 until the first in-server sweep: with the CronJob alone they stay at 0.

### Spin-up endpoint

doublure never reads the cluster, so Argo CD's notifications controller tells it when a preview service is up: it POSTs to `/webhooks/argocd` when a service's Application is synced and Healthy at a new revision, once per revision. Configuration: [`examples/argocd/notifications-values.yaml`](../examples/argocd/notifications-values.yaml).

- The route exists only when `ARGOCD_WEBHOOK_TOKEN` is set (chart: optional `argocd-webhook-token` key of `existingSecret`). Requests need `Authorization: Bearer $ARGOCD_WEBHOOK_TOKEN`, compared in constant time; a missing or wrong token gets 401. The public Ingress exposes the route too.
- The JSON body is at most 64 KiB; invalid JSON or a larger body gets 400.
- Spin-up is `now − updatedAt`, where `updatedAt` is the Application's `preview.doublure.dev/updated-at` annotation: the time doublure wrote the service to the gitops repo. It runs from that write to the Application synced and Healthy at that revision, so it includes the ApplicationSet poll and any wait for CI to push the image.
- An event is recorded when `service` is set and `updatedAt` is RFC 3339 and 0 to 24h old; any other valid event is ignored. Unpinned services carry no `updated-at`, so their events are ignored. Events are not checked against the catalog: the token is the gate.
- Late samples: the notifications controller also sends the trigger for already-Healthy pinned Applications the first time the configuration is applied, and re-sends it after a failed delivery (doublure restarting, a 5xx). Each is recorded as the time since `updated-at`, up to the 24h bound, so it usually lands in `+Inf`. Read samples above the top bucket (1800 s) as backfill or retries, not real spin-ups.
- Every valid, authorized event gets 204, recorded or not, and logs `argocd event` with `app`, `service`, `preview`, `revision`, `result` and, when recorded, `spinup_seconds`.

### Preview deployments on GitHub

Argo CD, not doublure, signals on GitHub that a PR's code is running in its preview: health is Argo CD's call. Its notifications controller posts GitHub Deployments through its own GitHub App (Deployments R/W, Metadata R); doublure makes no Deployments calls. Configuration: [`examples/argocd/github-deployments-values.yaml`](../examples/argocd/github-deployments-values.yaml), applied after `notifications-values.yaml` (setup: INSTALL [Preview deployments on GitHub](INSTALL.md#preview-deployments-on-github-optional)).

- Both triggers apply only to pinned preview service Applications: those whose `preview.doublure.dev/vcs.ref.revision` annotation is not empty.
- `on-preview-deployed` fires when the Application is synced and Healthy, once per `syncResult.revision` (the PR head SHA, since a pinned service's `targetRevision` is its commit SHA). It posts status `success` on the service repo (`spec.source.repoURL`) at that revision, with `environment_url` built from the `preview.doublure.dev/host` annotation, which `preview-env` 0.5.0 sets from the service's `host`. The deployment is created with `auto_merge: false` and `required_contexts: []`, so an older ref or a red commit still gets one.
- `on-preview-deleted` fires when the Application has a `deletionTimestamp` and posts `inactive` on the deployment for its current `syncResult.revision`.
- The environment is `preview-<slug>-<service>`, one per preview service. A new `success` on it marks the prior `success` deployments `inactive` (GitHub's `auto_inactive`).

Limitations:

1. `inactive` on delete is best effort: the controller only sees the deletion if it processes the update before the Application's finalizer completes. It looks up the deployment by the current revision, so if that SHA never went Healthy it creates an extra `inactive` deployment for it, and the earlier SHA's `success` stays.
2. A PR that leaves a surviving preview keeps its last `success`, because its Application is updated, not deleted.
3. Failed notifications are not retried.
4. Applying the config posts `success` for every already-Healthy pinned Application, as `oncePer` has no prior state.
5. Environments accumulate on the service repos and can be deleted by hand.
6. The URL is the host root; the expose path is not applied.
7. One App installation covers all service repos (`service.github` takes one `installationID`).
8. For a pinned service with no host (for example `postgres`, not in `expose`), `environment_url` is sent as `""`. Whether GitHub accepts that is not verified; the notifications controller log shows the result.

### Dashboard

[`charts/doublure/dashboards/doublure.json`](../charts/doublure/dashboards/doublure.json) is a Grafana dashboard (uid `doublure`) over these metrics: live previews, deploys and teardowns per hour, spin-up p50/p90 and p90 by service, zombies swept, reconcile skips by reason, webhook deliveries by status, gitops operation p90 and the last successful sweep. It picks its Prometheus data source through a `datasource` variable. With `dashboard.enabled`, the chart renders it as a ConfigMap labelled `dashboard.labels` (default `grafana_dashboard: "1"`), which the Grafana sidecar of kube-prometheus-stack loads; the JSON also imports on its own. Live previews and last successful sweep need `cleanup.interval`, and the spin-up panels need the Argo CD notifications callback.

## Demo setup

Demo services: [demo-backend-1](https://github.com/doublurehq/demo-backend-1), [demo-backend-2](https://github.com/doublurehq/demo-backend-2), [demo-front](https://github.com/doublurehq/demo-front). Gitops repo: [demo-gitops](https://github.com/doublurehq/demo-gitops). Local dev of doublure itself: [DEVELOPMENT.md](../DEVELOPMENT.md).
