# Install

From an empty cluster to a first preview. Contract details live in [ARCHITECTURE](ARCHITECTURE.md); this guide links to them.

## 1. Prerequisites

- A Kubernetes cluster with Argo CD 3.1 or later (OCI chart source, Beta since v3.1.0), installed in the `argocd` namespace: the `preview-env` chart creates its AppProject and Applications there (`namespace: argocd` is hardcoded in its templates and in the examples).
- An ingress controller with TLS for the doublure webhook URL (GitHub calls it over https), plus wildcard DNS when preview URLs are used.
- A GitHub gitops repo, and service repos whose CI pushes images tagged with the full commit SHA, all under the one account (user or org) the App is installed on.
- `kubectl`, Helm 3.8+ and Go 1.25+ (for the `doublure` CLI).

## 2. Gitops repo

- Commit a `services.yaml` at the root, starting from [`examples/argocd/services.yaml`](../examples/argocd/services.yaml). `serviceRepos` is required; `preview` and `serviceCharts` are optional.
- `serviceRepos` URLs must be `https://github.com/<owner>/<repo>` (with or without `.git`), or the label flow never matches them.
- Contract: ARCHITECTURE [Gitops repo contract](ARCHITECTURE.md#gitops-repo-contract).
- doublure creates `previews/` itself; a missing directory reads as no previews.

## 3. Argo CD wiring

- If the gitops repo or any service repo is private, register it in Argo CD first (repository credentials; see Argo CD [Private Repositories](https://argo-cd.readthedocs.io/en/stable/user-guide/private-repositories/)). The public `oci://ghcr.io/doublurehq/charts` needs no credentials.
- Copy [`examples/argocd/appproject-previews.yaml`](../examples/argocd/appproject-previews.yaml) and [`applicationset-previews.yaml`](../examples/argocd/applicationset-previews.yaml), and adapt both as listed in [Adapt to your org](../examples/argocd/README.md#adapt-to-your-org).
- Keep the `preview-env` `targetRevision` from the example; it tracks the current chart.
- Apply both:

  ```bash
  kubectl apply -f appproject-previews.yaml
  kubectl apply -f applicationset-previews.yaml
  ```

  Re-apply after a chart bump: Argo CD does not manage the ApplicationSet.

## 4. Public URL for doublure

Choose the https host GitHub will call, e.g. `https://doublure.example.com`. The chart's Ingress serves it (step 6), and it is the webhook base URL.

## 5. GitHub App

1. Install the CLI:

   ```bash
   go install github.com/doublurehq/doublure/cmd/doublure@latest
   ```

   `$(go env GOPATH)/bin` must be on `PATH`. Use `@vX.Y.Z` to match a pinned chart.
2. In an empty working directory, create the App. It writes `.env` and `dev.private-key.pem` there:

   ```bash
   doublure bootstrap-app --name <unique-app-name> \
     --webhook-url https://doublure.example.com \
     --gitops-repo <owner>/<gitops-repo> [--owner <org>]
   ```
3. Install the App on the gitops repo and every service repo, all under that one account. doublure accepts webhooks from that single installation only.
4. In the same directory, list the installations:

   ```bash
   . ./.env
   doublure verify-app
   ```

   Put the listed `id=` into `GITHUB_INSTALLATION_ID` in `.env`, then run `. ./.env` again.
5. Alternative: create the App by hand following [DEVELOPMENT.md Appendix B](../DEVELOPMENT.md#appendix-b--manual-app-creation-manifest-flow-fallback), with the webhook URL `<base>/webhooks/github`, and write the same `.env` keys yourself.

## 6. Install doublure

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
  --set cleanup.enabled=true
```

- Pin `--version` in production.
- For TLS, set `ingress.className` and `ingress.tls` (the standard Ingress `tls` list) in a `-f values.yaml`.
- `cleanup.ttl` (e.g. `168h`) is optional: it also deletes previews with no deploy for that long.
- Keep `replicaCount: 1`.

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
2. Expect, within the ApplicationSet poll (3 minutes by default):
   - a doublure comment on the PR;
   - `previews/<slug>/values.yaml` in the gitops repo;
   - one Argo CD Application per service.
3. With preview URLs, the comment links each exposed service.
4. Removing the label or closing the PR tears the preview down.

## 9. Preview URLs (optional)

Set `preview.domain` in `services.yaml`, create a wildcard DNS record `*.<domain>` pointing to the ingress controller, and list the services under `expose`. Details: ARCHITECTURE [Per-preview URLs](ARCHITECTURE.md#per-preview-urls).

## 10. Upgrade

1. Upgrade doublure first: `helm upgrade` with the same flags as step 6.
2. Then bump the `preview-env` `targetRevision` in the ApplicationSet and re-apply it.

## 11. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Deliveries return 404 on `POST /` | The webhook URL lacks `/webhooks/github` | Set the App's webhook URL to `<base>/webhooks/github` |
| Deliveries return 401 `invalid signature` | `github-webhook-secret` differs from the App's webhook secret | Make the Secret key and the App's webhook secret match, then restart doublure |
| Deliveries return 403 `installation not allowed` | `config.githubInstallationId` is wrong, or the repo is under another account | Set the ID listed by `doublure verify-app`; install the App on repos of that account only |
| Deliveries time out or fail to connect | The ingress or DNS is unreachable from GitHub | Expose the host publicly over https and check `/healthz` from outside |
| `preview reconcile failed … services.yaml: …` in `kubectl logs -n doublure deploy/doublure` | `services.yaml` is malformed; the error names the key | Fix that key in `services.yaml` |
| `preview reconcile skipped … reason="repo not in catalog"` | The `serviceRepos` URL isn't `https://github.com/<owner>/<repo>[.git]` | Fix the URL in `services.yaml` |
| No comment after labeling | The App isn't installed on that repo, or the label differs from `preview.label` | Install the App on the repo, or use the configured label |
| Pods in `ImagePullBackOff` right after a push | The image isn't pushed yet | Wait: it resolves on its own once CI pushes the image |
