"""Analysis worker — consumes issue analysis requests from Redis Stream."""

from __future__ import annotations

import logging
import os
import re
import socket
import subprocess
import threading
import time
from concurrent.futures import ThreadPoolExecutor

import redis
import requests
from slack_sdk import WebClient

log = logging.getLogger(__name__)

ANALYSIS_STREAM = "customclaw:analysis-requests"
ANALYSIS_GROUP = "analysis-workers"
CONSUMER_NAME = f"analyzer-{socket.gethostname()}"
PENDING_IDLE_MS = int(os.environ.get("ANALYSIS_PENDING_IDLE_MS", "60000"))
CLAUDE_CLI_PATH = os.environ.get("CLAUDE_CLI_PATH", "claude")
GITHUB_TOKEN = os.environ.get("GITHUB_TOKEN", "")
MAX_ANALYSIS_WORKERS = int(os.environ.get("MAX_ANALYSIS_WORKERS", "5"))
DRIVER_BOT_ID = os.environ.get("OMCUSTOM_DRIVER_BOT_ID", "omcustomdriver")
DATABASE_DSN = os.environ.get("DATABASE_DSN", "")
OPENSEARCH_URL = os.environ.get("OPENSEARCH_URL", "http://opensearch:9200")
CODEBASE_INDEX = "omc-codebase"
RAG_TOP_K = 10


def start_analysis_consumer(redis_client: redis.Redis) -> threading.Thread:
    """Start the analysis consumer in a background thread."""
    thread = threading.Thread(
        target=_consume_loop,
        args=(redis_client,),
        daemon=True,
        name="analysis-consumer",
    )
    thread.start()
    log.info("Analysis consumer started on stream %s", ANALYSIS_STREAM)
    return thread


def _process_and_ack(redis_client: redis.Redis, msg_id: bytes, request: dict) -> None:
    """Process analysis and ACK the message."""
    try:
        request_type = request.get("type", "issue_analysis")
        if request_type == "pr_analysis":
            _process_pr_analysis(request)
        else:
            _process_analysis(request)
        redis_client.xack(ANALYSIS_STREAM, ANALYSIS_GROUP, msg_id)
        log.info("ACKed analysis request %s", msg_id)
    except Exception as e:
        log.error("Failed to process analysis %s: %s", msg_id, e)


def _claim_pending_analyses(redis_client: redis.Redis, executor: ThreadPoolExecutor) -> None:
    """Re-claim idle pending analysis messages after worker restart."""
    next_start_id = "0-0"
    while True:
        try:
            next_start_id, messages, _deleted = redis_client.xautoclaim(
                ANALYSIS_STREAM,
                ANALYSIS_GROUP,
                CONSUMER_NAME,
                min_idle_time=PENDING_IDLE_MS,
                start_id=next_start_id,
                count=5,
            )
        except Exception as e:
            log.error("Failed to claim pending analyses: %s", e)
            break

        if not messages:
            break

        for msg_id, msg_data in messages:
            decoded = {
                k.decode() if isinstance(k, bytes) else k:
                v.decode() if isinstance(v, bytes) else v
                for k, v in msg_data.items()
            }
            log.info("Re-claimed pending analysis: %s", msg_id)
            executor.submit(_process_and_ack, redis_client, msg_id, decoded)

        if next_start_id == "0-0":
            break


def _consume_loop(redis_client: redis.Redis) -> None:
    """Main consumption loop for analysis requests."""
    # Ensure consumer group exists
    try:
        redis_client.xgroup_create(ANALYSIS_STREAM, ANALYSIS_GROUP, id="0", mkstream=True)
    except redis.exceptions.ResponseError as e:
        if "BUSYGROUP" not in str(e):
            raise

    executor = ThreadPoolExecutor(max_workers=MAX_ANALYSIS_WORKERS)

    # Recover any pending messages from a previous worker instance
    _claim_pending_analyses(redis_client, executor)

    while True:
        try:
            entries = redis_client.xreadgroup(
                ANALYSIS_GROUP,
                CONSUMER_NAME,
                {ANALYSIS_STREAM: ">"},
                count=5,
                block=5000,
            )
            if not entries:
                continue

            for _stream, messages in entries:
                for msg_id, msg_data in messages:
                    decoded = {
                        k.decode() if isinstance(k, bytes) else k:
                        v.decode() if isinstance(v, bytes) else v
                        for k, v in msg_data.items()
                    }
                    executor.submit(_process_and_ack, redis_client, msg_id, decoded)
        except Exception as e:
            log.error("Analysis consumer error: %s", e)
            time.sleep(5)


