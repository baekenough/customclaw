# CustomClaw 아키텍처 문서

> [English](architecture.en.md)

## 1. 시스템 개요

CustomClaw는 멀티플랫폼 멀티봇 AI 플랫폼으로, 여러 봇을 하나의 인프라에서 운영하면서 각 봇이 독립적인 퍼소나·프로젝트 컨텍스트·도구 세트를 가질 수 있도록 설계되어 있습니다. Slack, Mattermost, Discord 플랫폼 어댑터를 지원합니다. 사용자 메시지는 Socket Mode(Slack) 또는 플랫폼별 어댑터를 통해 수신되고, Redis Stream을 거쳐 Worker로 비동기 전달됩니다. Worker는 Claude CLI(Anthropic) 또는 Codex CLI(OpenAI)를 subprocess로 호출해 응답을 생성하며, 대화 이력은 PostgreSQL에, 장기 기억은 PostgreSQL(pgvector 임베딩) + OpenSearch(한국어 nori 키워드 검색)의 하이브리드 구조로 저장됩니다. Airflow는 GitHub 이슈 분석·릴리즈 모니터링·공식 문서 변경 감지 등 자동화 DAG 9개를 담당하고, Next.js 기반 Web UI가 봇 관리와 모니터링 기능을 제공합니다.

---

## 2. 아키텍처 다이어그램

### 2.1 전체 시스템 아키텍처

<p align="center"><img src="../assets/diagrams/01-system-architecture.png" width="800" /></p>

### 2.2 메시지 처리 흐름 (Sequence)

<p align="center"><img src="../assets/diagrams/02-message-sequence.png" width="800" /></p>

---

## 3. 컴포넌트 상세

### 3.1 플랫폼 어댑터 (app.py / bot_runner.py)

`BotManager`는 `/app/bots/*.yaml` 파일을 로드하고, `platform` 필드에 따라 적절한 어댑터를 초기화합니다.

**Slack 어댑터**: 각 봇에 대해 `BotRunner` 인스턴스를 생성한 뒤 별도 스레드에서 Slack Socket Mode Handler를 시작합니다.
- `message` 이벤트를 수신하고 `subtype`이 있는 메시지(봇 메시지, 수정 이벤트 등)는 무시합니다.
- `security.allowed_channels` / `security.allowed_users` 필터를 적용합니다.
- 검증을 통과한 메시지를 `customclaw:slack-messages` Redis Stream에 `xadd`합니다.
- 처리 중 ⏳ 리액션을 추가해 사용자에게 진행 상황을 알립니다.

**Discord 어댑터** (`platforms/discord_adapter.py`): `discord.py` 라이브러리 기반. `DiscordConfig`의 `token`으로 연결하고, `guild_id`로 서버를 제한합니다. `security.mention_only: true` 설정 시 봇 멘션이 포함된 메시지만 처리합니다.

**Mattermost 어댑터**: Mattermost WebSocket API 기반 어댑터.

<p align="center"><img src="../assets/diagrams/03-worker-consumer.png" width="800" /></p>

### 3.2 Worker (worker.py)

Redis Consumer Group(`customclaw-workers`) 방식으로 메시지를 소비합니다. 여러 Worker 컨테이너를 병렬로 실행하면 자동으로 부하가 분산됩니다.

**2-Phase Prompt 방식 (limited mode):**
1. **Phase 1**: 시스템 프롬프트 + 기억 컨텍스트 + 도구 설명 + 대화 이력 + 사용자 메시지를 전달. Claude는 `json tool_call` 블록을 포함할 수 있습니다.
2. **Phase 2**: tool_call 감지 시 해당 도구를 실행하고, 원본 메시지 + 도구 결과를 Claude에 다시 전달해 최종 응답을 생성합니다.

**full_agent mode**: Claude CLI에 `--dangerously-skip-permissions`를 적용하고 Claude 자체의 내장 도구(파일 조작, Bash 등)를 모두 활성화합니다. tool_call JSON 파싱 없이 단일 프롬프트로 동작합니다.

**Codex provider**: `codex exec --dangerously-bypass-approvals-and-sandbox`로 실행. 응답 메타데이터 헤더/푸터를 파싱해 순수 콘텐츠만 추출합니다.

