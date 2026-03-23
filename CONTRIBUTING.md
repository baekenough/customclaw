# Contributing to customclaw

Thank you for your interest in contributing to customclaw!

## Getting Started

1. Fork and clone the repository
2. Copy `.env.example` to `.env` and configure your environment
3. Run `python setup.py` for interactive setup (or manually edit `.env`)
4. Start services: `docker compose up -d`

## Development Stack

| Component | Tech | Location |
|-----------|------|----------|
| Bot Engine | Python (slack-bolt) | `bot_engine/` |
| Web Dashboard | Next.js 16 / React 19 | `web-ui/` |
| Database | PostgreSQL (pgvector) | `migrations/` |
| Search | OpenSearch (nori) | — |
| Queue | Redis Streams | — |
| Orchestration | Apache Airflow 3 | `dags/` |
| Infrastructure | Docker Compose | `docker/` |

## Branch Strategy

- `develop` — main development branch
- `feature/*` — feature branches (PR into develop)
- `fix/*` — bug fix branches
- `release/*` — release preparation

## Commit Convention

Follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — new feature
- `fix:` — bug fix
- `docs:` — documentation only
- `chore:` — maintenance
- `refactor:` — code refactoring

## Pull Request Process

1. Create a feature branch from `develop`
2. Make your changes
3. Ensure Docker services build successfully
4. Submit a PR with the provided template
5. PRs are automatically analyzed by the AI review pipeline

## Adding a New Bot

Bots are configured via YAML files in `bots/`. See `bots/example.yaml` for the template.

## Adding a New DAG

DAGs go in `dags/`. See `dags/example_hello_world.py` for the template. DAGs are auto-loaded by Airflow via volume mount.

## Code Style

- **Python**: Follow PEP 8
- **TypeScript**: ESLint + Biome
- **SQL**: Lowercase keywords, snake_case tables/columns

---

## Development Workflow

All application code runs inside Docker containers. Development is done against local builds using the override file.

### Setup

```bash
# Enable local build mode (builds from source instead of pulling GHCR images)
cp docker-compose.override.yml.example docker-compose.override.yml

# Start all services
docker compose up -d --build
```

### Iterating on Python code

After modifying any file under `slack_bot/`:

```bash
# Rebuild the affected service(s)
docker compose build worker

# Restart with the new image
docker compose up -d worker

# Tail logs to verify behavior
docker compose logs -f worker --since 2m
```

> **Important:** `docker compose restart worker` does NOT pick up code changes — you must `build` first. See [FOR-AGENTS.md](FOR-AGENTS.md) for the full operations reference.

### Iterating on the Web UI

```bash
docker compose build web-ui
docker compose up -d web-ui
```

The web UI is available at `http://localhost:3000`.

### Running database migrations

```bash
# Apply a new migration file
docker compose exec postgres psql -U customclaw -d customclaw -f /migrations/<file>.sql
```

Migration files live in `migrations/` and are applied in order. Follow the existing sequential naming convention when adding new files.

### Checking service health

```bash
docker compose ps                     # All services status
docker compose logs -f worker         # Worker logs
docker compose logs -f slack-bolt     # Slack event listener logs
docker compose exec postgres psql -U customclaw -d customclaw  # DB access
```

---

## Project Structure

```
customclaw/
├── setup.py                  # Interactive setup CLI (stdlib only, no pip needed)
├── bots/                     # Bot configuration YAML files (user-managed)
│   └── example.yaml          # Full-schema bot config example
├── dags/                     # Airflow DAG files (auto-loaded via volume mount)
├── docker/
│   ├── airflow/              # Airflow Dockerfile + entrypoint
│   ├── opensearch/           # OpenSearch image with nori plugin
│   ├── slack-bolt/           # Shared image: slack-bolt, worker
│   └── web-ui/               # Next.js image
├── migrations/               # PostgreSQL migration SQL files (applied in order)
├── slack_bot/
│   ├── app.py                # BotManager — entry point, loads bots, starts adapters
│   ├── worker.py             # Main message processing engine (Redis consumer)
│   ├── analysis_worker.py    # PR/issue analysis consumer (separate stream)
│   ├── docs_analyzer.py      # Documentation drift analysis consumer
│   ├── supervisor.py         # Process supervisor (restart-on-exit-75)
│   ├── runtime_control.py    # Redis-based restart signaling helpers
│   ├── bot_runner.py         # Slack-specific message handler
│   ├── config/
│   │   └── loader.py         # YAML → BotConfig dataclass loader
│   ├── memory/
│   │   ├── extractor.py      # Fact/decision/preference extraction via Claude haiku
│   │   ├── search.py         # HybridSearch: OpenSearch keyword + pgvector semantic
│   │   ├── store.py          # PostgreSQL + OpenSearch persistence
│   │   └── opensearch_client.py  # Index management
│   ├── platforms/            # Platform adapters (Slack, Discord, Mattermost)
│   └── tools/                # Tool implementations (GitHub, Airflow, code, bot mgmt)
├── web-ui/
│   ├── app/                  # Next.js App Router pages
│   ├── components/           # Shared UI components
│   └── prisma/               # Prisma schema (PostgreSQL)
├── .github/
│   ├── workflows/            # GitHub Actions (PR analysis, feedback, issue analysis)
│   └── ISSUE_TEMPLATE/       # Issue templates
├── docs/
│   ├── architecture.md       # Full architecture reference (Korean)
│   └── architecture.en.md    # Full architecture reference (English)
└── docker-compose.yml
```

