# CustomClaw

> **AI Agent?** → [FOR-AGENTS.md](FOR-AGENTS.md)를 참조하세요.

AI Slack 봇 관리 플랫폼 — 여러 AI 봇을 한 곳에서 생성·운영하며, Claude CLI 또는 Codex CLI를 LLM 프로바이더로 선택할 수 있습니다.

[English](README.en.md) | [아키텍처](docs/architecture.md)

## 주요 기능

- **멀티 봇 관리** — YAML 하나로 봇 정의, PostgreSQL에서 런타임 CRUD
- **멀티 프로바이더** — 봇별로 Claude (Anthropic) 또는 Codex (OpenAI) 선택
- **RAG 메모리** — pgvector 벡터 검색 + OpenSearch(nori) 하이브리드 검색으로 대화 맥락 유지
- **도구 호출** — GitHub 이슈 관리, Airflow DAG 조작, 코드 검색, 봇 자체 관리
- **Web UI** — Next.js 16 대시보드로 봇 CRUD, 사용량 모니터링, Airflow 연동
- **Airflow DAG** — 릴리즈 모니터링, GitHub 이슈 자동 분석
- **PR 자동 분석** — PR 생성 시 GitHub Actions → Airflow DAG → Claude CLI로 정합성 분석, Slack 스레드 알림
- **피드백 수집** — 익명 피드백 제출 → GitHub 이슈 자동 생성
- **API 사용량 추적** — Claude CLI 호출마다 토큰(input/output/cache)과 비용을 자동 기록, 대시보드에서 기간별 조회
- **문서 변경 감지** — Claude Code / Codex 공식 문서의 구조 변경을 매일 감지, 영향 분석 후 이슈 자동 생성
- **유연한 배포** — 직접 IP, 리버스 프록시, 또는 Cloudflare Tunnel로 퍼블릭 노출

---

## 기술 스택

| 카테고리 | 기술 |
|---|---|
| LLM | Claude Code CLI (Anthropic), Codex CLI (OpenAI) |
| 메시징 | Slack Bolt (Socket Mode), Redis Streams |
| 백엔드 | Python 3.12, Slack SDK |
| 벡터 DB | PostgreSQL 16 + pgvector (HNSW 인덱스, 1024차원) |
| 검색 | OpenSearch 2.x + nori 한국어 형태소 분석기 |
| 캐시/큐 | Redis 7 (Streams, AOF 영속성) |
| 워크플로우 | Apache Airflow 3.x |
| Web UI | Next.js 16, React 19, Tailwind CSS 4, shadcn/ui |
| ORM | Prisma 7 (Next.js), psycopg2 (Python) |
| 인증 | NextAuth v5, GitHub OAuth |
| 컨테이너 | Docker Compose |
| 배포 | 직접 IP / 리버스 프록시 / Cloudflare Tunnel |
| 임베딩 | Voyage AI (1024차원, 선택사항) |

---

## 빠른 시작

### Prerequisites

