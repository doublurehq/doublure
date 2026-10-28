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
                                            └─ service chart (k8s/ in each service repo) at commitSha or main

 label removed or pull_request closed ──► doublure serve (async) ── unpins the PR's services, or deletes previews/<slug>/values.yaml ──► ArgoCD prunes
 doublure cleanup (CronJob) ── deletes stale previews/<slug>/, unpins closed PRs, and open PRs whose label moved, conflicts or is gone ──► ArgoCD prunes
```

## Components

| Component | Where | Notes |
|---|---|---|
| `doublure serve` | this repo | chi HTTP server: `/healthz`, `/v1/deploy`, `/webhooks/github` |
| `doublure cleanup` | this repo | One-shot, CronJob (hourly). Per preview, the first matching rule: no pinned service → delete; `CLEANUP_TTL` set and no deploy within it → delete; otherwise unpin closed PRs, and open PRs whose label moved, conflicts or is gone (see [Cleanup sweep](#cleanup-sweep)) |
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
preview:                        # optional: preview label and per-preview URLs (URLs off without domain)
  label: preview                # optional, default "preview": the PR label that creates the preview
  domain: 127.0.0.1.nip.io      # lowercase bare domain: no scheme, no path, no leading or trailing dot
  tls: false                    # optional, default true: https or http in the PR-comment links only
  expose:                       # services that get a host and a link
    front: {}                   # entry value: empty, {} or {path: ...}
    backend-1:
      path: /docs               # optional, default "/": PR-comment link only
```

`services.yaml` is a value file of the `preview-env` chart and is read by doublure on every deploy. A malformed `preview` block fails `/v1/deploy` and makes every reconcile log `preview reconcile failed`, with an error naming the offending key. An `expose` entry for a service missing from `serviceRepos` is ignored.

- `preview.label` must be a non-empty string; it is matched case-insensitively, like GitHub label names. `<label>:<name>` labels name the preview (see the label flow below).
- Every `serviceRepos` value must be a string. A PR's repo maps to the services whose URL is `https://github.com/<owner>/<repo>`, compared case-insensitively, with or without a trailing `/` or `.git`. Other URL forms (SSH, GitHub Enterprise) never match, so the label flow never deploys those services.

```yaml
# previews/feature-pricing/values.yaml
services:
  - name: backend-1
    commitSha: "<sha>"          # pinned: targetRevision + image tag
    host: backend-1-feature-pricing.127.0.0.1.nip.io   # exposed services only: ingress host written by doublure
    metadata: { pr-author, pr-url, pr-number, created-at, updated-at, vcs.ref.name, cicd.pipeline.run.url }
                                # cicd.pipeline.run.url is "" for label deploys
  - name: backend-2             # no commitSha: tracks main
  - name: front
```

## Per-preview URLs

Each exposed service of a preview gets the host `<service>-<slug>.<domain>` (the `<service>-<slug>` label capped at 63 characters, see below). One wildcard DNS record (and, for TLS, one wildcard certificate) for `*.<domain>` covers every preview.

- doublure writes the host into each exposed entry's `host` in `values.yaml`, and `preview-env` passes it to the service's Application as the Helm parameter `app.ingress.host`.
- doublure appends ` · [<host><path>](<scheme>://<host><path>)` to the service's line in the PR comment.

`tls` only picks the link scheme; ingress TLS, the ingress class and annotations stay in the service chart. Services not in `expose` (non-HTTP services, for example) get no host and no link.

Length caps: a slug longer than 53 characters (Helm's release-name limit, which Argo CD hits through the ApplicationSet's `releaseName`) and a `<service>-<slug>` host label longer than 63 characters (the DNS label limit) are cut, and a hash of the full value (6 hex characters) is appended. For example, the branch `feature/very-long-branch-name-that-keeps-going-and-going-past-the-helm-limit` gets the slug `feature-very-long-branch-name-that-keeps-going-5bec34`, and the service `front-office-web` in it gets the host `front-office-web-feature-very-long-branch-name-that-keep-395ff3.<domain>`.

Upgrade order: upgrade doublure (0.10.0 or later) first, then `preview-env` (0.3.0 or later). `preview-env` 0.3.0 reads only `host`, so an entry without it gets no ingress host. Preview files written by an older doublure carry no `host` until their next deploy write.

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
- Images tagged with the full git commit SHA: pinned services use the PR head SHA (label flow) or the `commit_sha` sent to `/v1/deploy`, main-tracking services use `$ARGOCD_APP_REVISION`.
- For services in `preview.expose`: ingress enabled and a Helm parameter `app.ingress.host` setting the ingress host. Otherwise the PR-comment link is dead.
- A service whose image is not built per commit (for example a database) may ignore `app.image.tag`.

These values are fixed in the chart today; making them configurable is on the [roadmap](ROADMAP.md).

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

Several PRs, from different repos and branches, labeled `preview:pricing` share the preview `pricing`: each pins its own services, and the rest run at `main`. A named preview and a branch preview with the same slug are the same preview: `preview:main` joins the preview of a branch called `main`. doublure never creates labels: labels are per repo, so `preview:<name>` must be created in each repo.

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

Teardown flow: a reconcile tears the PR down from each slug of its cleanup set: the PR is closed (merged or not), lost its label (for example after `unlabeled`), has conflicting labels, or moved to another preview. A `closed` delivery whose reconcile can't read the catalog or the PR falls back to tearing down, as closed, the payload's head branch and the payload's named labels, logged as a warning, so a close still tears down when GitHub reads fail. For each slug (the slugified head branch or named label), doublure reads `previews/<slug>/values.yaml` and unpins every service whose `pr-url` points to the PR: the entry goes back to a bare main-tracking `{name}`. If other pins remain, the file is updated and the remaining PRs' comments are refreshed; otherwise the file is deleted. Both writes use SHA optimistic locking + 409 retry, deciding update vs delete again on every retry. The PR's comment is edited (or created) to say what was removed and why: `PR closed`, ``label `<label>` removed``, ``label `<label>:<name>` removed``, ``conflicting `<label>:` labels`` or ``moved to preview `<slug>` ``.

Deleting the file drops `previews/<slug>/` from the git directory generator, and the ApplicationSet deletes the preview's Application and its resources. This requires the ApplicationSet controller policy `sync` (the default) or `create-delete`; with `create-only` or `create-update` a deleted preview keeps running. Closes missed while doublure was down, and pins written by a deploy after the PR closed, are caught by the cleanup sweeper. A labeled PR whose branch was renamed after the deploy is unpinned from its old slug by the sweeper, without a comment; push to redeploy it at the new slug.

## Cleanup sweep

`doublure cleanup` runs once per CronJob schedule (hourly by default). It lists `previews/*` and applies to each preview the first matching rule:

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
CLEANUP_TTL                 # cleanup only, optional: Go duration (e.g. 168h); unset disables expiry
```

GitHub App permissions: Contents R/W, Pull requests W, Metadata R. Subscribed event: `pull_request` (installation events are delivered automatically).

## Demo setup

Demo services: [demo-backend-1](https://github.com/doublurehq/demo-backend-1), [demo-backend-2](https://github.com/doublurehq/demo-backend-2), [demo-front](https://github.com/doublurehq/demo-front). Gitops repo: [demo-gitops](https://github.com/doublurehq/demo-gitops). Local dev of doublure itself: [DEVELOPMENT.md](../DEVELOPMENT.md).