| 설정 | 설명 |
|------|------|
| `claude.provider` | `claude` (기본) 또는 `codex` |
| `claude.model` | `opus` / `sonnet` / `haiku` (또는 `gpt-5.4`) |
| `claude.max_turns` | CLI 최대 턴 수 (기본 10) |
| `claude.full_agent` | full_agent 모드 활성화 |
| `memory.context_window` | 대화 이력 불러올 메시지 수 (기본 20) |

### 3.3 메모리 시스템

<p align="center"><img src="../assets/diagrams/04-memory-extraction.png" width="800" /></p>

**현재 구현 상태:**
- OpenSearch nori 키워드 검색은 완전 구현되어 있습니다.
- pgvector 시맨틱 검색은 Voyage API 키 확보 후 활성화할 예정입니다 (현재 zero-vector 플레이스홀더).
- 추출은 대화 4개 메시지 이상 누적 시 Claude haiku(`--max-turns 1`)로 자동 실행됩니다.
- RRF(Reciprocal Rank Fusion)로 두 검색 결과를 병합하는 코드는 TODO 상태입니다.

### 3.4 도구 시스템 (tools/)

<p align="center"><img src="../assets/diagrams/05-tool-class.png" width="800" /></p>

| 카테고리 | 도구 | 설명 |
|----------|------|------|
| GitHub | `create_issue`, `query_issues` | 이슈 생성·조회 (봇 설정의 `github_repo` + `github_token_var` 사용) |
| Airflow | `get_dag_status`, `list_dag_runs`, `list_dags`, `trigger_dag` | DAG 상태 조회·트리거 |
| 코드 검색 | `search_code` | 로컬 리포지토리 Claude CLI 검색 (`repo_path` 기반) |
| 봇 관리 | `create_bot`, `list_bots`, `update_bot`, `delete_bot` | 런타임 봇 CRUD |
| 런타임 제어 | `restart_runtime` | worker / app / all 안전 재시작 요청 (`target`: worker\|app\|all) |

총 12개 도구. 봇별 `tools.enabled` 설정으로 허용할 도구 집합을 제한할 수 있습니다.

### 3.5 봇 설정 (config/loader.py)

봇 설정은 YAML 파일(`/app/bots/*.yaml`) 또는 PostgreSQL `bots` 테이블에서 로드됩니다. 환경 변수 레퍼런스(`${ENV_VAR}` 형식)는 자동으로 확장됩니다. `platform` 필드로 `slack` / `mattermost` / `discord` 중 하나를 지정합니다.

<p align="center"><img src="../assets/diagrams/06-bot-config.png" width="800" /></p>

### 3.6 Web UI (Next.js 16)

<p align="center"><img src="../assets/diagrams/07-web-ui.png" width="800" /></p>

- **인증**: NextAuth v5 + GitHub OAuth Provider. `PrismaAdapter`로 세션을 PostgreSQL에 저장합니다.
- **Airflow 프록시**: 매 요청마다 `/auth/token` 엔드포인트에서 JWT를 발급받아 `Authorization: Bearer` 헤더로 Airflow API를 호출합니다.
- **헬스 체크**: PostgreSQL, Redis, OpenSearch, Airflow 4개 서비스를 병렬(`Promise.allSettled`)로 확인합니다. 하나라도 실패하면 HTTP 503을 반환합니다.

### 3.7 Airflow DAGs

<p align="center"><img src="../assets/diagrams/08-dag-pipeline.png" width="800" /></p>