def _get_driver_bot_info() -> tuple[str, str]:
    """Fetch omcustom-driver bot token and channel from the database.

    Returns (bot_token, channel_id). Falls back to empty strings on error.
    """
    if not DATABASE_DSN:
        log.warning("DATABASE_DSN not set, cannot fetch driver bot info")
        return "", ""
    try:
        import psycopg2

        conn = psycopg2.connect(DATABASE_DSN)
        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT slack_bot_token, channels FROM bots WHERE id = %s AND is_active = true",
                    (DRIVER_BOT_ID,),
                )
                row = cur.fetchone()
                if not row:
                    log.warning("Driver bot '%s' not found in database", DRIVER_BOT_ID)
                    return "", ""
                token = row[0] or ""
                channels = row[1] or []
                # channels is JSONB array of channel IDs
                channel = channels[0] if channels else ""
                return token, channel
        finally:
            conn.close()
    except Exception as e:
        log.warning("Failed to fetch driver bot info: %s", e)
        return "", ""


_cached_bot_info: tuple[str, str] | None = None


def _notify_slack(
    text: str,
    issue_number: str = "",
    repo: str = "",
    thread_ts: str = "",
    emoji: str = "",
) -> str:
    """Send a notification to the Slack channel. Best-effort, never blocks.

    Returns the message timestamp (thread_ts) for threading follow-ups.
    Returns empty string on failure.
    """
    global _cached_bot_info
    if _cached_bot_info is None:
        _cached_bot_info = _get_driver_bot_info()

    bot_token, channel = _cached_bot_info
    if not bot_token or not channel:
        log.warning("Driver bot token/channel not available, skipping Slack notification")
        return ""
    try:
        client = WebClient(token=bot_token)
        issue_link = ""
        if issue_number and repo:
            issue_link = f" (<https://github.com/{repo}/issues/{issue_number}|#{issue_number}>)"

        kwargs: dict = {
            "channel": channel,
            "text": f"{text}{issue_link}",
            "unfurl_links": False,
        }
        if thread_ts:
            kwargs["thread_ts"] = thread_ts

        resp = client.chat_postMessage(**kwargs)
        ts = resp.get("ts", "") if resp.get("ok") else ""
        # Add emoji reaction if specified
        if emoji and ts:
            try:
                client.reactions_add(
                    channel=channel,
                    name=emoji,
                    timestamp=thread_ts if thread_ts else ts,
                )
            except Exception:
                pass  # Best-effort
        return ts
    except Exception as e:
        log.warning("Slack notification failed (non-blocking): %s", e)
        return ""


def _search_relevant_code(issue_title: str, issue_body: str) -> str:
    """Search OpenSearch for code relevant to the issue. Returns formatted context string."""
    try:
        from opensearchpy import OpenSearch

        client = OpenSearch(
            hosts=[OPENSEARCH_URL.replace("http://", "").replace("https://", "")],
            use_ssl=OPENSEARCH_URL.startswith("https://"),
            verify_certs=False,
            ssl_show_warn=False,
        )

        # Build search query from issue title + first 500 chars of body
        query_text = f"{issue_title} {issue_body[:500]}"

        response = client.search(
            index=CODEBASE_INDEX,
            body={
                "size": RAG_TOP_K,
                "query": {
                    "multi_match": {
                        "query": query_text,
                        "fields": ["content", "file_path^2"],
                        "type": "best_fields",
                        "fuzziness": "AUTO",
                    }
                },
                "_source": ["file_path", "content", "chunk_index"],
            },
        )

        hits = response.get("hits", {}).get("hits", [])
        if not hits:
            log.info("RAG search returned 0 results")
            return ""

        # Format results as context
        sections = []
        seen_files: set[str] = set()
        for hit in hits:
            src = hit["_source"]
            file_path = src["file_path"]
            # Deduplicate by file (keep first chunk per file)
            if file_path in seen_files:
                continue
            seen_files.add(file_path)

            # Shorten path for readability
            short_path = (
                file_path.split("/oh-my-customcode/")[-1]
                if "/oh-my-customcode/" in file_path
                else file_path
            )
            content = src["content"]
            # Truncate very long chunks
            if len(content) > 1500:
                content = content[:1500] + "\n... (truncated)"

            sections.append(f"### {short_path}\n```\n{content}\n```")

        context = "\n\n".join(sections)
        log.info(
            "RAG search returned %d relevant code snippets (%d unique files)",
            len(hits),
            len(seen_files),
        )
        return context

    except Exception as e:
        log.warning("RAG search failed (non-blocking): %s", e)
        return ""