- Docker & Docker Compose
- [uv](https://docs.astral.sh/uv/) (권장) 또는 Python 3.12+
- Claude CLI (`~/.local/bin/claude`) — 호스트에 설치된 바이너리를 컨테이너에 마운트
- Codex CLI (`~/.codex`) — npm으로 설치 후 인증 완료 상태 (선택)

### 1. 설정 및 실행

```bash
git clone https://github.com/baekenough/customclaw.git
cd customclaw
uv run setup.py
```

대화형 CLI가 안내합니다:
1. **환경 설정** — DB, API 키, Slack 토큰, 호스트 경로 등 입력 → `.env` 생성
2. **첫 번째 봇 생성** — 봇 이름, 페르소나, LLM 프로바이더 선택 → `bots/*.yaml` 생성
3. **서비스 시작** — `docker compose up -d` 자동 실행

### 2. 확인

```bash
# 서비스 상태
docker compose ps

# 봇 로그
docker compose logs -f slack-bolt worker
```

서비스 포트:

| 서비스 | 포트 |
|---|---|
| Web UI | http://localhost:3000 |
| Airflow | http://localhost:8080 |
| PostgreSQL | localhost:5432 |

---

## 프로젝트 구조

```
customclaw/
├── setup.py                 # 대화형 설정 CLI
├── bots/                    # 봇 설정 YAML (사용자 정의)
│   └── example.yaml         # 예시 봇 설정
├── dags/                    # Airflow DAG 파일 (사용자 정의)
│   └── example_hello_world.py  # 예시 DAG
├── docker/
│   ├── airflow/             # Airflow Dockerfile + entrypoint
│   ├── opensearch/          # nori 플러그인 포함 이미지
│   ├── slack-bolt/          # Slack 봇 + Worker 이미지
│   └── web-ui/              # Next.js 이미지
├── migrations/              # PostgreSQL 마이그레이션 SQL
├── bot_engine/
│   ├── app.py               # BotManager — Socket Mode 진입점
│   ├── worker.py            # Redis Stream 컨슈머, CLI 실행
│   ├── analysis_worker.py   # 분석 전용 Redis Stream 컨슈머
│   ├── supervisor.py        # 프로세스 슈퍼바이저
│   ├── runtime_control.py   # 런타임 제어 헬퍼
│   ├── bot_runner.py        # 개별 봇 실행 로직
│   ├── config/
│   │   └── loader.py        # YAML → BotConfig 로더
│   ├── memory/
│   │   ├── extractor.py     # 대화에서 메모리 추출
│   │   ├── search.py        # pgvector + OpenSearch 하이브리드 검색
│   │   ├── store.py         # 메시지·메모리 저장
│   │   └── opensearch_client.py  # OpenSearch 인덱스 관리
│   └── tools/               # 도구 구현 (GitHub, Airflow, 봇 관리 등)
├── web-ui/                  # Next.js 16 Web UI
│   ├── app/                 # App Router 페이지
│   ├── components/          # UI 컴포넌트
│   └── prisma/              # Prisma 스키마
├── .github/
│   ├── workflows/           # GitHub Actions 워크플로우
│   │   ├── issue-analyzer.yml    # 이슈 자동 분석 트리거
│   │   ├── pr-analysis.yml       # PR 분석 트리거
│   │   └── feedback-submission.yml  # 피드백 제출
│   ├── ISSUE_TEMPLATE/      # 이슈 템플릿 (버그, 기능 요청)
│   └── PULL_REQUEST_TEMPLATE.md
└── docker-compose.yml
```

---

## 아키텍처

<p align="center"><img src="assets/diagrams/01-system-architecture.png" width="800" /></p>

1. `slack-bolt` — Slack Socket Mode로 메시지 수신, Redis Stream에 발행
2. `worker` — Redis Consumer Group으로 메시지 소비, 봇 설정에 따라 Claude CLI 또는 Codex CLI 실행
3. 메모리 — Voyage AI 임베딩 생성 → pgvector + OpenSearch에 저장·검색
4. 응답 — Slack API로 스레드에 답변 전송
5. `analysis_worker` — PR 분석 요청을 별도 Redis Stream(`customclaw:analysis-requests`)에서 소비, Claude CLI로 분석 후 Slack 알림

---

## Web UI

GitHub OAuth로 로그인 후 아래 페이지를 사용할 수 있습니다.

| 페이지 | 설명 |
|---|---|
| 대시보드 | 서비스 상태, 봇별 호출 통계, 일별 활동 추이 차트, 토큰/비용 (기간별 필터) |
| 봇 관리 | 봇 생성·수정·삭제, 설정 편집 (채널, 페르소나, LLM 프로바이더) |
| Airflow | DAG 목록 조회, 실행 이력, 수동 트리거 |
| 모니터링 | 토큰 사용량, 모델별 비용, 오류율 추적 |

---

## 봇 설정

봇은 `bots/*.yaml` 파일로 정의합니다. 컨테이너 재시작 없이 YAML 수정으로 봇을 추가할 수 있으며, Web UI를 통한 DB 기반 관리도 가능합니다.

```yaml
name: my-bot
slack:
  app_token: ${MY_BOT_SLACK_APP_TOKEN}   # xapp-...
  bot_token: ${MY_BOT_SLACK_BOT_TOKEN}   # xoxb-...
  channels: []                            # 빈 배열 = 전체 채널

persona:
  display_name: "My Bot"
  description: "팀 업무 자동화 봇"
  personality: |
    당신은 개발팀을 돕는 AI 어시스턴트입니다.
    코드 리뷰, 이슈 관리, 배포 상태 확인을 담당합니다.

project:
  repo_path: /path/to/your/repo
  github_repo: org/my-repo
  github_token_var: GITHUB_TOKEN

airflow:
  dag_prefix: my_bot_

claude:
  provider: claude        # "claude" 또는 "codex"
  model: sonnet           # claude: sonnet/opus/haiku, codex: gpt-5.4
  max_turns: 10
  full_agent: false       # true = Claude Code 에이전트 모드

memory:
  context_window: 20      # 최근 N개 메시지 컨텍스트
  auto_extract: true      # 장기 메모리 자동 추출

tools:
  enabled:
    - create_issue
    - query_issues
    - get_dag_status
    - trigger_dag
    - search_code

security:
  allowed_channels: []    # 빈 배열 = 제한 없음
  allowed_users: []       # 빈 배열 = 제한 없음
  dangerous_tools:        # 확인 프롬프트 표시
    - trigger_dag
```

### 사용 가능한 도구

| 도구 | 설명 |
|---|---|
| `create_issue` | GitHub 이슈 생성 |
| `query_issues` | GitHub 이슈 목록·검색 |
| `get_dag_status` | Airflow DAG 상태 조회 |
| `list_dags` | Airflow DAG 목록 |
| `list_dag_runs` | DAG 실행 이력 |
| `trigger_dag` | DAG 수동 실행 |
| `search_code` | 코드베이스 검색 |
| `create_bot` | 새 봇 생성 |
| `list_bots` | 봇 목록 조회 |
| `update_bot` | 봇 설정 수정 |
| `delete_bot` | 봇 삭제 |

---

## Airflow DAGs

`dags/` 디렉토리에 Airflow DAG 파일을 추가하면 자동으로 로드됩니다.

```bash
# 포함된 예시 DAG로 연결 확인
docker compose exec airflow airflow dags trigger example_hello_world
```

DAG 작성 가이드는 [Apache Airflow 문서](https://airflow.apache.org/docs/)를 참조하세요.

---

## 배포

### 옵션 1: 직접 접속 (IP)

Docker Compose를 실행하면 바로 사용할 수 있습니다.

- Web UI: `http://<서버-IP>:3000`
- Airflow: `http://<서버-IP>:8080`

`.env`에서 `NEXTAUTH_URL=http://<서버-IP>:3000`으로 설정하세요.

### 옵션 2: 리버스 프록시 (Nginx, Caddy 등)

Nginx 예시:

```nginx
server {
    listen 80;
    server_name customclaw.example.com;

    location / {
        proxy_pass http://localhost:3000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### 옵션 3: Cloudflare Tunnel

외부 포트 오픈 없이 HTTPS를 제공합니다.

```bash
cloudflared tunnel login
cloudflared tunnel create customclaw
cloudflared tunnel route dns customclaw customclaw.yourdomain.com
cloudflared tunnel run customclaw
```

### 서버 배포 순서

```bash
git pull origin develop
docker compose pull
docker compose up -d
```

### 자동 업데이트

customclaw는 Watchtower를 통해 핵심 엔진을 자동으로 업데이트합니다. 자동 업데이트를 활성화하려면 `setup.py`에서 옵션을 선택하거나, `.env`에 `COMPOSE_PROFILES=auto-update`를 추가하세요.

- **자동 모드**: Watchtower가 매일 새벽 4시에 새 이미지를 확인하고 자동 업데이트
- **수동 모드**: `docker compose pull && docker compose up -d`
- **사용자 설정(`bots/`, `.env`)은 업데이트 영향을 받지 않습니다**

#### 버전 고정

자동 업데이트 대신 특정 버전을 사용하려면:

```yaml
# docker-compose.yml에서 latest 대신 버전 태그 지정
image: ghcr.io/baekenough/customclaw-slack-bolt:v1.2.3
```

#### 개발 모드

소스 코드를 직접 수정하며 개발하려면:

```bash
cp docker-compose.override.yml.example docker-compose.override.yml
docker compose up -d --build
```

---

## 데이터베이스 스키마

| 테이블 | 설명 |
|---|---|
| `bots` | 봇 설정 (토큰은 암호화 저장) |
| `messages` | 대화 이력 + 벡터 임베딩 (HNSW 인덱스) |
| `memories` | 장기 메모리 추출본 + 벡터 임베딩 |
| `api_usage_logs` | 모델별 토큰 사용량 및 비용 추적 |

---

## 라이선스

[PolyForm Noncommercial 1.0.0](LICENSE)

개인 및 팀 내부 사용, 수정, 포크·재배포를 허용합니다. 상업적 이용은 금지됩니다.
