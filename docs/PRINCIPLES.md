# Principles → doublure

doublure is an open-source (Apache-2.0), self-hosted preview-environment tool for teams running Kubernetes + ArgoCD. It implements the practices from [AI Writes Code in Hours. It Still Takes Weeks to Ship.](https://medium.com/@aminebenseddik/ai-writes-code-in-hours-it-still-takes-weeks-to-ship-7fb07f87cc07): when code is produced at machine speed, the bottleneck is validation and integration, and per-PR preview environments remove the shared-staging queue.

Not a SaaS. Single-tenant, installed in the user's own cluster with their own GitHub App.

| Principle (post) | doublure feature | Status |
|---|---|---|
| Every PR gets its own isolated, ephemeral world | One `previews/<slug>/` dir in the gitops repo per branch → one ArgoCD ApplicationSet entry → one namespace | ✅ |
| The branch is the unit of preview | Slug derived from `head_ref`; PRs on the same branch across repos share one preview | ✅ |
| Composite, full-clone previews: changed services at PR version, everything else at main | `services.yaml` catalog; services with `commitSha` are pinned, others track `main` | ✅ |
| Build once, deploy many | CI builds the image, then calls `POST /v1/deploy` with the commit SHA; doublure never builds | ✅ |
| Deployment as version-controlled code; parallel, independently failing deploys | GitOps write + ArgoCD ApplicationSet, one Application per service | ✅ |
| Stable, shareable URL | PR comment fan-out to every PR in the preview | ⚠️ comment exists, no per-preview URL (ingress host is not slug-scoped) |
| Teardown on merge/close | `pull_request` closed webhook → delete preview | ❌ |
| Hard TTLs + sweeper for zombie environments | `doublure cleanup` CronJob: stale (no open PR) + TTL | ❌ |
| Data isolation, per-environment seed data | Per-preview database, created and dropped with the preview | ❌ DB name is the service name on a shared Postgres: previews of one service share a DB |
| Right-size for cost | `preview-env` chart profile: one small replica per service | ⚠️ chart-level, not enforced |
| Measure (DORA, spin-up time) | Dashboard: live previews, spin-up time, teardowns, zombies swept | ❌ |
| Validate AI output in a running system before it touches main | E2E demo: agent-opened PR → preview → tests against preview URL → merge → teardown | ❌ |
| Start small, evolve | v0 (a CI action plus a cleanup CronJob) → doublure (GitHub App + API) | ✅ documented |

Roadmap to close the gaps: [ROADMAP.md](ROADMAP.md).