def _process_analysis(request: dict) -> None:
    """Process a single issue analysis request.

    Pipeline:
      1. RAG search for relevant code context
      2. Architect + Colleague analyses in parallel
      3. Post architect comment, post colleague comment
      4. Professor synthesis
      5. Post professor comment
      6. Notify Slack
    """
    issue_number = request.get("issue_number", "0")
    repo = request.get("repo", os.environ.get("DEFAULT_TARGET_REPO", ""))
    repo_path = request.get("repo_path", os.environ.get("DEFAULT_REPO_PATH", ""))
    issue_title = request.get("issue_title", "")
    issue_body = request.get("issue_body", "")
    issue_labels = request.get("issue_labels", "[]")

    log.info("Processing analysis for issue #%s: %s", issue_number, issue_title[:50])

    # Phase 1: RAG — retrieve relevant code snippets for context injection
    rag_context = _search_relevant_code(issue_title, issue_body)
    rag_section = ""
    if rag_context:
        rag_section = (
            f"\n\n## 관련 코드 (RAG 검색 결과)\n"
            f"아래는 이슈와 관련될 수 있는 코드입니다. 분석의 출발점으로 활용하되, "
            f"필요하면 추가로 코드베이스를 탐색하세요.\n\n"
            f"{rag_context}"
        )

    # Phase 2: Run architect + colleague in parallel
    with ThreadPoolExecutor(max_workers=2) as inner_pool:
        architect_future = inner_pool.submit(
            _run_analysis,
            issue_number=issue_number,
            issue_title=issue_title,
            issue_body=issue_body,
            issue_labels=issue_labels,
            analysis_type="architect",
            repo_path=repo_path,
            extra_context=rag_section,
        )
        colleague_future = inner_pool.submit(
            _run_analysis,
            issue_number=issue_number,
            issue_title=issue_title,
            issue_body=issue_body,
            issue_labels=issue_labels,
            analysis_type="colleague",
            repo_path=repo_path,
            extra_context=rag_section,
        )
        architect_result = architect_future.result()
        colleague_result = colleague_future.result()

    # Post comments to GitHub (sequentially to avoid GitHub API rate issues)
    if architect_result:
        _post_github_comment(
            repo,
            issue_number,
            (
                f"## 🏛️ Senior Architect Analysis\n\n{architect_result}\n\n"
                f"---\n_Senior Architect analysis by Claude Code — omc_issue_analyzer_"
            ),
        )
    else:
        log.warning("Architect analysis returned no output for #%s", issue_number)

    if colleague_result:
        _post_github_comment(
            repo,
            issue_number,
            (
                f"## 🤝 Project Colleague Review\n\n{colleague_result}\n\n"
                f"---\n_Project Colleague review by Claude Code — omc_issue_analyzer_"
            ),
        )
    else:
        log.warning("Colleague analysis returned no output for #%s", issue_number)

    # Phase 3: Professor synthesizes both analyses
    if architect_result and colleague_result:
        log.info("Running professor synthesis for issue #%s", issue_number)
        professor_result = _run_analysis(
            issue_number=issue_number,
            issue_title=issue_title,
            issue_body=issue_body,
            issue_labels=issue_labels,
            analysis_type="professor",
            repo_path=repo_path,
            extra_context=(
                f"## Architect Analysis\n\n{architect_result}\n\n"
                f"## Colleague Review\n\n{colleague_result}"
            ),
        )
        if professor_result:
            _post_github_comment(
                repo,
                issue_number,
                (
                    f"## 🎓 Professor Synthesis\n\n{professor_result}\n\n"
                    f"---\n_Professor synthesis by Claude Code (opus) — omc_issue_analyzer_"
                ),
            )
            _add_label(repo, issue_number, "professor")
            _notify_slack(
                f"🎓 이슈 #{issue_number} 교수 종합 분석 완료",
                issue_number, repo,
                emoji="microscope",
            )
        else:
            log.warning("Professor synthesis returned no output for #%s", issue_number)

    log.info("Analysis complete for issue #%s", issue_number)


