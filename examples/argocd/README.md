# ArgoCD example

The ArgoCD side of doublure: an AppProject and an ApplicationSet that turn every `previews/<slug>/` directory of your gitops repo into one preview environment, rendered by the `preview-env` chart.

## Gitops repo contract

- `services.yaml` at the repo root maps each service name to its repo URL (`serviceRepos`). The copy here lists the demo services.
- doublure writes `previews/<slug>/values.yaml`, one per preview, with the services it runs and their PR metadata. Sample: [`values.golden.yaml`](../../internal/preview/testdata/values.golden.yaml).
- The ApplicationSet renders `preview-env` with `services.yaml` and `previews/<slug>/values.yaml` as value files.

## Apply

1. `services.yaml`: commit it to the root of your gitops repo.
2. `appproject-previews.yaml`: `kubectl apply -f appproject-previews.yaml`
3. `applicationset-previews.yaml`: `kubectl apply -f applicationset-previews.yaml`

## Adapt to your org

- Replace `https://github.com/doublurehq/demo-gitops.git` with your gitops repo URL, in both the AppProject and the ApplicationSet.
- Replace `oci://ghcr.io/doublurehq/charts/preview-env` if you host the chart elsewhere.
- Replace the `serviceRepos` entries in `services.yaml` with your services.

The charts are published to `oci://ghcr.io/doublurehq/charts/*` from the first release on.

## Install doublure

Create the Secret the chart reads (it never creates Secrets itself):

```bash
kubectl create secret generic doublure -n doublure \
  --from-file=github-private-key=./app.private-key.pem \
  --from-literal=github-webhook-secret="$GITHUB_WEBHOOK_SECRET" \
  --from-literal=deploy-token="$DOUBLURE_DEPLOY_TOKEN"
```

Install the chart from [`charts/doublure`](../../charts/doublure):

```bash
helm install doublure charts/doublure -n doublure \
  --set-string config.githubAppId=<app-id> \
  --set-string config.githubInstallationId=<installation-id> \
  --set config.gitopsRepo=<owner>/<gitops-repo> \
  --set existingSecret=doublure
```

Set `ingress.enabled=true` and `ingress.host` to expose `/webhooks/github` (GitHub App webhook) and `/v1/deploy` (CI). The image tag defaults to the chart `appVersion`; no image is published before the first release.