| DAG | 트리거 | 역할 |
|-----|--------|------|
| `omc_issue_analyzer` | GitHub Actions webhook (SSH) / issue-comment-reeval.yml | GitHub 이슈를 4단계 파이프라인으로 분석: ① 아키텍트 + 동료 병렬 분석 → ② Professor 합성 → ③ Professor 판단(auto-dev/needs-input) → ④ omcustom-driver 자동 개발 |
| `claude_code_release_monitor` | 스케줄러 | anthropics/claude-code 새 릴리즈 감지 → oh-my-customcode에 트래킹 이슈 자동 생성 |
| `gpt_codex_release_monitor` | 스케줄러 | openai/codex 새 릴리즈 감지 → oh-my-customcode에 추적 이슈 생성, worker 컨테이너 Codex CLI 자동 업데이트 |
| `omc_codebase_indexer` | 스케줄러 | oh-my-customcode 코드베이스를 OpenSearch에 인덱싱 (RAG 검색용) |
| `omc_feedback_collector` | Airflow REST API / CLI | 사용자 피드백 수신, 검증 후 GitHub 이슈 생성 (익명 제출 지원) |
| `omc_pr_analyzer` | GitHub Actions 웹훅 (SSH) | PR 분석 요청을 Redis Stream에 발행, 분석 워커가 Claude CLI로 분석 수행. Redis 분산 락으로 중복 분석 방지 |
| `agentnav_issue_analyzer` | docs_drift_monitor 트리거 / GitHub Actions webhook | docs-drift 이슈에서 변경된 문서 소스를 식별하고 소스별 Redis Stream에 분석 요청 발행 → claude/codex/gemini analyzer 컨테이너가 소비 |
| `docs_drift_monitor` | `0 */3 * * *` (3시간 주기) | Claude Code·Codex·Gemini CLI 공식 문서 변경 감지 → GitHub docs-drift 이슈 생성 → agentnav_issue_analyzer 트리거 |
| `example_hello_world` | 수동 / 스케줄러 | Airflow 동작 확인용 예제 DAG |

### 3.8 omcustom-driver (자동 개발 에이전트)

Professor가 `auto-dev` 판단을 내린 이슈에 한해 활성화되는 자동 개발 파이프라인입니다.

**동작 흐름:**

1. **Professor 게이트**: Professor가 `auto-dev` 라벨을 승인한 경우에만 실행
2. **진행 상황 공유**: GitHub 이슈 코멘트로 실시간 진행 상태 게시
3. **질문 처리**: 추가 정보가 필요한 경우 `needs-input` 라벨 부착 후 이슈 코멘트로 질문
4. **자동 개발**: 브랜치 생성 → 코드 구현 → 커밋 → 푸시 → PR 생성
5. **재평가**: 사용자가 `needs-input` 이슈에 답변하면 `issue-comment-reeval.yml` GitHub Actions가 분석을 재트리거

**worker 컨테이너 요구사항:**

omcustom-driver는 git 및 gh CLI가 필요합니다. worker 컨테이너 이미지에 두 도구가 포함되어 있습니다.

| 라벨 | 의미 |
|------|------|
| `auto-dev` | Professor가 자동 개발 승인 — omcustom-driver 활성화 |
| `needs-input` | 추가 정보 필요 — 사용자 답변 대기 중 |

**GitHub Actions 연동:**

- `oh-my-customcode` 저장소의 `issue-comment-reeval.yml`이 `needs-input` 라벨이 붙은 이슈에 새 코멘트가 달리면 SSH를 통해 `omc_issue_analyzer` DAG를 재트리거합니다.
- Analysis consumer는 xautoclaim 복구 기능을 갖추고 있어 재시작 시 메시지 유실을 방지합니다.

### 3.9 Analysis Worker (analysis_worker.py)

PR 분석 및 이슈 분석 요청을 처리하는 별도의 Redis Stream 컨슈머입니다.

- **Redis Stream**: `customclaw:analysis-requests` / Consumer Group: `analysis-workers`
- **기능**: Claude CLI를 subprocess로 호출하여 PR 정합성 분석 수행
- **Slack 알림**: 분석 시작 → 진행 → 완료 메시지를 스레드로 묶어 전송 (thread_ts 기반)
- **중복 방지**: Redis `SET NX EX` 분산 락으로 동일 PR 15분 내 중복 분석 차단
- **복구**: `xautoclaim`으로 미확인 메시지 자동 복구

### 3.10 프로세스 관리 (supervisor.py / runtime_control.py)

`supervisor.py`는 worker/app 프로세스의 관리형 재시작을 지원하는 프로세스 슈퍼바이저입니다. `runtime_control.py`는 Redis 기반 안전한 자체 재시작 요청 헬퍼를 제공합니다.