def _run_analysis(
    issue_number: str,
    issue_title: str,
    issue_body: str,
    issue_labels: str,
    analysis_type: str,
    repo_path: str,
    extra_context: str = "",
    base_branch: str = "main",
) -> str | None:
    """Run Claude CLI analysis in full_agent mode."""
    if analysis_type == "architect":
        prompt = (
            f"You are a senior software architect reviewing GitHub issue #{issue_number} "
            f"for the oh-my-customcode project. This project is a Claude Code customization "
            f"framework with agents, skills, rules, and hooks.\n\n"
            f"## Issue #{issue_number}: {issue_title}\n\n"
            f"{issue_body[:3000]}\n\n"
            f"Labels: {issue_labels}\n\n"
            f"IMPORTANT RULES FOR ACCURACY:\n"
            f"1. NEVER claim a file, function, feature, or setting exists without reading it first with Read/Glob/Grep\n"
            f"2. NEVER claim something \"doesn't exist\" or \"is missing\" without searching for it "
            f"(use Glob to search for files, Grep to search for code patterns)\n"
            f"3. When referencing line numbers, read the actual file and quote the EXACT line number — do not approximate\n"
            f"4. When describing data structures (arrays, objects, fields), enumerate each element explicitly and count them\n"
            f"5. If you're unsure whether something exists, say \"확인 필요\" instead of making a definitive claim\n"
            f"6. Cross-check your findings: if you claim a file has N fields, re-read and count them\n\n"
            f"Analyze this issue thoroughly by exploring the codebase. Respond in Korean with these sections:\n"
            f"## 아키텍처 영향 분석\n"
            f"- 프로젝트 구조(.claude/agents, skills, rules, hooks)에 미치는 영향\n"
            f"- 관련 파일/모듈 목록 (실제 코드를 탐색하여 확인)\n\n"
            f"## 코드 레벨 분석\n"
            f"- 수정이 필요한 구체적 파일과 라인\n"
            f"- 코드 스니펫 참조, 구현 방향 제안\n\n"
            f"## 전략 분석\n"
            f"- 타당성 평가 및 우선순위 권고\n"
            f"- 열린 이슈들과의 관계/의존성\n\n"
            f"## 리스크 및 고려사항\n"
            f"- Breaking changes, 하위 호환성 문제\n"
            f"- 예상 작업량 (S/M/L/XL)"
        )
        if extra_context:
            prompt += f"\n\n{extra_context}"
    elif analysis_type == "professor":
        prompt = (
            f"You are a distinguished professor and technical advisor synthesizing "
            f"two independent analyses of GitHub issue #{issue_number} for the oh-my-customcode project. "
            f"This project is a Claude Code customization framework with agents, skills, rules, and hooks.\n\n"
            f"## Issue #{issue_number}: {issue_title}\n\n"
            f"{issue_body[:2000]}\n\n"
            f"Labels: {issue_labels}\n\n"
            f"Below are two independent analyses from a Senior Architect and a Project Colleague:\n\n"
            f"{extra_context}\n\n"
            f"CRITICAL INSTRUCTIONS:\n"
            f"1. Do NOT take the Architect or Colleague's code references at face value\n"
            f"2. INDEPENDENTLY verify the key factual claims from both analyses by reading the actual code\n"
            f"3. When the two analyses disagree about code state (e.g., \"exists\" vs \"doesn't exist\"), "
            f"resolve the disagreement by checking yourself\n\n"
            f"Synthesize both analyses into a definitive assessment. Respond in Korean with these sections:\n"
            f"## 코드베이스 검증\n"
            f"- 두 분석에서 언급된 핵심 사실(파일 존재 여부, 함수/설정 존재, 라인 번호)을 직접 코드를 읽어 검증\n"
            f"- 검증 결과를 ✅ (정확) / ❌ (오류) / ⚠️ (부분 정확)으로 표시\n"
            f"- 오류가 발견된 경우, 정확한 정보로 교정\n\n"
            f"## 종합 판단\n"
            f"- 두 분석의 합의점과 차이점\n"
            f"- 최종 권고안 (구체적인 action plan)\n\n"
            f"## 우선순위 매트릭스\n"
            f"| 작업 | 긴급도 | 중요도 | 예상 규모 | 순서 |\n"
            f"|------|--------|--------|----------|------|\n"
            f"(각 작업을 테이블로 정리)\n\n"
            f"## 놓친 관점\n"
            f"- 두 분석 모두에서 빠진 고려사항\n"
            f"- 코드베이스를 탐색하여 추가로 확인한 사항\n\n"
            f"## 실행 로드맵\n"
            f"- Phase별 구체적 실행 계획\n"
            f"- 각 Phase의 완료 기준(Definition of Done)\n"
            f"- 의존성과 병렬 처리 가능 여부"
        )
    elif analysis_type == "pr_architect":
        pr_number = issue_number  # reuse parameter — PR number passed as issue_number
        repo = issue_labels  # reuse parameter — repo passed as issue_labels
        issue_analysis_context = extra_context
        prompt = (
            f"You are a senior software architect reviewing PR #{pr_number} "
            f"for the oh-my-customcode project.\n"
            f"This project is a Claude Code customization framework with agents (.claude/agents/), "
            f"skills (.claude/skills/), rules (.claude/rules/), and hooks (.claude/hooks/).\n\n"
            f"## PR #{pr_number}: {issue_title}\n\n"
            f"{issue_body[:3000]}\n\n"
            f"## Linked Issue Analysis (if available)\n"
            f"{issue_analysis_context}\n\n"
            f"## Instructions\n"
            f"1. Run `gh pr diff {pr_number} --repo {repo}` to see the actual code changes\n"
            f"2. Read CLAUDE.md and relevant rules to understand project philosophy\n"
            f"3. Analyze alignment in Korean with these sections:\n\n"
            f"## 프로젝트 철학 정합성\n"
            f"- oh-my-customcode의 핵심 철학(에이전트 설계, 관심사 분리, 스킬/규칙 구조)과 얼마나 일치하는가\n"
            f"- CLAUDE.md 규칙(R006, R007, R008, R009, R010 등)을 준수하는가\n\n"
            f"## 이슈 분석 대비 구현 검증\n"
            f"- 이슈 분석(Architect/Colleague/Professor)에서 권고한 사항이 실제로 구현되었는가\n"
            f"- 빠진 권고사항이나 분석과 다른 방향의 구현이 있는가\n\n"
            f"## 구조적 영향\n"
            f"- 프로젝트 구조(.claude/agents, skills, rules, hooks)에 미치는 영향\n"
            f"- 기존 패턴과의 일관성\n\n"
            f"## 리스크 및 권고\n"
            f"- Breaking changes, 하위 호환성 문제\n"
            f"- 개선 제안사항"
        )
    elif analysis_type == "pr_colleague":
        pr_number = issue_number
        repo = issue_labels
        issue_analysis_context = extra_context
        prompt = (
            f"You are an experienced project collaborator reviewing PR #{pr_number} "
            f"for the oh-my-customcode project.\n\n"
            f"## PR #{pr_number}: {issue_title}\n\n"
            f"{issue_body[:3000]}\n\n"
            f"## Linked Issue Analysis (if available)\n"
            f"{issue_analysis_context}\n\n"
            f"## Instructions\n"
            f"1. Run `gh pr diff {pr_number} --repo {repo}` to see the code changes\n"
            f"2. Explore the codebase to understand context\n"
            f"3. Provide feedback in Korean:\n\n"
            f"## 이슈 요구사항 충족도\n"
            f"- 원래 이슈에서 요구한 사항이 모두 구현되었는가\n"
            f"- 이슈 분석에서 제안한 action items가 반영되었는가\n\n"
            f"## 코드 품질\n"
            f"- 코드 컨벤션 일관성\n"
            f"- 엣지 케이스 처리\n"
            f"- 테스트 커버리지\n\n"
            f"## 놓친 부분\n"
            f"- 이슈 분석에서 언급했으나 PR에서 빠진 사항\n"
            f"- 다른 이슈/기능과의 시너지 또는 충돌\n\n"
            f"## 실용적 제안\n"
            f"- 즉시 개선 가능한 사항\n"
            f"- 후속 작업으로 남길 사항"
        )
    elif analysis_type == "pr_professor":
        pr_number = issue_number
        issue_analysis_context = extra_context
        prompt = (
            f"You are a distinguished professor synthesizing two independent PR reviews "
            f"for PR #{pr_number}.\n\n"
            f"## PR #{pr_number}: {issue_title}\n"
            f"{issue_body[:2000]}\n\n"
            f"## Linked Issue Analysis\n"
            f"{issue_analysis_context}\n\n"
            f"Synthesize in Korean:\n\n"
            f"## 종합 정합성 판정\n"
            f"- PR이 이슈 분석 결과와 프로젝트 철학에 얼마나 일치하는가\n"
            f"- 정합성 점수 (A/B/C/D/F) 및 근거\n\n"
            f"## 주요 발견사항\n"
            f"- 두 리뷰의 합의점과 차이점\n"
            f"- 이슈 분석 대비 구현의 갭 분석\n\n"
            f"## 필수 수정사항\n"
            f"- PR 머지 전 반드시 수정해야 할 사항 (있다면)\n\n"
            f"## 권고사항\n"
            f"- 개선하면 좋을 사항 (선택적)\n"
            f"- 후속 이슈로 추적할 사항"
        )
    else:
        # Default: colleague analysis
        prompt = (
            f"You are a friendly and experienced project collaborator reviewing GitHub issue #{issue_number} "
            f"for the oh-my-customcode project. This project is a Claude Code customization "
            f"framework with agents, skills, rules, and hooks.\n\n"
            f"## Issue #{issue_number}: {issue_title}\n\n"
            f"{issue_body[:3000]}\n\n"
            f"Labels: {issue_labels}\n\n"
            f"IMPORTANT RULES FOR ACCURACY:\n"
            f"1. Before suggesting '이런 방식은 어떨까요?', verify the current state — "
            f"the feature might already be implemented\n"
            f"2. NEVER assume a feature is missing without checking. Search with Glob and Grep first\n"
            f"3. When referencing existing code patterns, quote the actual file path and line number "
            f"after reading the file\n"
            f"4. If you reference a variable, function, or config field, verify it exists by reading the source\n"
            f"5. Distinguish clearly between '확인 결과 없음' (verified absent) vs '탐색하지 않음' (not checked)\n\n"
            f"Provide constructive, collaborative feedback by exploring the codebase. "
            f"Respond in Korean with these sections:\n"
            f"## 첫인상\n"
            f"- 이 이슈의 핵심 가치와 동기\n\n"
            f"## 구현 아이디어\n"
            f"- '이런 방식은 어떨까요?' 스타일의 제안\n"
            f"- 관련 코드/패턴을 참조하며 구체적으로\n\n"
            f"## 놓치기 쉬운 부분\n"
            f"- 엣지 케이스, 사이드 이펙트\n"
            f"- 다른 이슈/기능과의 시너지\n\n"
            f"## 다음 단계 제안\n"
            f"- 구체적인 action items\n"
            f"- '이거 먼저 하면 좋을 것 같아요' 식의 우선순위"
        )
        if extra_context:
            prompt += f"\n\n{extra_context}"

    # PR analysis types require Bash (for gh pr diff); professor needs Bash for independent verification
    bash_types = {"pr_architect", "pr_colleague", "pr_professor", "professor"}
    allowed_tools = (
        "Read,Glob,Grep,Bash" if analysis_type in bash_types else "Read,Glob,Grep"
    )

    model = "opus"
    max_turns = "30"

    try:
        # Use repo_path if it exists, else fall back to /tmp
        cwd = repo_path if os.path.isdir(repo_path) else "/tmp"

        # Checkout the correct base branch before analysis to avoid stale code issues
        # Sanitize base_branch: strip whitespace, fall back to "main" if empty
        base_branch = base_branch.strip() if base_branch else "main"
        if not base_branch:
            base_branch = "main"
        try:
            subprocess.run(
                ["git", "-C", cwd, "fetch", "origin", base_branch],
                capture_output=True,
                timeout=30,
            )
            subprocess.run(
                ["git", "-C", cwd, "checkout", f"origin/{base_branch}"],
                capture_output=True,
                timeout=30,
            )
        except Exception:
            log.warning(
                "Best-effort git checkout failed for #%s (base=%s), "
                "continuing with current working tree",
                issue_number,
                base_branch,
            )

        env = {
            **os.environ,
            "HOME": os.environ.get("CONTAINER_HOME", "/home/appuser"),
            "NO_COLOR": "1",
        }
        args = [
            CLAUDE_CLI_PATH, "-p", prompt,
            "--model", model,
            "--max-turns", str(max_turns),
            "--allowedTools", allowed_tools,
        ]

        result = subprocess.run(
            args,
            env=env,
            capture_output=True,
            text=True,
            timeout=600,  # 10 min max
            stdin=subprocess.DEVNULL,
            cwd=cwd,
        )

        if result.returncode != 0:
            log.error(
                "Claude CLI error for #%s (%s): exit %d, stderr: %s",
                issue_number,
                analysis_type,
                result.returncode,
                result.stderr[:500],
            )
            return None

        output = result.stdout.strip()
        if not output:
            log.warning(
                "Claude CLI returned empty output for #%s (%s)",
                issue_number,
                analysis_type,
            )
            return None

        return output

    except subprocess.TimeoutExpired:
        log.error("Claude CLI timed out for #%s (%s)", issue_number, analysis_type)
        return None
    except Exception as e:
        log.error("Analysis error for #%s (%s): %s", issue_number, analysis_type, e)
        return None


