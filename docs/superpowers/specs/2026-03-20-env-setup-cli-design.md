# CustomClaw Setup CLI &amp; .env Separation Design Spec

## Goal

Make CustomClaw deployable as a public template: `git clone` → `uv run setup.py` → running platform. All personal credentials, host paths, and project-specific configurations must be extracted from version-controlled files into `.env` and generated config files.

## Core User Experience

```
git clone https://github.com/user/customclaw.git
cd customclaw
uv run setup.py
# Answer prompts → .env, bot YAML, dags/ ready
# docker compose up -d auto-launched
# Done. Bot responds in Slack.
```

## What Ships (Template)

| Included | Excluded (.gitignore) |
|----------|----------------------|
| Core platform code (`slack_bot/`) | `dags/*.py` (except example) |
| Docker infrastructure (`docker/`, `docker-compose.yml`) | `bots/*.yaml` (except example) |
| Memory system (`slack_bot/memory/`) | `workflows/` (all) |
| Tool system (`slack_bot/tools/`) | `.env` |
| Web UI (`web-ui/`) | |
| `setup.py`, `.env.example` | |
| `dags/example_hello_world.py` | |
| `bots/example.yaml` | |
| Migrations (`migrations/`) | |
| Docs (`docs/`, `README.md`, `README.en.md`) | |

## Platform Value (What Users Get)

- Slack ↔ LLM pipeline (Redis Stream, multi-bot worker)
- RAG/Hybrid Search memory (pgvector + OpenSearch nori)
- Airflow workflow integration (framework — users add their own DAGs)
- Web UI for bot management and monitoring
- Multi-provider LLM support (Claude CLI, Codex CLI)

## Changes Required

### 1. `docker-compose.yml` — Replace Hardcoded Paths

| Current | New Variable | Default |
|---------|-------------|---------|
| `${CONTAINER_HOME}/workspace` (bind mount, airflow/worker) | `${HOST_WORKSPACE_PATH}` | `~/workspace` |
| `${CONTAINER_HOME}/.claude` | `${CLAUDE_CONFIG_DIR}` | `~/.claude` |
| `${CONTAINER_HOME}/.claude.json` | `${CLAUDE_CREDENTIALS_FILE}` | `~/.claude.json` |
| `${CONTAINER_HOME}/.local/bin/claude` | `${CLAUDE_CLI_BINARY}` | `~/.local/bin/claude` |
| `${CONTAINER_HOME}/.codex` | `${CODEX_CONFIG_DIR}` | `~/.codex` |
| `HOME: ${CONTAINER_HOME}` | `HOME: ${CONTAINER_HOME}` | `/home/appuser` |

### 2. `.env.example` — Full Variable Catalog

```dotenv
# ─── Database ────────────────────────────────────
DB_USER=customclaw
DB_PASSWORD=changeme

# ─── Redis ───────────────────────────────────────
REDIS_URL=redis://redis:6379

# ─── OpenSearch ──────────────────────────────────
OPENSEARCH_URL=http://opensearch:9200
OPENSEARCH_ADMIN_PASSWORD=changeme

# ─── LLM Providers ──────────────────────────────
ANTHROPIC_API_KEY=sk-ant-...
VOYAGE_API_KEY=

# ─── Slack (first bot) ──────────────────────────
CUSTOMCLAW_SLACK_APP_TOKEN=xapp-...
CUSTOMCLAW_SLACK_BOT_TOKEN=xoxb-...

# ─── GitHub ──────────────────────────────────────
GITHUB_TOKEN=ghp_...

# ─── Airflow ─────────────────────────────────────
AIRFLOW_SECRET_KEY=
AIRFLOW_API_USER=admin
AIRFLOW_API_PASSWORD=changeme

# ─── Web UI (GitHub OAuth) ───────────────────────
NEXTAUTH_URL=http://localhost:3000
NEXTAUTH_SECRET=
GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=

# ─── Security ────────────────────────────────────
ENCRYPTION_KEY=

# ─── Host Paths ──────────────────────────────────
HOST_WORKSPACE_PATH=~/workspace
CLAUDE_CLI_BINARY=~/.local/bin/claude
CLAUDE_CONFIG_DIR=~/.claude
CLAUDE_CREDENTIALS_FILE=~/.claude.json
CODEX_CONFIG_DIR=~/.codex
CONTAINER_HOME=/home/user
```

