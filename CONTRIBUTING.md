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
