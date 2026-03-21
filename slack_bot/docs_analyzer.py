"""Documentation drift analyzer worker.

Consumes documentation drift analysis requests from a dedicated Redis Stream
and runs analysis using a configured CLI tool (Claude, Codex, or Gemini).

Configuration via environment variables:
    ANALYZER_CLI:      CLI to use — "claude", "codex", or "gemini" (default: "claude")
    ANALYZER_STREAM:   Redis stream to consume from (default: "customclaw:{cli}-analysis")
    ANALYZER_GROUP:    Consumer group name (default: "{cli}-analyzers")
    REDIS_URL:         Redis connection URL (default: "redis://redis:6379")
    GITHUB_TOKEN:      GitHub token for posting issue comments
    SLACK_BOT_TOKEN:   Slack bot token for notifications (optional)
    SLACK_CHANNEL:     Slack channel ID for #agentnav (optional)

Usage:
    python -m slack_bot.docs_analyzer
"""

from __future__ import annotations

import logging
import os
import socket
import subprocess
import time

import redis
import requests

log = logging.getLogger(__name__)

PENDING_IDLE_MS = int(os.environ.get("ANALYZER_PENDING_IDLE_MS", "60000"))
GITHUB_TOKEN = os.environ.get("GITHUB_TOKEN", "")
_HOME_DIR = os.environ.get("HOME", "/home/appuser")

# Model info displayed in GitHub comment per CLI
_CLI_MODEL_INFO: dict[str, str] = {
    "claude": "Claude Opus 4",
    "codex": "o3",
    "gemini": "Gemini 3.0 Pro",
}

_AGENTNAV_DOC_URLS: dict[str, str] = {
    "claude-code": "https://agentnav.baekenough.com/claude-code/agents.md",
    "codex": "https://agentnav.baekenough.com/gpt-codex/agents.md",
    "gemini-cli": "https://agentnav.baekenough.com/gemini-cli/agents.md",
}


def _fetch_agentnav_doc(source_name: str) -> str:
    """Fetch the AgentNav agents.md document for the given source.

    Returns the document content, or an error message on failure.
    """
    url = _AGENTNAV_DOC_URLS.get(source_name, "")
    if not url:
        return f"(AgentNav document URL not configured for {source_name})"
    try:
        resp = requests.get(url, timeout=30)
        resp.raise_for_status()
        content = resp.text
        # Truncate to avoid exceeding prompt limits
        if len(content) > 15000:
            content = content[:15000] + "\n\n... (truncated, full doc at " + url + ")"
        return content
    except Exception as exc:
        log.warning("Failed to fetch AgentNav doc for %s: %s", source_name, exc)
        return f"(Failed to fetch AgentNav document: {exc})"


# ---------------------------------------------------------------------------
# CLI execution
# ---------------------------------------------------------------------------


def _build_command(cli: str) -> list[str]:
    """Build the command args for the given CLI (prompt will be piped via stdin).

    Args:
        cli: One of "claude", "codex", or "gemini".

    Returns:
        A list of command-line arguments.
    """
    if cli == "claude":
        return ["claude", "--model", "sonnet", "--max-turns", "5"]
    if cli == "codex":
        return ["codex", "exec", "--full-auto", "--skip-git-repo-check", "-m", "o3", "-c", "reasoning.effort=\"high\"", "-c", "reasoning.summary=\"auto\""]
    if cli == "gemini":
        return ["gemini", "--model", "gemini-3-pro-preview", "--yolo"]
    raise ValueError(f"Unknown CLI: {cli!r}")