def _post_github_comment(repo: str, issue_number: str, body: str) -> bool:
    """Post a comment on a GitHub issue or PR.

    Deletes any pre-existing comment with the same footer signature before
    posting so that re-triggered analyses replace rather than duplicate.
    """
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
    headers = {
        "Authorization": f"Bearer {GITHUB_TOKEN}",
        "Accept": "application/vnd.github+json",
    }

    # Delete existing analysis comments with same footer pattern
    footer = body.split("---\n_")[-1] if "---\n_" in body else ""
    try:
        existing = requests.get(url, headers=headers, timeout=10).json()
        for comment in existing:
            comment_body = comment.get("body", "")
            if footer and footer in comment_body:
                requests.delete(
                    f"https://api.github.com/repos/{repo}/issues/comments/{comment['id']}",
                    headers=headers,
                    timeout=10,
                )
                log.info("Deleted old analysis comment %s", comment["id"])
            elif "Analysis could not be completed" in comment_body:
                requests.delete(
                    f"https://api.github.com/repos/{repo}/issues/comments/{comment['id']}",
                    headers=headers,
                    timeout=10,
                )
                log.info("Deleted failed analysis comment %s", comment["id"])
    except Exception as e:
        log.warning("Failed to clean up old comments: %s", e)

    try:
        resp = requests.post(url, json={"body": body}, headers=headers, timeout=10)
        resp.raise_for_status()
        log.info("Posted comment on issue #%s", issue_number)
        return True
    except Exception as e:
        log.error("Failed to post comment on #%s: %s", issue_number, e)
        return False


