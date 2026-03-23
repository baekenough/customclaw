FROM python:3.12-slim
RUN groupadd -g 1000 appuser && useradd -u 1000 -g 1000 -m appuser

# Install system dependencies: Node.js (Codex CLI), git, gh (GitHub CLI)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    gnupg && \
    # Node.js 22.x
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    # GitHub CLI official apt repository
    curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list && \
    apt-get update && apt-get install -y --no-install-recommends \
    gh \
    nodejs && \
    rm -rf /var/lib/apt/lists/*

# Configure git identity for omcustom-driver commits (appuser)
RUN su -c 'git config --global user.name "omcustom-driver" && git config --global user.email "omcustom-driver@noreply.github.com"' appuser

RUN ln -sf /usr/lib/node_modules/@openai/codex/bin/codex.js /usr/local/bin/codex

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY bot_engine/ ./bot_engine/
COPY bots/ ./bots/
RUN chown -R appuser:appuser /app
USER appuser
CMD ["python", "-m", "bot_engine.app"]
