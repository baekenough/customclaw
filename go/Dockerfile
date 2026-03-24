# Stage 1: Build
FROM golang:1.23-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/app ./cmd/app

# Stage 2: Runtime
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash appuser
USER appuser
WORKDIR /home/appuser

COPY --from=builder /bin/worker /usr/local/bin/worker
COPY --from=builder /bin/app /usr/local/bin/app

# Default to worker; override with entrypoint in docker-compose
ENTRYPOINT ["worker"]