def _add_label(repo: str, issue_number: str, label: str) -> None:
    """Add a label to a GitHub issue or PR."""
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/labels"
    token = os.environ.get("GITHUB_TOKEN", "")
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    try:
        resp = requests.post(
            url, json={"labels": [label]}, headers=headers, timeout=30,
        )
        resp.raise_for_status()
        log.info("Added label '%s' to %s#%s", label, repo, issue_number)
    except requests.exceptions.RequestException as exc:
        log.warning("Failed to add label '%s' to %s#%s: %s", label, repo, issue_number, exc)


def _extract_linked_issue(pr_title: str, pr_body: str) -> str | None:
    """Extract linked issue number from PR title/body.

    Looks for patterns like: #123, fixes #123, closes #456, resolves #789.
    Returns the issue number as a string, or None if not found.
    """
    # Keyword-prefixed patterns take priority (e.g. "fixes #123", "closes #456")
    keyword_pattern = re.compile(
        r"(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+#(\d+)",
        re.IGNORECASE,
    )
    for text in (pr_title, pr_body):
        match = keyword_pattern.search(text)
        if match:
            return match.group(1)

    # Fall back to bare #N reference
    bare_pattern = re.compile(r"#(\d+)")
    for text in (pr_title, pr_body):
        match = bare_pattern.search(text)
        if match:
            return match.group(1)

    return None