def _run_cli(cli: str, prompt: str) -> str | None:
    """Run the configured CLI with the given prompt via stdin pipe.

    Args:
        cli: CLI identifier ("claude", "codex", or "gemini").
        prompt: The analysis prompt to execute.

    Returns:
        Stripped stdout on success, or ``None`` on failure/timeout/empty output.
    """
    try:
        # Copy gemini config to writable /tmp (container HOME may be read-only)
        if cli == "gemini":
            import shutil
            src = os.path.join(_HOME_DIR, ".gemini")
            dst = "/tmp/.gemini"
            if os.path.isdir(src):
                if os.path.isdir(dst):
                    shutil.rmtree(dst)
                shutil.copytree(src, dst)
            else:
                os.makedirs(dst, exist_ok=True)

        import tempfile

        cmd_args = _build_command(cli)
        log.info(
            "Running %r: prompt_len=%d, first_80=%r",
            cli, len(prompt), prompt[:80],
        )
        env = os.environ.copy()
        if cli == "gemini":
            env["HOME"] = "/tmp"
        else:
            env["HOME"] = _HOME_DIR
        env["NO_COLOR"] = "1"

        # Pass prompt directly as -p argument (no shell, no stdin)
        if cli == "claude":
            full_cmd = cmd_args + ["-p", prompt]
        elif cli == "codex":
            full_cmd = cmd_args + [prompt]
        else:  # gemini
            full_cmd = cmd_args + ["-p", prompt]

        result = subprocess.run(
            full_cmd,
            capture_output=True,
            text=True,
            timeout=600,
            stdin=subprocess.DEVNULL,
            env=env,
        )

        if result.returncode != 0:
            log.error(
                "CLI %r exited with %d. stderr: %s stdout: %s",
                cli,
                result.returncode,
                result.stderr[:500],
                result.stdout[:500],
            )
            return None

        output = result.stdout.strip()
        if not output:
            log.warning("CLI %r returned empty output", cli)
            return None

        return output

    except subprocess.TimeoutExpired:
        log.error("CLI %r timed out after 600 s", cli)
        return None
    except Exception as exc:
        log.error("CLI %r raised an unexpected error: %s", cli, exc)
        return None


# ---------------------------------------------------------------------------
# Prompt construction
# ---------------------------------------------------------------------------


def _build_analysis_prompt(
    source_name: str,
    added_lines: str,
    removed_lines: str,
    issue_url: str,
    agentnav_doc: str = "",
) -> str:
    """Return a Korean-language deep comparison prompt.

    Compares official documentation changes against AgentNav's current
    parsed document to identify gaps, mismatches, and required updates.

    Args:
        source_name: Documentation source identifier (e.g. "claude-code",
            "codex", "gemini-cli").
        added_lines: Diff lines added to the documentation.
        removed_lines: Diff lines removed from the documentation.
        issue_url: URL of the GitHub issue tracking this drift.
        agentnav_doc: Current AgentNav agents.md content for comparison.

    Returns:
        A fully formed prompt string ready for CLI execution.
    """
    return (
        f"당신은 AgentNav 문서 품질 관리 전문가입니다.\n\n"
        f"AgentNav는 AI 코딩 도구들(Claude Code, GPT Codex, Gemini CLI)의 공식 문서를 "
        f"구조화된 agents.txt 형식으로 파싱하여 제공하는 서비스입니다.\n\n"
        f"## 작업: {source_name} 문서 동기화 분석\n\n"
        f"공식 문서(`{source_name}`)에서 변경이 감지되었습니다. "
        f"AgentNav이 제공하는 파싱 문서와 비교하여 동기화 상태를 분석해주세요.\n\n"
        f"---\n\n"
        f"## 데이터 형식 안내\n\n"
        f"아래 diff 데이터는 llms.txt에서 추출되었으며, 여러 유형의 항목이 혼재합니다:\n"
        f"- `## 헤딩` — 문서 **섹션 헤더** (개별 페이지가 아님)\n"
        f"- `- [제목](경로): 설명` — 실제 **개별 페이지 항목**\n"
        f"- 코드 스니펫, 설정값, 파일 경로 등 — 기존 페이지 내부 **콘텐츠 변경**\n\n"
        f"**중요**: 섹션 헤더와 콘텐츠 조각을 독립 페이지로 오인하지 마세요. "
        f"AgentNav agents.md의 `- [title](path)` 형식과 1:1 매칭하여 분석하세요.\n\n"
        f"AgentNav agents.md에는 문서의 URL, 총 페이지 수, 섹션 구조가 명시되어 있습니다. "
        f"공식 문서의 소스 URL과 AgentNav이 추적하는 URL이 동일한지도 반드시 확인하세요.\n\n"
        f"---\n\n"
        f"## 1. 공식 문서 변경사항 (llms.txt diff)\n\n"
        f"### 추가된 항목\n<added>\n{added_lines or '(없음)'}\n</added>\n\n"
        f"### 제거된 항목\n<removed>\n{removed_lines or '(없음)'}\n</removed>\n\n"
        f"---\n\n"
        f"## 2. AgentNav 현재 제공 문서\n\n"
        f"아래는 AgentNav이 현재 `{source_name}`에 대해 제공하는 agents.md 문서입니다:\n\n"
        f"<agentnav_document>\n{agentnav_doc or '(문서를 가져올 수 없음)'}\n</agentnav_document>\n\n"
        f"---\n\n"
        f"## 분석 요청\n\n"
        f"위 두 자료를 1:1 비교하여 다음을 한국어로 분석해주세요:\n\n"
        f"### 1. 구조적 검증\n"
        f"- AgentNav agents.md의 소스 URL과 diff의 출처 URL이 일치하는가?\n"
        f"- 불일치하면 근본 원인을 먼저 보고하세요 (잘못된 소스 추적 등)\n\n"
        f"### 2. 동기화 상태 진단\n"
        f"- 공식 문서에 추가된 **페이지** 중 AgentNav에 아직 없는 것 (섹션 헤더, 콘텐츠 조각 제외)\n"
        f"- 공식 문서에서 제거된 **페이지** 중 AgentNav에 아직 남아있는 것\n"
        f"- 경로/제목이 변경된 것 중 AgentNav이 구버전을 참조하는 것\n\n"
        f"### 3. 영향도 평가\n"
        f"- 잘못된 정보를 제공할 위험이 있는 항목 (높은 우선순위)\n"
        f"- 단순 추가가 필요한 항목 (낮은 우선순위)\n\n"
        f"### 4. 구체적 액션 아이템\n"
        f"각 항목을 테이블로 정리:\n"
        f"| # | 유형 | 항목 | 현재 상태 | 필요한 조치 | 우선순위 |\n"
        f"|---|------|------|----------|-----------|----------|\n\n"
        f"### 5. 전체 요약\n"
        f"- 동기화율: AgentNav이 공식 문서의 몇 %를 반영하고 있는가 (페이지 단위 기준)\n"
        f"- 전체 우선순위: **높음** / **중간** / **낮음** + 근거\n\n"
        f"참고 이슈: {issue_url}"
    )


