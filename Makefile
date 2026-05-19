BINARY := previo
CMD := ./cmd/previo

.PHONY: build test lint docker-build

build:
	CGO_ENABLED=0 go build -o $(BINARY) $(CMD)

test:
	go test ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build -t $(BINARY):latest .