### 3. `.gitignore` Changes

Add:
```gitignore
# User-specific configurations
dags/*.py
!dags/example_hello_world.py
bots/*.yaml
!bots/example.yaml
workflows/
```

### 4. `setup.py` — Interactive Setup CLI

**Implementation:** Single Python file, stdlib only. Run via `uv run setup.py`.

**Three-step flow:**

#### Step 1: Environment Configuration

Reads `.env.example` as template. For each variable:
- Shows description and default value
- Prompts user for input (Enter = accept default)
- Passwords/API keys masked via `getpass`
- Auto-generates `ENCRYPTION_KEY`, `NEXTAUTH_SECRET`, `AIRFLOW_SECRET_KEY` with `secrets.token_hex(32)`
- Host paths: detects current user's home directory for sensible defaults
- Writes result to `.env`
- If `.env` exists: asks "Overwrite / Merge / Cancel"

Grouped sections:
1. Database (DB_USER, DB_PASSWORD)
2. LLM Providers (ANTHROPIC_API_KEY, VOYAGE_API_KEY)
3. Slack (CUSTOMCLAW_SLACK_APP_TOKEN, CUSTOMCLAW_SLACK_BOT_TOKEN)
4. GitHub (GITHUB_TOKEN)
5. Airflow (AIRFLOW_SECRET_KEY, AIRFLOW_API_USER, AIRFLOW_API_PASSWORD)
6. Web UI (NEXTAUTH_URL, NEXTAUTH_SECRET, GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET)
7. Host Paths (HOST_WORKSPACE_PATH, CLAUDE_CLI_BINARY, CLAUDE_CONFIG_DIR, CLAUDE_CREDENTIALS_FILE, CODEX_CONFIG_DIR, CONTAINER_HOME)
8. Security (ENCRYPTION_KEY)

#### Step 2: First Bot Creation (optional)

Prompts:
- Bot name (kebab-case, used as filename)
- Display name
- Personality (one-liner)
- LLM provider: claude / codex (default: claude)
- Model: sonnet / opus / haiku (default: sonnet)
- Full agent mode: y/N (default: N)

Generates `bots/{name}.yaml` using Slack tokens from Step 1's `.env`. Creates `dags/` directory if missing.

#### Step 3: Launch (optional)

- Asks "Run `docker compose up -d` now?"
- If yes: runs `subprocess.run(["docker", "compose", "up", "-d"])`
- Shows service status summary

**Error handling:**
- Validates Slack tokens start with `xapp-` / `xoxb-`
- Validates paths exist (warns if not, allows override)
- Ctrl+C gracefully exits with partial `.env` save option

### 5. Example Files

#### `dags/example_hello_world.py`
Simple BashOperator DAG that prints "Hello from CustomClaw!" — verifies Airflow integration works.

#### `bots/example.yaml`
Full bot configuration template with inline comments explaining every field. All values use `${ENV_VAR}` references. Not a working bot — a documented reference.

### 6. Documentation Updates

**README.md / README.en.md:**
- Quick Start: replace `cp .env.example .env` + manual editing with `uv run setup.py`
- Prerequisites: add `uv` (recommended) or `python 3.12+`
- Airflow DAGs section: mention example DAG, guide users to create their own
- Bot configuration: reference `bots/example.yaml`

## File Change Summary

| File | Action |
|------|--------|
| `setup.py` | **New** — interactive setup CLI |
| `.gitignore` | **Edit** — add dags/, bots/, workflows/ with exceptions |
| `.env.example` | **Edit** — add host path variables, reorganize |
| `docker-compose.yml` | **Edit** — replace hardcoded paths with ${VAR} |
| `dags/example_hello_world.py` | **New** — example DAG |
| `bots/example.yaml` | **New** — example bot config |
| `README.md` | **Edit** — quick start, DAG/bot sections |
| `README.en.md` | **Edit** — same changes in English |

## Out of Scope

- DAG content (users create their own)
- Bot persona content (users define their own)
- GitHub Actions workflows (too project-specific)
- CI/CD pipeline
- Non-interactive mode (`--defaults` flag) — add later if needed