# ---------------------------------------------------------------------------
# GitHub comment
# ---------------------------------------------------------------------------


def _post_github_comment(repo: str, issue_number: str, body: str) -> bool:
    """Post a comment on a GitHub issue.

    Attempts to delete any pre-existing comment with the same footer
    signature before posting so re-triggered analyses replace rather than
    duplicate.

    Args:
        repo: Repository in ``owner/name`` format.
        issue_number: Issue number as a string.
        body: Markdown comment body.

    Returns:
        ``True`` on success, ``False`` on failure.
    """
    if not GITHUB_TOKEN:
        log.warning("GITHUB_TOKEN not set; skipping GitHub comment")
        return False

    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
    headers = {
        "Authorization": f"Bearer {GITHUB_TOKEN}",
        "Accept": "application/vnd.github+json",
    }

    # Delete any existing comment that shares the same footer signature
    footer = body.split("---\n_")[-1] if "---\n_" in body else ""
    try:
        existing = requests.get(url, headers=headers, timeout=10).json()
        if isinstance(existing, list):
            for comment in existing:
                comment_body = comment.get("body", "")
                if footer and footer in comment_body:
                    requests.delete(
                        f"https://api.github.com/repos/{repo}/issues/comments/{comment['id']}",
                        headers=headers,
                        timeout=10,
                    )
                    log.info("Deleted old drift analysis comment %s", comment["id"])
    except Exception as exc:
        log.warning("Failed to clean up old comments: %s", exc)

    try:
        resp = requests.post(url, json={"body": body}, headers=headers, timeout=10)
        resp.raise_for_status()
        log.info("Posted drift analysis comment on %s#%s", repo, issue_number)
        return True
    except Exception as exc:
        log.error("Failed to post comment on %s#%s: %s", repo, issue_number, exc)
        return False


def _build_comment_body(
    source_name: str,
    cli: str,
    analysis_content: str,
) -> str:
    """Format the GitHub comment body.

    Args:
        source_name: Documentation source name (e.g. "claude-code").
        cli: CLI identifier used for this analysis.
        analysis_content: The raw output from the CLI.

    Returns:
        Formatted Markdown string.
    """
    model_info = _CLI_MODEL_INFO.get(cli, cli)
    footer_tag = f"docs_drift_monitor — Analyzer: {cli}"
    return (
        f"## 🤖 {source_name} Documentation Drift Analysis\n\n"
        f"**Analyzer**: {cli} ({model_info})\n"
        f"**Source**: {source_name}\n\n"
        f"{analysis_content}\n\n"
        f"---\n"
        f"_{footer_tag}_"
    )


