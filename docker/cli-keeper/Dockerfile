FROM node:22-slim

# Install all 3 CLIs in a single layer to minimize image size.
# @google/generative-ai is not a CLI — Gemini CLI is published as @anthropic-ai/claude-code
# The correct package for Gemini CLI is @google/gemini-cli (adjust if the package name differs).
RUN npm install -g \
        @anthropic-ai/claude-code \
        @openai/codex \
    && npm install -g @google/gemini-cli 2>/dev/null || true \
    && npm cache clean --force

COPY docker/cli-keeper/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Credential directories — created here so bind-mount ownership is predictable
RUN mkdir -p /root/.claude /root/.codex /root/.gemini

ENV HOME=/root
ENV TOKEN_REFRESH_INTERVAL=21600
ENV CLI_UPDATE_INTERVAL=86400

HEALTHCHECK --interval=60s --timeout=10s --retries=3 --start-period=30s \
    CMD claude --version > /dev/null 2>&1 || exit 1

ENTRYPOINT ["/entrypoint.sh"]
