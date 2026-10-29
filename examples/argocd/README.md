# ArgoCD example

The ArgoCD side of doublure: an AppProject and an ApplicationSet that turn every `previews/<slug>/` directory of your gitops repo into one preview environment, rendered by the `preview-env` chart.

Requires Argo CD 3.1 or later (OCI chart source).

## Gitops repo contract

- `services.yaml` at the repo root maps each service name to its repo URL (`serviceRepos`). The copy here lists the demo services.
- doublure writes `previews/<slug>/values.yaml`, one per preview, with the services it runs and their PR metadata. Sample: [`values.golden.yaml`](../../internal/preview/testdata/values.golden.yaml).
- The ApplicationSet renders `preview-env` with `services.yaml` and `previews/<slug>/values.yaml` as value files.

## Apply

1. `services.yaml`: commit it to the root of your gitops repo.
2. `appproject-previews.yaml`: `kubectl apply -f appproject-previews.yaml`
3. `applicationset-previews.yaml`: `kubectl apply -f applicationset-previews.yaml`

## Preview URLs

To give each preview its own URL, uncomment the `preview` block in `services.yaml` and set `domain`, then create a wildcard DNS record for `*.<domain>` pointing at your ingress controller. Each service listed under `expose` gets the ingress host `<service>-<slug>.<domain>` (capped to a 63-character label, see ARCHITECTURE) and a link in the PR comment; its chart must enable ingress and honor `app.ingress.host` (or its `hostParam`). This needs `preview-env` 0.3.0 or later and doublure 0.10.0 or later: after changing the chart version (`targetRevision`) in `applicationset-previews.yaml`, re-apply it with `kubectl apply -f applicationset-previews.yaml`, as Argo CD does not manage the ApplicationSet itself.

Per-service chart settings (`serviceCharts` in `services.yaml`) need `preview-env` 0.4.0 or later and doublure 0.11.0 or later; with an older chart the block is ignored and the defaults apply.

## Adapt to your org

- Replace `https://github.com/doublurehq/demo-gitops.git` with your gitops repo URL, in both the AppProject and the ApplicationSet.
- Replace `oci://ghcr.io/doublurehq/charts/preview-env` if you host the chart elsewhere.
- Replace the `serviceRepos` entries in `services.yaml` with your services.

Charts are published to `oci://ghcr.io/doublurehq/charts/*`.

## Install doublure

Create the namespace and the Secret the chart reads (it never creates Secrets itself):

```bash
kubectl create namespace doublure
kubectl create secret generic doublure -n doublure \
  --from-file=github-private-key=./app.private-key.pem \
  --from-literal=github-webhook-secret="$GITHUB_WEBHOOK_SECRET" \
  --from-literal=deploy-token="$DOUBLURE_DEPLOY_TOKEN"
```

Install the chart from [`charts/doublure`](../../charts/doublure):

```bash
helm install doublure oci://ghcr.io/doublurehq/charts/doublure --version 0.1.0 -n doublure \
  --set-string config.githubAppId=<app-id> \
  --set-string config.githubInstallationId=<installation-id> \
  --set config.gitopsRepo=<owner>/<gitops-repo> \
  --set existingSecret=doublure
```

Set `ingress.enabled=true` and `ingress.host` to expose `/webhooks/github` (GitHub App webhook) and `/v1/deploy` (manual trigger). The image tag defaults to the chart `appVersion` (`ghcr.io/doublurehq/doublure:<appVersion>`).
