# Install

From an empty cluster to a first preview. Contract details live in [ARCHITECTURE](ARCHITECTURE.md); this guide links to them.

## 1. Prerequisites

- A Kubernetes cluster with Argo CD 3.1 or later (OCI chart source, Beta since v3.1.0), installed in the `argocd` namespace: the `preview-env` chart creates its AppProject and Applications there (`namespace: argocd` is hardcoded in its templates and in the examples).
- An ingress controller with TLS for the doublure webhook URL (GitHub calls it over https), plus wildcard DNS when preview URLs are used.
- A GitHub gitops repo, and service repos whose CI pushes images tagged with the full commit SHA, all under the one account (user or org) the App is installed on.
- Service images pullable from the preview namespaces: `preview-env` creates each namespace fresh, with no `imagePullSecrets`. Use public images, node-level registry credentials, or pull secrets set by the service chart.
- `kubectl`, Helm 3.8+ and Go 1.26+ (for the `doublure` CLI).

## 2. Gitops repo

- Commit a `services.yaml` at the root, starting from [`examples/argocd/services.yaml`](../examples/argocd/services.yaml). `serviceRepos` is required; `preview` and `serviceCharts` are optional.
- `serviceRepos` URLs must be `https://github.com/<owner>/<repo>` (with or without `.git`), or the label flow never matches them.
- Contract: ARCHITECTURE [Gitops repo contract](ARCHITECTURE.md#gitops-repo-contract).
- doublure creates `previews/` itself; a missing directory reads as no previews.
- doublure commits straight to the default branch. Its protection must let the App push directly: no required pull request, or the App on the bypass list.

## 3. Argo CD wiring

- If the gitops repo or any service repo is private, register it in Argo CD first (repository credentials; see Argo CD [Private Repositories](https://argo-cd.readthedocs.io/en/stable/user-guide/private-repositories/)). The public `oci://ghcr.io/doublurehq/charts` needs no credentials.
- Copy [`examples/argocd/appproject-previews.yaml`](../examples/argocd/appproject-previews.yaml) and [`applicationset-previews.yaml`](../examples/argocd/applicationset-previews.yaml), and adapt both as listed in [Adapt to your org](../examples/argocd/README.md#adapt-to-your-org).
- Take the files from the doublure release tag you install (`https://github.com/doublurehq/doublure/tree/vX.Y.Z/examples/argocd`): they carry the matching `preview-env` `targetRevision`. When installing the latest, use `main`'s files.
- Apply both:

  ```bash
  kubectl apply -f appproject-previews.yaml
  kubectl apply -f applicationset-previews.yaml
  ```

  Re-apply after a chart bump: Argo CD does not manage the ApplicationSet.

## 4. Public URL for doublure

Choose the https host GitHub will call, e.g. `https://doublure.example.com`, and create its A or CNAME record pointing at the ingress controller. The chart's Ingress serves it (step 6), and it is the webhook base URL.

## 5. GitHub App

1. Install the CLI:

   ```bash
   go install github.com/doublurehq/doublure/cmd/doublure@latest
   ```

   `$(go env GOPATH)/bin` must be on `PATH`. Use `@vX.Y.Z` to match a pinned chart.
2. In an empty working directory, create the App. It writes `.env` and `dev.private-key.pem` there.
   - Run it on a machine with a desktop browser signed in to GitHub, not over SSH on a remote host: it listens on `127.0.0.1` and waits 5 minutes for the browser redirect. Otherwise create the App by hand (item 5).
   - The App is private, so it can only be installed on the account that owns it. When the repos are in an org, `--owner <org>` is required, and creating the App needs org owner (or App manager) rights.

   ```bash
   APP_NAME=my-doublure            # unique on GitHub
   GITOPS=my-org/gitops            # owner/repo
   ORG=my-org                      # org that owns the repos
   ```

   Repos in an org:

   ```bash
   doublure bootstrap-app --name "$APP_NAME" \
     --webhook-url https://doublure.example.com \
     --gitops-repo "$GITOPS" --owner "$ORG"
   ```

   Repos in your personal account:

   ```bash
   doublure bootstrap-app --name "$APP_NAME" \
     --webhook-url https://doublure.example.com \
     --gitops-repo "$GITOPS"
   ```
3. Install the App on the gitops repo and every service repo, all under that one account. doublure accepts webhooks from that single installation only.
4. In the same directory, list the installations:

   ```bash
   . ./.env
   doublure verify-app
   ```

   Put the listed `id=` into `GITHUB_INSTALLATION_ID` in `.env`, then run `. ./.env` again.
5. Alternative: create the App by hand following [DEVELOPMENT.md Appendix B](../DEVELOPMENT.md#appendix-b--manual-app-creation-manifest-flow-fallback), with the webhook URL `<base>/webhooks/github`, and write the same `.env` keys yourself.

`.env` and the PEM are secrets: keep them out of git and store them in a secret manager. The PEM is the only copy; if lost, generate a new private key in the App settings.

## 6. Install doublure

GitHub needs https, so set `ingress.className` and `ingress.tls` in a `values.yaml`:

```yaml
ingress:
  className: nginx                # your IngressClass
  tls:
    - hosts: [doublure.example.com]
      secretName: doublure-tls
```

The chart has no `ingress.annotations`: the TLS Secret must exist beforehand (created by you, or by a cert-manager `Certificate`), or the controller's default certificate must be valid for the host. On a local test cluster you can skip the file and use http.

From the directory holding `.env`, after `. ./.env`:

```bash
kubectl create namespace doublure
kubectl create secret generic doublure -n doublure \
  --from-file=github-private-key="$GITHUB_PRIVATE_KEY_PATH" \
  --from-literal=github-webhook-secret="$GITHUB_WEBHOOK_SECRET" \
  --from-literal=deploy-token="$DOUBLURE_DEPLOY_TOKEN"
helm install doublure oci://ghcr.io/doublurehq/charts/doublure -n doublure \
  --set-string config.githubAppId="$GITHUB_APP_ID" \
  --set-string config.githubInstallationId="$GITHUB_INSTALLATION_ID" \
  --set config.gitopsRepo="$GITOPS_REPO" \
  --set existingSecret=doublure \
  --set ingress.enabled=true --set ingress.host=doublure.example.com \
  --set cleanup.interval=1h \
  -f values.yaml
```

- Pin `--version` in production. The image tag defaults to the chart `appVersion`.
- The Ingress also exposes `POST /v1/deploy`, the manual trigger, protected by the `deploy-token` bearer token (see [README](../README.md)).
- `cleanup.ttl` (e.g. `168h`) is optional: it also deletes previews with no deploy for that long.
- `cleanup.interval=1h` runs the cleanup sweep in the server, which exports its metrics (with the Prometheus Operator, scrape them with `metrics.serviceMonitor.enabled=true` and `metrics.serviceMonitor.labels` matching its selector, e.g. `release: <kube-prometheus-stack release>`); `cleanup.enabled=true`, an hourly CronJob without metrics, is the alternative, not to combine with it.
- Keep `replicaCount: 1`.

Once the Secret exists, delete the local `.env` and PEM.

Check:

```bash
curl https://doublure.example.com/healthz   # {"status":"ok"}
```

## 7. Service repos

- Create the `preview` label (or your custom `preview.label`) in each service repo, e.g. `gh label create preview --repo <owner>/<repo>`. Create any `preview:<name>` label yourself in each repo too: doublure never creates labels.
- CI must push the image tagged with the full commit SHA: ARCHITECTURE [Pinned and branch-tracking services](ARCHITECTURE.md#pinned-and-branch-tracking-services).
- Chart contract and `serviceCharts` overrides: ARCHITECTURE [Service repo contract](ARCHITECTURE.md#service-repo-contract).

## 8. First preview

1. Label a PR `preview`.
2. Expect:
   - within seconds, a doublure comment on the PR and `previews/<slug>/values.yaml` in the gitops repo;
   - within the ApplicationSet poll (3 minutes by default), one Argo CD Application per service.
3. With preview URLs, the comment links each exposed service.
4. Removing the label or closing the PR tears the preview down.
5. PRs from forks never get a preview.

## 9. Preview URLs (optional)

Set `preview.domain` in `services.yaml`, create a wildcard DNS record `*.<domain>` pointing to the ingress controller, and list the services under `expose`. Details: ARCHITECTURE [Per-preview URLs](ARCHITECTURE.md#per-preview-urls).

## 10. Upgrade

1. Upgrade doublure first: `helm upgrade` with the same flags as step 6.
2. Then set the ApplicationSet's `preview-env` `targetRevision` to the one in the new tag's [`examples/argocd/applicationset-previews.yaml`](../examples/argocd/applicationset-previews.yaml), and re-apply it.

## 11. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Deliveries return 404 on `POST /` | The webhook URL lacks `/webhooks/github` | Set the App's webhook URL to `<base>/webhooks/github` |
| Deliveries return 401 `invalid signature` | `github-webhook-secret` differs from the App's webhook secret | Make the Secret key and the App's webhook secret match, then restart doublure |
| Deliveries return 403 `installation not allowed` | `config.githubInstallationId` is wrong, or the repo is under another account | Set the ID listed by `doublure verify-app`; install the App on repos of that account only |
| Deliveries time out or fail to connect | The ingress or DNS is unreachable from GitHub | Expose the host publicly over https and check `/healthz` from outside |
| `preview reconcile failed … services.yaml: …` in `kubectl logs -n doublure deploy/doublure` | `services.yaml` is malformed; the error names the key | Fix that key in `services.yaml` |
| `preview reconcile skipped … reason="repo not in catalog"` | The `serviceRepos` URL isn't `https://github.com/<owner>/<repo>[.git]` | Fix the URL in `services.yaml` |
| `preview reconcile skipped … reason="fork pr"` | The PR comes from a fork | Expected: fork PRs never get a preview |
| No comment after labeling | The App isn't installed on that repo, or the label differs from `preview.label` | Install the App on the repo, or use the configured label |
| Pods in `ImagePullBackOff` | The image isn't pushed yet, or the namespace can't pull from the registry | If CI is still pushing, wait; otherwise check registry access (prerequisites) |
