# targetRevision and the ImagePullBackOff Race Condition

## Context

Preview environments have two types of services:

- **PR services** (`commitSha` set): pinned to a specific commit SHA. `targetRevision: <sha>` and `image.tag: <sha>`. Only gitops changes trigger resyncs.
- **Main-tracking services** (`commitSha` omitted): follow `main` branch. `targetRevision: main` and `image.tag: $ARGOCD_APP_REVISION`. Auto-sync on new main commits.

## The Race Condition

When a developer pushes to `main` on a service repo (e.g. backend-2):

```
1. Push to main
2. CI starts: lint → test → build image → push to ghcr.io    (~3-5 min)
3. ArgoCD detects new commit on main                          (~0-3 min, polling interval)
4. ArgoCD syncs: resolves $ARGOCD_APP_REVISION to new SHA
5. Kubernetes pulls image for new SHA
```

If step 3+4 happens before step 2 completes, the image doesn't exist in the registry yet. The pod enters `ImagePullBackOff`.

## Why This Is Acceptable

### Kubernetes retries automatically

`ImagePullBackOff` uses exponential backoff: 10s → 20s → 40s → ... up to 5 minutes. The pod will start once the image lands in the registry. No manual intervention needed.

### ArgoCD self-heals

With `selfHeal: true`, ArgoCD continuously reconciles. A degraded application will recover on the next sync cycle after the image becomes available.

### The window is small

- CI build+push typically completes in 2-3 minutes
- ArgoCD polls every 3 minutes by default
- Worst case: ~3 minutes of `ImagePullBackOff` before the pod starts

### Preview environments are not production

Transient unavailability of a non-PR service in a preview environment has no user impact. The developer is working on the PR service, not the main-tracking ones.

## Alternatives Considered

### 1. CI fan-out (gitops writes SHA for all previews)

When a service's `main` is updated, CI updates `commitSha` in every active preview's `values.yaml`.

- **Pro**: Eliminates race condition entirely. Gitops is sole trigger.
- **Con**: Complex CI logic. Must enumerate all active previews and update each one. Concurrency issues with multiple services updating the same file.

### 2. ArgoCD webhooks

CI sends a webhook to ArgoCD _after_ the image is pushed, instead of relying on polling.

- **Pro**: ArgoCD only syncs when the image is guaranteed to exist.
- **Con**: Requires webhook infrastructure, firewall rules (CI → cluster), and ArgoCD API exposure. Doesn't help if ArgoCD also polls.

### 3. Argo CD Image Updater

Watches container registries for new tags and updates Applications automatically.

- **Pro**: Purpose-built tool for image tag management.
- **Con**: Designed for a different use case (continuous deployment of latest tags). Adds operational complexity (another controller, registry credentials, write-back config). Overkill for preview environments.

### 4. Init container image check

Add an init container that polls the registry until the image exists before the main container starts.

- **Pro**: Pod doesn't enter `ImagePullBackOff`.
- **Con**: Adds complexity to every service's Helm chart. Registry credentials in init container. Still delays startup.

## Decision

Accept the transient `ImagePullBackOff`. Kubernetes retry + ArgoCD self-heal handle recovery automatically. The alternatives add significant complexity for a problem that self-resolves in minutes.

## Community Alignment

This pattern (`targetRevision: main` + `$ARGOCD_APP_REVISION` for image tags) is documented in:

- [ArgoCD Helm docs](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/) — build env vars in helm parameters
- [ArgoCD Tracking Strategies](https://argo-cd.readthedocs.io/en/latest/user-guide/tracking_strategies/) — branch tracking for dev/preview
- [Akuity blog](https://akuity.io/blog/argo-cd-build-environment-examples) — build environment variable examples
- [Codefresh blog](https://codefresh.io/blog/argocd-application-target-revision-field/) — targetRevision as promotion mechanism

The community does not document the race condition as a significant concern. The standard mitigation is Kubernetes retry behavior.
