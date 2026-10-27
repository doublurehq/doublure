# Contributing

## Setup

Go 1.25+. Local GitHub App and server: [DEVELOPMENT.md](DEVELOPMENT.md).

```bash
make build
make test
make lint    # golangci-lint
```

CI runs gofmt, `go vet`, `go build` and `go test -race` on every push and PR. All must pass before merge.

## Pull requests

- One change per PR, small and short-lived.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/): `type(scope): description`.
- Add or update tests with behaviour changes.

## Releases

Releases are cut by [release-please](https://github.com/googleapis/release-please) from Conventional Commits.

- On `main` it keeps a `chore(main): release X.Y.Z` PR up to date. Merging it tags `vX.Y.Z`, creates the GitHub Release, and publishes `ghcr.io/doublurehq/doublure:X.Y.Z` (also `X.Y` and `latest`, for linux/amd64 and linux/arm64) and the `doublure` chart.
- The release PR gets no CI, because it is created by `GITHUB_TOKEN`. Before merging, a maintainer checks that it only touches `CHANGELOG.md`, `.release-please-manifest.json` and `charts/doublure/Chart.yaml`.

Chart versioning:

- Never hand-edit `version`/`appVersion` in `charts/doublure/Chart.yaml`; the release sets both.
- A PR that changes `charts/preview-env` or `charts/app` must bump that chart's `version` (enforced by the required `lint` check of the `Charts` workflow).
- Merged bumps are published to `oci://ghcr.io/doublurehq/charts` on the next push to `main`.

## License

Contributions are licensed under [Apache-2.0](LICENSE).