**Key conventions:**
- `slack_bot/` is the Python application package. All new Python backend code goes here.
- `slack_bot/tools/` contains one file per tool category. Each tool class inherits from `BaseTool` (`tools/base.py`).
- `slack_bot/platforms/` contains one adapter file per messaging platform.
- `migrations/` files are append-only; never modify existing migration files.
- `bots/` and `dags/` are user-managed at runtime; do not commit user-specific bot configs.

---

## Code Style

### Python

- Follow **PEP 8** for formatting.
- Type annotations are used throughout — add them to all new functions.
- Async is used for Slack API calls (slack-sdk async client). Use `async/await` in adapter handlers.
- Worker processing is thread-based (`ThreadPoolExecutor`) for parallel analysis. Use locks when accessing shared state.
- Error handling: log exceptions with context, do not let analysis errors surface to end users. Analysis tasks are best-effort and should not crash the worker.
- Environment variable access: use `os.environ.get("VAR", default)` with sensible defaults. Never hard-code credentials.
- String formatting: f-strings are preferred over `.format()` or `%`.

### TypeScript (web-ui)

- **ESLint + Biome** are configured — run `npm run lint` before submitting.
- Next.js App Router conventions: page components in `app/`, shared components in `components/`.
- All API routes must check session via `auth()` from NextAuth before returning data.
- Prisma client is the only ORM for the web UI — do not use raw SQL in TypeScript code.

### SQL (migrations)

- Lowercase keywords: `select`, `insert`, `create table`, etc.
- snake\_case for all table and column names.
- Always include `created_at TIMESTAMPTZ DEFAULT NOW()` on new tables.
- New indexes should be `CREATE INDEX CONCURRENTLY` to avoid locking.

---

## Testing

There is currently no automated test infrastructure in this project. No test files or test runner configuration exist.

**Current expectations for contributions:**
- Verify your changes manually by running the affected service locally with `docker compose`.
- For worker changes: send a test message in Slack and confirm the response.
- For web UI changes: verify the relevant page renders correctly at `http://localhost:3000`.
- For DAG changes: trigger the DAG manually via `docker compose exec airflow airflow dags trigger <dag_id>` and verify the run succeeds in the Airflow UI at `http://localhost:8080`.
- Document any manual test steps in your PR description.

If you add test infrastructure (e.g., pytest for `slack_bot/`), a `tests/` directory at the repo root is the expected location.

---

## Bot Configuration

Bot configuration files live in `bots/`. The reference schema is `bots/example.yaml`.

**Key config sections:**

| Section | Purpose |
|---------|---------|
| `slack` | Platform tokens (app\_token for Socket Mode, bot\_token for Web API) |
| `persona` | Display name and personality injected into the LLM system prompt |
| `project` | Local repo path and GitHub repo for tool access |
| `claude` | LLM provider (`claude` or `codex`), model, max\_turns, execution mode |
| `memory` | Context window size and whether to auto-extract long-term memories |
| `tools.enabled` | List of tools the bot can use (see full list in `bots/example.yaml`) |
| `security` | Channel/user allowlists and dangerous tool confirmation |

**Environment variable references** use `${VAR_NAME}` syntax and are resolved at container startup from the `.env` file. Never store tokens directly in bot YAML files.

**Two execution modes** (`claude.full_agent`):
- `false` (default) — structured two-phase mode: worker detects tool intent, executes tools, synthesizes response. Safer, more predictable.
- `true` — Claude Code agent mode: single CLI call with unrestricted file system and shell tool access.

After adding or modifying a bot YAML:

```bash
docker compose restart slack-bolt worker
```

Changes to bot config in the web UI (PostgreSQL) take effect on next message; no restart needed.