# ---------------------------------------------------------------------------
# Slack notification
# ---------------------------------------------------------------------------


def _notify_slack(text: str, thread_ts: str = "") -> str:
    """Send a best-effort notification to the configured Slack channel.

    Returns the message timestamp for threading follow-up messages.
    Returns empty string on failure or when not configured.

    Args:
        text: Plain text message body.
        thread_ts: If provided, reply in this thread instead of top-level.
    """
    bot_token = os.environ.get("SLACK_BOT_TOKEN", "")
    channel = os.environ.get("SLACK_CHANNEL", "")
    if not bot_token or not channel:
        return ""

    try:
        from slack_sdk import WebClient  # noqa: PLC0415 — optional dependency

        client = WebClient(token=bot_token)
        kwargs: dict = {
            "channel": channel,
            "text": text,
            "unfurl_links": False,
        }
        if thread_ts:
            kwargs["thread_ts"] = thread_ts

        resp = client.chat_postMessage(**kwargs)
        return resp.get("ts", "") if resp.get("ok") else ""
    except Exception as exc:
        log.warning("Slack notification failed (non-blocking): %s", exc)
        return ""


# ---------------------------------------------------------------------------
# Message processing
# ---------------------------------------------------------------------------


def _decode_message(msg_data: dict) -> dict[str, str]:
    """Decode raw Redis message bytes to a plain string dict.

    Args:
        msg_data: Raw key/value dict from Redis, values may be bytes.

    Returns:
        Dict with all keys and values decoded to ``str``.
    """
    return {
        (k.decode() if isinstance(k, bytes) else k): (
            v.decode() if isinstance(v, bytes) else v
        )
        for k, v in msg_data.items()
    }


def _process_message(cli: str, request: dict) -> None:
    """Process a single drift analysis request end-to-end.

    Steps:
        1. Extract fields from the request.
        2. Notify Slack that analysis has started.
        3. Build prompt and run the CLI.
        4. Post result as a GitHub issue comment.
        5. Notify Slack that analysis is complete.

    Errors are logged but never re-raised so the caller always ACKs.

    Args:
        cli: CLI identifier ("claude", "codex", or "gemini").
        request: Decoded Redis message fields.
    """
    issue_number = request.get("issue_number", "0")
    repo = request.get("repo", "")
    source_name = request.get("source_name", cli)
    added_lines = request.get("added_lines", "")
    removed_lines = request.get("removed_lines", "")
    issue_url = request.get("issue_url", "")
    agentnav_doc = request.get("agentnav_doc", "")

    log.info(
        "Processing drift analysis: source=%s issue=%s repo=%s",
        source_name,
        issue_number,
        repo,
    )

    # Step 1: Start notification
    thread_ts = _notify_slack(
        f"🔍 [{source_name}] 문서 변경 분석 시작 — Issue #{issue_number}\n"
        f"Analyzer: {cli}"
    )

    # Step 2: Build prompt and run CLI
    prompt = _build_analysis_prompt(
        source_name, added_lines, removed_lines, issue_url, agentnav_doc
    )
    analysis_content = _run_cli(cli, prompt)

    if analysis_content is None:
        log.error(
            "CLI %r returned no output for %s#%s; skipping comment",
            cli,
            repo,
            issue_number,
        )
        _notify_slack(
            f"❌ [{source_name}] 분석 실패 — Issue #{issue_number}\n"
            f"Analyzer: {cli}",
            thread_ts=thread_ts,
        )
        return

    # Step 3: Post GitHub comment
    if repo and issue_number:
        comment_body = _build_comment_body(source_name, cli, analysis_content)
        _post_github_comment(repo, issue_number, comment_body)
    else:
        log.warning("repo or issue_number missing; skipping GitHub comment")

    # Step 4: Completion notification
    _notify_slack(
        f"✅ [{source_name}] 문서 변경 분석 완료 — Issue #{issue_number}",
        thread_ts=thread_ts,
    )

    log.info(
        "Drift analysis complete: source=%s issue=%s",
        source_name,
        issue_number,
    )


