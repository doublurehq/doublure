# Roadmap to v1

v1 = anyone can install doublure on their own cluster and get the full loop: a preview per branch, deployed by Argo CD, torn down when it is no longer needed.

Shipped so far: [CHANGELOG.md](../CHANGELOG.md) and [GitHub releases](https://github.com/doublurehq/doublure/releases).

## v1

| # | Change | Goal | Depends on |
|---|---|---|---|
| 5 | **CI integration**: reusable GitHub Action calling `/v1/deploy` | build once, deploy many | — |
| 6 | **Metrics + dashboard**: deploy/teardown events, spin-up time (deploy call → ArgoCD healthy), live previews, zombies swept; Next.js dashboard | measure | — |
| 7 | **Install guide**: GitHub App, chart install, gitops repo contract, CI step | deployable by anyone | 5, 9 |
| 8 | **Slug length cap**: truncate and hash long branch slugs so they fit a 63-character namespace name and the `<service>-<slug>` host label | any branch name works | — |
| 9 | **Bring your own chart**: per-service default branch, chart path, image-tag parameter and ingress-host parameter, instead of `main`, `k8s/`, `app.image.tag` and `app.ingress.host`, so existing company charts work unchanged | fits existing repos | — |
| 10 | **`bootstrap-app --webhook-url`**: create the App with a given webhook URL, for installs without ngrok | deployable by anyone | — |
| 11 | **E2E demo**: agent-opened PR → preview → tests against preview URL → merge → teardown | validate AI output | all |

## Non-goals

Multi-tenancy, hosted control plane, in-cluster agent, billing, SSO, queue/workers.

## After v1

- Auth via CI `GITHUB_TOKEN` instead of a static deploy token
- Scale-to-zero / off-hours pause for previews
- Check the image exists in the registry before writing gitops
- Contract tests against preview APIs