- 재시작 요청은 Redis 키(`customclaw:control:restart`)를 통해 전달
- 종료 코드 75로 프로세스 종료 시 슈퍼바이저가 자동 재시작
- `CUSTOMCLAW_SUPERVISED` 환경 변수로 슈퍼바이저 모드 감지

### 3.11 GitHub Actions 워크플로우 (workflows/)

| 워크플로우 | 파일 | 설명 |
|-----------|------|------|
| PR Analysis | `workflows/pr-analysis.yml` | PR 이벤트(opened, synchronize, ready_for_review) 발생 시 SSH로 서버에 접속, `omc_pr_analyzer` DAG 트리거 |
| Feedback Submission | `workflows/feedback-submission.yml` | 수동 `workflow_dispatch`로 피드백 제출. SSH로 `omc_feedback_collector` DAG 트리거 |

---

## 4. 데이터 흐름

### 전체 메시지 처리 흐름

```
1. [수신] Slack 사용자 메시지
      → BotRunner: 채널·사용자 권한 검증
      → Redis Stream xadd (customclaw:slack-messages)

2. [큐잉] Redis Stream
      → Consumer Group: customclaw-workers
      → Worker 인스턴스 중 하나가 xreadgroup으로 할당 수신

3. [전처리] Worker
      → PostgreSQL: 사용자 메시지 저장 (messages 테이블)
      → PostgreSQL: 최근 대화 이력 조회 (context_window)
      → OpenSearch: 관련 기억 검색 (top_k=5, nori 키워드)

4. [AI 처리] Claude CLI / Codex CLI
      → limited mode:
           Phase 1: 프롬프트 → Claude → tool_call 감지
           (tool 호출 시) Phase 2: tool_result → Claude → 최종 응답
      → full_agent mode: 단일 프롬프트 → Claude (내장 도구 활용)
      → codex provider: codex exec → 메타데이터 파싱 → 순수 응답

5. [응답] Slack API
      → chat_postMessage (thread 내 응답)
      → PostgreSQL: assistant 메시지 저장

6. [사후 처리] 비동기 메모리 추출
      → 대화 ≥ 4개 메시지 시 Claude haiku 호출
      → fact/decision/preference 추출 (JSON)
      → PostgreSQL memories 테이블 저장
      → OpenSearch customclaw-memories 인덱스 저장
      → Redis xack (메시지 확인 처리)
```

---

## 5. 데이터베이스 스키마

<p align="center"><img src="../assets/diagrams/09-er-diagram.png" width="800" /></p>

### 테이블별 설명

| 테이블 | 목적 | 주요 인덱스 |
|--------|------|------------|
| `messages` | 모든 대화 메시지 저장. thread 또는 채널 레벨 컨텍스트 조회 지원 | `(bot_id, channel_id, thread_ts)`, HNSW cosine 임베딩 |
| `memories` | 자동 추출된 장기 기억. pgvector로 시맨틱 유사도 검색 지원 (Voyage API 활성화 후) | `(bot_id)`, HNSW cosine 임베딩 |
| `bots` | 봇 설정 원장. Web UI에서 CRUD. slack-bolt는 YAML을 우선 사용 | Primary Key(id) |
| `api_usage_logs` | LLM API 호출 비용 추적 | `(bot_id, created_at)` |

**OpenSearch 인덱스 (`customclaw-memories`):**
- `content` 필드: nori 커스텀 analyzer (nori_tokenizer + nori_readingform + lowercase)
- `bot_id`, `category`, `user_id`: keyword 타입 (정확 매칭 필터)

---

## 6. 인프라

### Docker Compose 서비스 맵

<p align="center"><img src="../assets/diagrams/10-docker-services.png" width="800" /></p>

### 서비스 의존 관계

<p align="center"><img src="../assets/diagrams/11-service-dependency.png" width="800" /></p>

### 주요 볼륨 마운트

