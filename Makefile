BINARY := previo
CMD := ./cmd/previo
GITOPS_REPO ?= amine7536/akpe-gitops

.PHONY: build test lint docker-build dev-bootstrap dev-verify dev-serve

build:
	CGO_ENABLED=0 go build -o $(BINARY) $(CMD)

test:
	go test ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build -t $(BINARY):latest .

# Creates a dev GitHub App via the manifest flow and writes .env + PEM in this
# directory. Requires `ngrok http 8080` to be running in another terminal.
dev-bootstrap:
	go run $(CMD) bootstrap-app --gitops-repo $(GITOPS_REPO)

# Sanity-check the App credentials currently in .env / dev.private-key.pem.
dev-verify:
	set -a; . ./.env; set +a; go run $(CMD) verify-app

# Start the server with the current .env.
dev-serve:
	set -a; . ./.env; set +a; go run $(CMD) serve
