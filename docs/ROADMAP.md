# Roadmap to v1

v1 = anyone can install doublure on their own cluster and get the full loop described in [PRINCIPLES.md](PRINCIPLES.md). Each item below is scaffolded as its own change before implementation.

## Done

| | Change | PR |
|---|---|---|
| ✅ | Go scaffold, GitHub App auth | — |
| ✅ | Deploy logic port (slug, values, gitops write + 409 retry, PR comment fan-out) | doublure #1 |
| ✅ | `POST /v1/deploy`, bearer auth, `bootstrap-app`, `verify-app` | doublure #2 |
| ✅ | Webhook signature verification + typed routing (log only) | doublure #3 |
| ✅ | OSS repo baseline: LICENSE (Apache-2.0), README, CONTRIBUTING, SECURITY; CI on ubuntu-24.04 | doublure #4, #5 |
| ✅ | Webhook installation allow-list | doublure #7 |
| ✅ | Charts in doublure (preview-env, app, doublure), ApplicationSet example | doublure #8 |
| ✅ | Release pipeline: release-please, multi-arch image, OCI charts; demo setup on doublure charts | doublure #9, #10, #11, #12 |

## v1

| # | Change | Principle | Depends on |
|---|---|---|---|
| 4 | **Per-preview URL**: slug-scoped ingress host passed from preview-env chart to service charts; URL in PR comment | shareable URL | — |
| 5 | **Per-preview data isolation**: database named per preview (not per service), created on deploy, dropped on teardown | data isolation | — |
| 6 | **Teardown on PR close**: `pull_request` closed → remove service from preview; delete `previews/<slug>/` when no open PR remains | lifecycle | — |
| 7 | **Cleanup sweeper**: `doublure cleanup` (port akpe-cleanup runner, stale resolver, gitops plugin) + TTL | zombie cost | 5 |
| 9 | **CI integration**: reusable GitHub Action calling `/v1/deploy`; switch backend-1/2/front off akpe-workflows | build once, deploy many | — |
| 10 | **Metrics + dashboard**: deploy/teardown events, spin-up time (deploy call → ArgoCD healthy), live previews, zombies swept; Next.js dashboard | measure | 6, 7 |
| 11 | **Install guide**: GitHub App, chart install, gitops repo contract, CI step | deployable by anyone | 9 |
| 12 | **E2E demo**: agent-opened PR → preview → tests against preview URL → merge → teardown  | validate AI output | all |

## Non-goals

Multi-tenancy, hosted control plane, in-cluster agent, billing, SSO, queue/workers.

## After v1

- Auth via CI `GITHUB_TOKEN` instead of a static deploy token
- Scale-to-zero / off-hours pause for previews
- Registry check before deploy (`verifyImage`)
- Contract tests against preview APIs
