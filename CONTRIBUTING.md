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

## License

Contributions are licensed under [Apache-2.0](LICENSE).