def _process_and_ack(
    redis_client: redis.Redis,
    stream: str,
    group: str,
    cli: str,
    msg_id: bytes,
    msg_data: dict,
) -> None:
    """Process a message and unconditionally ACK it.

    The message is ACK'd even on processing failure to prevent infinite
    redelivery of messages that cannot be handled (e.g. malformed payloads).

    Args:
        redis_client: Connected Redis client.
        stream: Stream name for ACK.
        group: Consumer group name for ACK.
        cli: CLI identifier.
        msg_id: Redis message ID.
        msg_data: Raw message data dict (bytes or str values).
    """
    request = _decode_message(msg_data)
    try:
        _process_message(cli, request)
    except Exception as exc:
        log.error("Unhandled error processing message %s: %s", msg_id, exc)
    finally:
        try:
            redis_client.xack(stream, group, msg_id)
            log.debug("ACKed message %s", msg_id)
        except Exception as exc:
            log.error("Failed to ACK message %s: %s", msg_id, exc)


# ---------------------------------------------------------------------------
# Consumer loop
# ---------------------------------------------------------------------------


def _claim_pending(
    redis_client: redis.Redis,
    stream: str,
    group: str,
    consumer_name: str,
    cli: str,
) -> None:
    """Re-claim and process idle pending messages from a previous consumer.

    Iterates over all pending messages whose idle time exceeds
    ``PENDING_IDLE_MS`` and processes them synchronously.

    Args:
        redis_client: Connected Redis client.
        stream: Stream name.
        group: Consumer group name.
        consumer_name: This consumer's unique identifier.
        cli: CLI identifier.
    """
    next_start_id = "0-0"
    while True:
        try:
            next_start_id, messages, _deleted = redis_client.xautoclaim(
                stream,
                group,
                consumer_name,
                min_idle_time=PENDING_IDLE_MS,
                start_id=next_start_id,
                count=5,
            )
        except Exception as exc:
            log.error("Failed to claim pending messages: %s", exc)
            break

        if not messages:
            break

        for msg_id, msg_data in messages:
            log.info("Re-claimed pending message: %s", msg_id)
            _process_and_ack(redis_client, stream, group, cli, msg_id, msg_data)

        if next_start_id == "0-0":
            break


def _consume_loop(
    redis_client: redis.Redis,
    stream: str,
    group: str,
    consumer_name: str,
    cli: str,
) -> None:
    """Main blocking consumer loop.

    Ensures the consumer group exists, recovers pending messages, then polls
    the stream indefinitely.

    Args:
        redis_client: Connected Redis client.
        stream: Stream name to consume from.
        group: Consumer group name.
        consumer_name: Unique name for this consumer instance.
        cli: CLI identifier passed to each message processor.
    """
    # Ensure consumer group exists
    try:
        redis_client.xgroup_create(stream, group, id="0", mkstream=True)
        log.info("Created consumer group %r on stream %r", group, stream)
    except redis.exceptions.ResponseError as exc:
        if "BUSYGROUP" not in str(exc):
            raise

    # Recover pending messages from a previous instance
    _claim_pending(redis_client, stream, group, consumer_name, cli)

    log.info(
        "Starting consume loop: stream=%r group=%r consumer=%r cli=%r",
        stream,
        group,
        consumer_name,
        cli,
    )

    while True:
        try:
            entries = redis_client.xreadgroup(
                group,
                consumer_name,
                {stream: ">"},
                count=5,
                block=5000,
            )
            if not entries:
                continue

            for _stream_name, messages in entries:
                for msg_id, msg_data in messages:
                    _process_and_ack(
                        redis_client, stream, group, cli, msg_id, msg_data
                    )

        except Exception as exc:
            log.error("Consumer loop error: %s", exc)
            time.sleep(5)


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


def main() -> None:
    """Start the documentation drift analyzer worker.

    Reads configuration from environment variables, creates a Redis client,
    and runs the blocking consumer loop.
    """
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    cli = os.environ.get("ANALYZER_CLI", "claude")
    if cli not in {"claude", "codex", "gemini"}:
        log.error(
            "Invalid ANALYZER_CLI=%r. Must be 'claude', 'codex', or 'gemini'.", cli
        )
        raise SystemExit(1)

    stream = os.environ.get("ANALYZER_STREAM", f"customclaw:{cli}-analysis")
    group = os.environ.get("ANALYZER_GROUP", f"{cli}-analyzers")
    redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
    consumer_name = f"docs-analyzer-{cli}-{socket.gethostname()}"

    log.info(
        "Docs analyzer starting: cli=%r stream=%r group=%r consumer=%r",
        cli,
        stream,
        group,
        consumer_name,
    )

    redis_client = redis.from_url(redis_url, decode_responses=False)
    _consume_loop(redis_client, stream, group, consumer_name, cli)


if __name__ == "__main__":
    main()
