# CustomClaw Design Spec

## Architecture Overview

CustomClaw is a Slack bot management platform that enables creating, configuring, and monitoring AI-powered Slack bots backed by Claude CLI and Codex CLI.

```
User (Slack) → slack-bolt → Redis Stream → worker (Claude/Codex CLI)
Admin (Browser) → Next.js Web UI → PostgreSQL / Redis / Airflow API
External → Reverse proxy / tunnel → http://<your-host>:3000
```

### Core Components

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **PostgreSQL** | pgvector/pgvector:pg16 | Bot config, conversations, API costs, vector embeddings |
| **OpenSearch** | Nori analyzer plugin | Full-text Korean search over conversation logs |
| **Redis** | redis:7-alpine | Message queue (Streams), session cache |
| **Airflow** | apache/airflow:3.0.1 | Scheduled tasks (release monitoring, issue analysis) |
| **slack-bolt** | Python (slack-bolt) | Slack event listener, message routing |
| **worker** | Python + Claude/Codex CLI | LLM execution, conversation processing |
| **git-worker** | Python | Repository sync operations via Redis queue |
| **web-ui** | Next.js 16, shadcn/ui | Admin dashboard, bot management |

### Data Flow

1. Slack message arrives at slack-bolt via Slack Events API
2. slack-bolt publishes to Redis Stream with bot context
3. Worker consumes from stream, loads bot config from PostgreSQL
4. Worker invokes Claude CLI or Codex CLI with bot-specific system prompt
5. Response posted back to Slack; conversation logged to PostgreSQL + OpenSearch

## Web UI Design

**Framework:** Next.js 16 with App Router, shadcn/ui components, Tailwind CSS

**Auth:** GitHub OAuth via NextAuth.js (restrict to org members)

### Pages

| Route | Purpose |
|-------|---------|
| `/` | Dashboard — active bots, recent conversations, cost summary |
| `/bots` | Bot list with search/filter, create/edit/delete |
| `/bots/[id]` | Bot detail — config editor, conversation history, cost chart |
| `/conversations` | Global conversation log with OpenSearch full-text search |
| `/airflow` | Airflow DAG status, trigger manual runs, view logs |
| `/costs` | API cost tracking per bot, daily/monthly breakdown |
| `/settings` | System config, Slack workspace connections |

### API Routes

Server Actions and API routes under `/api/` proxy to PostgreSQL and Airflow REST API. Bot CRUD operations use server-side encryption for API keys via `ENCRYPTION_KEY`.

## Database Schema Summary

- **bots**: id, name, slack_channel, system_prompt, llm_provider, model, created_at
- **conversations**: id, bot_id, slack_thread_ts, messages (JSONB), token_count, cost
- **api_costs**: id, bot_id, provider, model, input_tokens, output_tokens, cost, date
- **users**: id, github_id, name, email, role (via NextAuth)

Vector embeddings stored via pgvector extension for future RAG retrieval.

## Deployment Strategy

All services run via a single `docker-compose.yml`. Named volumes persist data (`pgdata`, `osdata`, `redisdata`, `repos`).

**External access:** Optionally expose via reverse proxy, tunnel, or direct IP — port 3000 (web-ui) and 8080 (Airflow).

**Secrets:** Managed via `.env` file, injected as environment variables. Sensitive bot credentials encrypted at rest with `ENCRYPTION_KEY`.

## Phase Roadmap

| Phase | Status | Scope |
|-------|--------|-------|
| **Phase 1: Slack Bot Engine** | Done | slack-bolt, Redis Stream, worker, Claude/Codex CLI integration |
| **Phase 2: RAG Memory** | Done | pgvector embeddings, OpenSearch with Nori, conversation indexing |
| **Phase 3: Web UI** | In Progress | Next.js dashboard, bot CRUD, Airflow monitoring, cost tracking |
| **Phase 4: Harness Management** | Planned | Web-based agent/skill editor, oh-my-customcode integration |
