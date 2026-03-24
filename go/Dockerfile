# Stage 1: Build
FROM golang:1.25-bookworm AS builder

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

# All LLM providers use CLI subprocess (goroutine-based async concurrency).
# Mount the CLI binaries and credential directories via docker-compose volumes:
#
# Claude CLI:
#   - mount claude binary to /usr/local/bin/claude:ro
#   - mount ${CLAUDE_CONFIG_DIR} to ${CONTAINER_HOME}/.claude
#   - mount ${CLAUDE_CREDENTIALS_FILE} to ${CONTAINER_HOME}/.claude.json
#   - set CLAUDE_CLI_PATH=/usr/local/bin/claude (or rely on PATH default)
#
# Codex CLI (static Rust binary):
#   - mount the platform binary to /usr/local/bin/codex:ro
#   - mount ${CODEX_CONFIG_DIR} to ${CONTAINER_HOME}/.codex
#   - set CODEX_CLI_PATH=/usr/local/bin/codex (or rely on PATH default)
#
# Gemini CLI (optional):
#   - mount gemini binary to /usr/local/bin/gemini:ro
#   - set GEMINI_CLI_PATH=/usr/local/bin/gemini (or rely on PATH default)
#
# Set CONTAINER_HOME to the appuser home directory (/home/appuser) so that
# all CLI binaries can locate their credential files inside the container.