def _fetch_issue_analysis(repo: str, issue_number: str) -> dict:
    """Fetch existing issue analysis comments from GitHub.

    Returns a dict with keys: architect, colleague, professor
    (each str | None). Identifies comments by their emoji headers.
    """
    result: dict[str, str | None] = {
        "architect": None,
        "colleague": None,
        "professor": None,
    }

    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
    headers = {
        "Authorization": f"Bearer {GITHUB_TOKEN}",
        "Accept": "application/vnd.github+json",
    }

    try:
        resp = requests.get(url, headers=headers, timeout=10)
        resp.raise_for_status()
        for comment in resp.json():
            body = comment.get("body", "")
            if "## 🏛️ Senior Architect Analysis" in body:
                content = body.split("## 🏛️ Senior Architect Analysis\n\n", 1)[-1]
                result["architect"] = content.split("\n\n---\n_")[0]
            elif "## 🤝 Project Colleague Review" in body:
                content = body.split("## 🤝 Project Colleague Review\n\n", 1)[-1]
                result["colleague"] = content.split("\n\n---\n_")[0]
            elif "## 🎓 Professor Synthesis" in body:
                content = body.split("## 🎓 Professor Synthesis\n\n", 1)[-1]
                result["professor"] = content.split("\n\n---\n_")[0]
    except Exception as e:
        log.warning(
            "Failed to fetch issue analysis for #%s from %s: %s",
            issue_number, repo, e,
        )

    return result