| 볼륨/경로 | 서비스 | 목적 |
|-----------|--------|------|
| `./bots:/app/bots` | slack-bolt, worker | 봇 YAML 설정 파일 실시간 반영 |
| `repos:${CONTAINER_HOME}/workspace` | slack-bolt, worker, airflow | Git 리포지토리 공유 |
| `${CLAUDE_CONFIG_DIR}:${CLAUDE_CONFIG_DIR}` | worker | Claude CLI 설정·인증 |
| `${CLAUDE_CREDENTIALS_FILE}` | worker | Claude CLI 자격증명 |
| `${CLAUDE_CLI_BINARY}:/usr/local/bin/claude:ro` | worker | Claude CLI 바이너리 |
| `${CODEX_CONFIG_DIR}:${CODEX_CONFIG_DIR}` | worker | Codex CLI 설정·인증 |
| `./dags:/opt/airflow/dags` | airflow | DAG 파일 핫 리로드 |
| `./migrations:/docker-entrypoint-initdb.d` | postgres | 초기화 SQL 자동 실행 |

---

## 7. 배포 아키텍처

<p align="center"><img src="../assets/diagrams/12-deployment.png" width="800" /></p>

### 포트 매핑

| 서비스 | 호스트 포트 | 컨테이너 포트 | 외부 노출 |
|--------|------------|--------------|----------|
| postgres | 5432 | 5432 | 아니오 (내부 전용) |
| opensearch | - | 9200 | 아니오 (내부 전용) |
| redis | - | 6379 | 아니오 (내부 전용) |
| airflow | 8080 | 8080 | 아니오 (내부 + SSH) |
| web-ui | 3000 | 3000 | Cloudflare Tunnel 경유 |

### 환경 변수 요약

| 변수 | 서비스 | 설명 |
|------|--------|------|
| `DATABASE_DSN` | slack-bolt, worker | PostgreSQL 연결 문자열 |
| `REDIS_URL` | 전체 | Redis 연결 URL |
| `OPENSEARCH_URL` | worker | OpenSearch 엔드포인트 |
| `OPENSEARCH_ADMIN_PASSWORD` | opensearch | OpenSearch 초기 관리자 비밀번호 |
| `ANTHROPIC_API_KEY` | worker | Claude API 인증 |
| `VOYAGE_API_KEY` | worker | 임베딩 API (미래 pgvector 활성화용) |
| `ENCRYPTION_KEY` | slack-bolt, worker | 토큰 암호화 키 |
| `GITHUB_TOKEN` | worker, airflow | GitHub API 인증 |
| `AIRFLOW_API_URL` | worker, web-ui | Airflow REST API 엔드포인트 |
| `NEXTAUTH_URL` | web-ui | NextAuth 콜백 기본 URL |
| `GITHUB_CLIENT_ID/SECRET` | web-ui | GitHub OAuth 앱 자격증명 |

---

## 8. 주요 설계 결정 및 트레이드오프

| 결정 | 이유 | 트레이드오프 |
|------|------|------------|
| Redis Stream (Consumer Group) | 멀티 Worker 수평 확장, at-least-once 처리 보장 | xack 전 장애 시 메시지 재처리 가능성 |
| Claude CLI subprocess | API SDK 없이 최신 Claude CLI 기능(full_agent, tools) 바로 활용 | subprocess 오버헤드, 프로세스 관리 복잡도 |
| 2-Phase Prompt (limited mode) | JSON tool_call 파싱으로 도구 호출 제어 | Phase 2 추가 LLM 호출 비용 |
| pgvector + OpenSearch 하이브리드 | 시맨틱(벡터) + 키워드(nori) 검색 상호 보완 | OpenSearch 메모리 부담, 동기화 복잡도 |
| YAML 봇 설정 (우선) + DB (보조) | 파일 기반 빠른 배포, DB로 Web UI 관리 지원 | 두 소스 간 동기화 일관성 주의 필요 |
| Codex provider | Claude 대비 GPT-5.4 모델 옵션 추가 | 메타데이터 파싱 추가 구현 필요 |
| 멀티플랫폼 어댑터 (Slack / Discord / Mattermost) | 단일 인프라에서 플랫폼별 봇 운영, Discord `mention_only` 등 플랫폼 특화 옵션 지원 | 플랫폼별 어댑터 유지보수 필요 |
| 전용 analyzer 컨테이너 (claude/codex/gemini) | docs drift 분석을 CLI별 독립 컨테이너로 분리, Redis Stream 기반 비동기 처리 | 컨테이너 수 증가, CLI별 자격증명 볼륨 마운트 필요 |
