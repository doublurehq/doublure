# doublure deploy action

Calls doublure's `POST /v1/deploy` from a service repo's CI, so the pull request's image lands in its preview.

## Usage

Add the step after the image push:

```yaml
on:
  pull_request:

jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      # ... build and push the image tagged with the pull request head SHA

      - uses: doublurehq/doublure/actions/deploy@vX.Y.Z
        with:
          url: ${{ vars.DOUBLURE_URL }}
          token: ${{ secrets.DOUBLURE_DEPLOY_TOKEN }}
          service: backend-1
```

The action runs only on `pull_request` events. On any other event it logs a notice and exits without calling doublure. Place it after the image push: doublure pins the commit SHA, and Argo CD pulls that image.

## Inputs

| Input | Required | Description |
|---|---|---|
| `url` | yes | doublure base URL, without `/v1/deploy`. A trailing `/` is stripped. |
| `token` | yes | Deploy token (`DOUBLURE_DEPLOY_TOKEN` on the server). |
| `service` | yes | Service key in the doublure catalog (`services.yaml`). |
| `commit-sha` | no | Commit SHA to deploy. Defaults to `github.event.pull_request.head.sha`. |

## Outputs

| Output | Description |
|---|---|
| `preview-slug` | Slug of the preview the service was deployed to. |
| `gitops-commit-url` | URL of the gitops commit that pinned the service. |

The step fails on any non-2xx response and shows the server's `error`. Each entry of the response's `warnings` becomes a workflow warning.

## Fork pull requests

GitHub does not pass secrets to workflows triggered by fork pull requests, and `if:` cannot read secrets directly. Expose the secret through a job-level `env` and skip the step when it is empty:

```yaml
jobs:
  build:
    runs-on: ubuntu-24.04
    env:
      DOUBLURE_DEPLOY_TOKEN: ${{ secrets.DOUBLURE_DEPLOY_TOKEN }}
    steps:
      # ...
      - uses: doublurehq/doublure/actions/deploy@vX.Y.Z
        if: env.DOUBLURE_DEPLOY_TOKEN != ''
        with:
          url: ${{ vars.DOUBLURE_URL }}
          token: ${{ env.DOUBLURE_DEPLOY_TOKEN }}
          service: backend-1
```
