BINARY := doublure
CMD := ./cmd/doublure

.PHONY: build test lint docker-build dev-bootstrap dev-verify dev-serve dev-cleanup

build:
	CGO_ENABLED=0 go build -o $(BINARY) $(CMD)

test:
	go test ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build -t $(BINARY):latest .

# Creates a GitHub App via the manifest flow and writes .env + PEM in this
# directory. The webhook goes to DOUBLURE_WEBHOOK_URL (doublure's public base
# URL; /webhooks/github is appended) when set, otherwise to the ngrok tunnel,
# which then must be running in another terminal (`ngrok http 8080`).
# Pass GITOPS_REPO=<owner>/<repo> to prefill it in .env, otherwise edit .env
# after the fact:
#   GITOPS_REPO=myorg/my-gitops make dev-bootstrap
#   DOUBLURE_WEBHOOK_URL=https://doublure.example.com make dev-bootstrap
dev-bootstrap:
	go run $(CMD) bootstrap-app --gitops-repo "$(GITOPS_REPO)" --webhook-url "$(DOUBLURE_WEBHOOK_URL)"

# Sanity-check the App credentials currently in .env / dev.private-key.pem.
dev-verify:
	set -a; . ./.env; set +a; go run $(CMD) verify-app

# Start the server with the current .env.
dev-serve:
	set -a; . ./.env; set +a; go run $(CMD) serve

# Run one cleanup sweep with the current .env. Set CLEANUP_TTL (e.g. 168h) to
# also expire idle previews:
#   CLEANUP_TTL=168h make dev-cleanup
dev-cleanup:
	set -a; . ./.env; set +a; go run $(CMD) cleanup