def _process_pr_analysis(request: dict) -> None:
    """Process a PR analysis request — alignment check against issue analysis + project philosophy.

    Pipeline:
      1. Extract PR metadata
      2. Find linked issue and fetch its analysis comments
      3. RAG search for relevant code context
      4. Architect + Colleague PR analyses in parallel
      5. Post architect PR comment, post colleague PR comment
      6. Professor PR synthesis
      7. Post professor PR comment
      8. Notify Slack
    """
    pr_number = request.get("pr_number", "0")
    pr_title = request.get("pr_title", "")
    pr_body = request.get("pr_body", "")
    repo = request.get("repo", os.environ.get("DEFAULT_TARGET_REPO", ""))
    repo_path = request.get("repo_path", os.environ.get("DEFAULT_REPO_PATH", ""))
    pr_base = request.get("pr_base", "main")

    log.info("Processing PR analysis for PR #%s: %s", pr_number, pr_title[:50])

    start_ts = _notify_slack(
        f"🔍 PR #{pr_number} 정합성 분석 시작",
        issue_number=pr_number,
        repo=repo,
    )

    # Step 2: Find linked issue and fetch its analysis
    issue_analysis_context = ""
    linked_issue = _extract_linked_issue(pr_title, pr_body)
    if linked_issue:
        log.info("PR #%s linked to issue #%s — fetching analysis", pr_number, linked_issue)
        issue_analysis = _fetch_issue_analysis(repo, linked_issue)
        parts = []
        if issue_analysis.get("architect"):
            parts.append(f"### Architect Analysis\n{issue_analysis['architect']}")
        if issue_analysis.get("colleague"):
            parts.append(f"### Colleague Review\n{issue_analysis['colleague']}")
        if issue_analysis.get("professor"):
            parts.append(f"### Professor Synthesis\n{issue_analysis['professor']}")
        if parts:
            issue_analysis_context = (
                f"Issue #{linked_issue} was analyzed. Key findings:\n\n"
                + "\n\n".join(parts)
            )
    else:
        log.info("No linked issue found for PR #%s", pr_number)

    # Step 3: RAG search using PR title + body
    rag_context = _search_relevant_code(pr_title, pr_body)
    rag_section = ""
    if rag_context:
        rag_section = (
            f"\n\n## 관련 코드 (RAG 검색 결과)\n"
            f"아래는 PR과 관련될 수 있는 코드입니다. 분석의 출발점으로 활용하되, "
            f"필요하면 추가로 코드베이스를 탐색하세요.\n\n"
            f"{rag_context}"
        )

    # Combined context: issue analysis + RAG
    combined_context = issue_analysis_context
    if rag_section:
        combined_context = f"{issue_analysis_context}\n{rag_section}" if combined_context else rag_section

    # Step 4: Run architect + colleague PR analyses in parallel
    # We repurpose _run_analysis parameters:
    #   issue_number → pr_number
    #   issue_labels → repo  (needed for gh pr diff inside the prompt)
    #   extra_context → combined issue analysis + RAG context
    with ThreadPoolExecutor(max_workers=2) as inner_pool:
        architect_future = inner_pool.submit(
            _run_analysis,
            issue_number=pr_number,
            issue_title=pr_title,
            issue_body=pr_body,
            issue_labels=repo,
            analysis_type="pr_architect",
            repo_path=repo_path,
            extra_context=combined_context,
            base_branch=pr_base,
        )
        colleague_future = inner_pool.submit(
            _run_analysis,
            issue_number=pr_number,
            issue_title=pr_title,
            issue_body=pr_body,
            issue_labels=repo,
            analysis_type="pr_colleague",
            repo_path=repo_path,
            extra_context=combined_context,
            base_branch=pr_base,
        )
        architect_result = architect_future.result()
        colleague_result = colleague_future.result()

    # Step 5: Post architect and colleague comments on the PR
    if architect_result:
        _post_github_comment(
            repo,
            pr_number,
            (
                f"## 🏛️ Senior Architect Analysis\n\n{architect_result}\n\n"
                f"---\n_Senior Architect PR review by Claude Code — omc_pr_analyzer_"
            ),
        )
    else:
        log.warning("PR architect analysis returned no output for PR #%s", pr_number)

    if colleague_result:
        _post_github_comment(
            repo,
            pr_number,
            (
                f"## 🤝 Project Colleague Review\n\n{colleague_result}\n\n"
                f"---\n_Project Colleague PR review by Claude Code — omc_pr_analyzer_"
            ),
        )
    else:
        log.warning("PR colleague analysis returned no output for PR #%s", pr_number)

    # Step 6: Professor synthesizes both PR reviews
    _notify_slack(
        f"📝 PR #{pr_number} Architect + Colleague 분석 완료, Professor 종합 중...",
        issue_number=pr_number,
        repo=repo,
        thread_ts=start_ts,
    )

    if architect_result and colleague_result:
        log.info("Running professor PR synthesis for PR #%s", pr_number)
        professor_context = (
            f"{issue_analysis_context}\n\n"
            f"## Architect Review\n{architect_result}\n\n"
            f"## Colleague Review\n{colleague_result}"
        )
        professor_result = _run_analysis(
            issue_number=pr_number,
            issue_title=pr_title,
            issue_body=pr_body,
            issue_labels=repo,
            analysis_type="pr_professor",
            repo_path=repo_path,
            extra_context=professor_context,
            base_branch=pr_base,
        )
        if professor_result:
            _post_github_comment(
                repo,
                pr_number,
                (
                    f"## 🎓 Professor Synthesis\n\n{professor_result}\n\n"
                    f"---\n_Professor PR synthesis by Claude Code (opus) — omc_pr_analyzer_"
                ),
            )
            _notify_slack(
                f"🎓 PR #{pr_number} 교수 종합 분석 완료",
                issue_number=pr_number,
                repo=repo,
                thread_ts=start_ts,
                emoji="white_check_mark",
            )
        else:
            log.warning("Professor PR synthesis returned no output for PR #%s", pr_number)

    log.info("PR analysis complete for PR #%s", pr_number)
