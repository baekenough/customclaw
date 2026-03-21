# CustomClaw Implementation Plan

## Phase 3: Web UI (In Progress)

### Infrastructure Setup

- [x] Next.js 16 project scaffolding with App Router
- [x] shadcn/ui and Tailwind CSS configuration
- [x] Docker container for web-ui service
- [x] Docker Compose integration (port 3000)
- [ ] Cloudflare Tunnel routing for web-ui

### Authentication

- [ ] NextAuth.js setup with GitHub OAuth provider
- [ ] Session management and protected routes
- [ ] Role-based access (admin vs viewer)

### Dashboard (`/`)

- [ ] Active bots summary cards
- [ ] Recent conversations feed
- [ ] Daily API cost chart (per bot)
- [ ] System health indicators (Redis, PostgreSQL, Airflow status)

### Bot Management (`/bots`)

- [ ] Bot list page with search and filter
- [ ] Create bot form (name, channel, system prompt, LLM provider/model)
- [ ] Edit bot configuration
- [ ] Delete bot with confirmation
- [ ] Bot detail page with tabbed view (config, conversations, costs)
- [ ] Server-side encryption for stored API keys

### Conversation Logs (`/conversations`)

- [ ] Global conversation list with pagination
- [ ] OpenSearch full-text search integration
- [ ] Filter by bot, date range, keyword
- [ ] Thread view with message timeline
- [ ] Token count and cost per conversation

### Airflow Monitoring (`/airflow`)

- [ ] DAG list with status indicators
- [ ] Trigger manual DAG runs via Airflow REST API
- [ ] DAG run history and logs viewer
- [ ] Fix: Add `airflow dag-processor` to entrypoint (see Known Issues)

### API Cost Tracking (`/costs`)

- [ ] Per-bot cost breakdown (input/output tokens, cost)
- [ ] Daily and monthly aggregation charts
- [ ] Cost alerts/thresholds configuration
- [ ] Export cost reports

### API Routes

- [ ] `/api/bots` — CRUD operations
- [ ] `/api/conversations` — List, search, detail
- [ ] `/api/costs` — Aggregation queries
- [ ] `/api/airflow` — Proxy to Airflow REST API (DAGs, runs, triggers)
- [ ] `/api/health` — System health check endpoint

## Phase 4: Harness Management (Planned)

### Overview

Extend the web UI to manage oh-my-customcode agents, skills, and rules directly from the browser. This bridges the gap between the Slack bot platform and the agent framework.

### Planned Features

- Agent browser: view/edit `.claude/agents/*.md` files
- Skill catalog: browse available skills with metadata
- Rule viewer: display rules with priority and status
- Agent creation wizard: guided flow using mgr-creator patterns
- Deployment: push changes to git, trigger validation via mgr-sauron

### Dependencies

- Phase 3 Web UI must be complete
- Git integration for reading/writing agent files
- Validation pipeline (mgr-sauron, mgr-supplier) accessible via API

## Known Issues

### Airflow DAGs Showing 0

**Root Cause:** The `airflow dag-processor` process is not started in the entrypoint. In Airflow 3.0, DAG file processing is a separate process from the scheduler.

**Fix:** Add `airflow dag-processor &` to `docker/airflow/entrypoint.sh` before the scheduler line.

**Impact:** No DAGs are visible in the Airflow UI or CLI until fixed. Scheduled DAG runs will not execute.

**Priority:** High — blocks Airflow monitoring page and all automated DAG execution.

### Airflow Version

Currently using Airflow 3.0.1 (released 2025-05-12). Latest stable is 3.1.8 (released 2026-03-10). Consider upgrading for bug fixes and performance improvements.
