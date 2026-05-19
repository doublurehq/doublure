FROM golang:1-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /previo ./cmd/previo

FROM scratch
COPY --from=builder /previo /previo
ENTRYPOINT ["/previo"]
