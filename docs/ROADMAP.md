# Roadmap to v1

v1 = anyone can install doublure on their own cluster and get the full loop: a preview per branch, deployed by Argo CD, torn down when it is no longer needed.

Shipped so far: [CHANGELOG.md](../CHANGELOG.md) and [GitHub releases](https://github.com/doublurehq/doublure/releases).

## v1

| # | Change | Goal | Depends on |
|---|---|---|---|
| 11 | **E2E demo**: agent-opened PR → preview → tests against preview URL → merge → teardown | validate AI output | all |

## Non-goals

Multi-tenancy, hosted control plane, in-cluster agent, billing, SSO, queue/workers.

## After v1

- Scale-to-zero / off-hours pause for previews
- Check the image exists in the registry before writing gitops
- Contract tests against preview APIs
- Image tag format: derive the image tag from the commit SHA (e.g. sha-<short>) for CI that does not tag with the full SHA
