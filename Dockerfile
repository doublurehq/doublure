FROM golang:1-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /doublure ./cmd/doublure

FROM scratch
COPY --from=builder /doublure /doublure
ENTRYPOINT ["/doublure"]
