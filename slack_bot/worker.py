"""Worker — consumes from Redis Stream and processes messages via Claude CLI."""

from __future__ import annotations

import collections
import concurrent.futures
import json
import logging
import os
import re
import socket
import sys
import subprocess
import tempfile
import threading
import time

import redis
import requests

from slack_bot.config.loader import load_all_bots, BotConfig
from slack_bot.platforms.base import ResponsePublisher
from slack_bot.platforms.slack_adapter import SlackResponsePublisher
from slack_bot.memory.extractor import MemoryExtractor
from slack_bot.memory.search import HybridSearch
from slack_bot.memory.store import MessageStore
from slack_bot.runtime_control import (
    consume_restart_request,
    is_supervised_runtime,
)
from slack_bot.tools.base import ToolRegistry
from slack_bot.tools.github_tools import CreateIssueTool, QueryIssuesTool
from slack_bot.tools.airflow_tools import GetDagStatusTool, ListDagRunsTool, ListDagsTool, TriggerDagTool
from slack_bot.tools.code_tools import SearchCodeTool
from slack_bot.tools.bot_management_tools import (
    CreateBotTool,
    DeleteBotTool,
    ListBotsTool,
    RestartRuntimeTool,
    UpdateBotTool,
)
from slack_bot.analysis_worker import start_analysis_consumer

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
log = logging.getLogger(__name__)

STREAM_KEY = "customclaw:slack-messages"
CONSUMER_GROUP = os.environ.get("REDIS_CONSUMER_GROUP", "customclaw-workers")

# Module-level cache for response publishers (keyed by "platform:bot_id").
# Avoids creating a new HTTP session + login call per message.
_publisher_cache: dict[str, ResponsePublisher] = {}
CONSUMER_NAME = os.environ.get(
    "REDIS_CONSUMER_NAME", f"worker-{socket.gethostname()}"
)
CLAUDE_CLI_PATH = os.environ.get("CLAUDE_CLI_PATH", "claude")
CODEX_CLI_PATH = os.environ.get("CODEX_CLI_PATH", "codex")
PENDING_IDLE_MS = int(os.environ.get("REDIS_PENDING_IDLE_MS", "30000"))
MAX_CONCURRENT = int(os.environ.get("MAX_CONCURRENT_WORKERS", "10"))
MERGE_WINDOW = int(os.environ.get("MERGE_WINDOW_SECONDS", "30"))


def _get_publisher(msg_data: dict, bots: dict) -> ResponsePublisher:
    """Return a cached ResponsePublisher for the message's platform and bot.

    For Mattermost, the publisher performs a ``driver.login()`` on first
    creation.  Caching avoids repeating that HTTP round-trip for every
    message.
    """
    platform = msg_data.get("platform", "slack")
    bot_id = msg_data.get("bot_id", "")
    cache_key = f"{platform}:{bot_id}"

    if cache_key in _publisher_cache:
        return _publisher_cache[cache_key]

    if platform == "mattermost":
        from slack_bot.platforms.mattermost_adapter import MattermostResponsePublisher  # noqa: PLC0415
        config = bots.get(bot_id)
        if config and hasattr(config, "mattermost"):
            publisher: ResponsePublisher = MattermostResponsePublisher(
                token=config.mattermost.token,
                url=config.mattermost.url,
                port=config.mattermost.port,
            )
        else:
            publisher = MattermostResponsePublisher(
                token=msg_data.get("bot_token", ""),
                url=msg_data.get("platform_url", ""),
            )
    elif platform == "discord":
        from slack_bot.platforms.discord_adapter import DiscordResponsePublisher  # noqa: PLC0415
        publisher = DiscordResponsePublisher(bot_token=msg_data.get("bot_token", ""))
    else:
        publisher = SlackResponsePublisher(bot_token=msg_data.get("bot_token", ""))

    _publisher_cache[cache_key] = publisher
    return publisher


class AgentExecutionError(RuntimeError):
    """Raised when the configured LLM CLI fails to produce a usable response."""

    def __init__(self, provider: str, reason: str = "") -> None:
        self.provider = (provider or "agent").lower()
        self.reason = reason
        super().__init__(f"{self.provider} execution failed: {reason or 'unknown_error'}")


def _provider_label(provider: str) -> str:
    provider_name = (provider or "claude").lower()
    if provider_name == "codex":
        return "Codex CLI"
    return "Claude CLI"


def _user_visible_error_message(error: Exception) -> str:
    if isinstance(error, AgentExecutionError):
        return "응답 생성 중 오류가 발생했습니다. 잠시 후 다시 시도해 주세요."
    return f"처리 중 오류가 발생했습니다: {error}"


def _require_response(output: str | None, provider: str) -> str:
    if output:
        return output
    raise AgentExecutionError(provider, "no_output")


def _parse_claude_json_output(raw: str) -> tuple[str, dict | None]:
    """Parse Claude CLI ``--output-format json`` output.

    Returns ``(text, usage_dict)`` where *text* is the human-readable result
    and *usage_dict* contains token/cost metadata (or ``None`` on parse
    failure).  If *raw* is not valid JSON the entire string is returned as
    plain text with ``None`` usage — this keeps backward compatibility when
    the CLI is invoked without ``--output-format json``.
    """
    try:
        data = json.loads(raw)
    except (json.JSONDecodeError, TypeError):
        return raw, None

    if not isinstance(data, dict) or "result" not in data:
        return raw, None

    text = data.get("result", "")

    usage_info: dict = {}
    if data.get("usage"):
        usage_info["input_tokens"] = data["usage"].get("input_tokens", 0)
        usage_info["output_tokens"] = data["usage"].get("output_tokens", 0)
        usage_info["cache_read_tokens"] = data["usage"].get("cache_read_input_tokens", 0)
        usage_info["cache_creation_tokens"] = data["usage"].get("cache_creation_input_tokens", 0)
    if data.get("total_cost_usd") is not None:
        usage_info["cost_usd"] = data["total_cost_usd"]
    if data.get("modelUsage"):
        # Pick the first (usually only) model key for the model name
        usage_info["model"] = next(iter(data["modelUsage"]), None)

    return text, usage_info or None


def _log_usage(
    bot_id: str,
    user_id: str | None,
    model: str,
    input_tokens: int,
    output_tokens: int,
    cost_usd: float | None,
    tool_name: str | None = None,
    cache_read_tokens: int = 0,
    cache_creation_tokens: int = 0,
) -> None:
    """Log API usage to the ``api_usage_logs`` PostgreSQL table.

    Silently skips if ``DATABASE_DSN`` is not configured or the insert fails.
    """
    dsn = os.environ.get("DATABASE_DSN", "")
    if not dsn:
        return
    try:
        import psycopg2  # noqa: PLC0415 — deferred import to keep module lightweight

        conn = psycopg2.connect(dsn)
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """INSERT INTO api_usage_logs
                           (bot_id, user_id, model, input_tokens, output_tokens, cost_usd, tool_name, cache_read_tokens, cache_creation_tokens)
                    VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)""",
                    (bot_id, user_id, model, input_tokens, output_tokens, cost_usd, tool_name, cache_read_tokens, cache_creation_tokens),
                )
            conn.commit()
        finally:
            conn.close()
    except Exception as exc:
        log.warning("Failed to log API usage: %s", exc)


def _maybe_log_usage(
    usage: dict | None,
    bot_id: str,
    user_id: str | None,
    fallback_model: str,
    tool_name: str | None = None,
) -> None:
    """Log usage data if available, using *fallback_model* when the usage dict
    does not include a model name."""
    if not usage:
        return
    _log_usage(
        bot_id=bot_id,
        user_id=user_id,
        model=usage.get("model") or fallback_model,
        input_tokens=usage.get("input_tokens", 0),
        output_tokens=usage.get("output_tokens", 0),
        cost_usd=usage.get("cost_usd"),
        tool_name=tool_name,
        cache_read_tokens=usage.get("cache_read_tokens", 0),
        cache_creation_tokens=usage.get("cache_creation_tokens", 0),
    )


def _build_tool_registry() -> ToolRegistry:
    registry = ToolRegistry()
    registry.register(CreateIssueTool())
    registry.register(QueryIssuesTool())
    registry.register(GetDagStatusTool())
    registry.register(ListDagRunsTool())
    registry.register(ListDagsTool())
    registry.register(TriggerDagTool())
    registry.register(SearchCodeTool())
    registry.register(CreateBotTool())
    registry.register(ListBotsTool())
    registry.register(UpdateBotTool())
    registry.register(DeleteBotTool())
    registry.register(RestartRuntimeTool())
    return registry


def _restart_worker_if_requested() -> None:
    if is_supervised_runtime():
        return

    request = consume_restart_request("worker")
    if not request:
        return

    log.warning("Restart requested for worker: %s", request)
    os.execv(sys.executable, [sys.executable, "-m", "slack_bot.supervisor", "worker"])


def _build_tool_descriptions(registry: ToolRegistry, enabled: list[str] | None = None) -> str:
    """Build tool description text for inclusion in the prompt.

    Returns an empty string when no tools are available.
    """
    defs = registry.get_definitions(enabled)
    if not defs:
        return ""

    lines = [
        "You have access to the following tools. To use a tool, include a JSON block"
        " in your response like this:",
        "```json",
        '{"tool_call": {"name": "tool_name", "arguments": {"arg1": "value1"}}}',
        "```",
        "",
        "Available tools:",
    ]
    for d in defs:
        lines.append(f"\n### {d['name']}")
        lines.append(d["description"])
        props = d.get("input_schema", {}).get("properties", {})
        required = d.get("input_schema", {}).get("required", [])
        if props:
            lines.append("Parameters:")
            for pname, pinfo in props.items():
                req = " (required)" if pname in required else ""
                desc = pinfo.get("description", pinfo.get("type", ""))
                lines.append(f"  - {pname}: {desc}{req}")

    lines.append(
        "\nIMPORTANT: Only use ONE tool per response. If you don't need a tool,"
        " just respond normally without any JSON block."
    )
    return "\n".join(lines)


def _run_claude_cli(
    prompt: str,
    model: str = "opus",
    max_turns: int = 3,
    full_agent: bool = False,
    cwd: str | None = None,
) -> tuple[str | None, dict | None]:
    """Run Claude CLI with the given prompt and return ``(text, usage)``.

    Uses ``--output-format json`` so that token/cost metadata can be
    extracted alongside the human-readable result.

    In full_agent mode, bypasses permissions and allows all tools natively.
    In limited mode, runs with a fixed turn budget for simple prompt/response.

    Returns ``(None, None)`` on timeout, non-zero exit code, or empty output.
    """
    prompt_file = None
    timeout = 180  # default; overridden based on mode below
    try:
        with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False) as f:
            f.write(prompt)
            prompt_file = f.name

        model_map = {
            "opus": "opus",
            "sonnet": "sonnet",
            "haiku": "haiku",
        }
        cli_model = model_map.get(model, model)

        if full_agent:
            # Full agent mode: high turns, all built-in tools available
            cmd = (
                f"NO_COLOR=1 HOME={os.environ.get('CONTAINER_HOME', '/home/appuser')} "
                f"{CLAUDE_CLI_PATH} -p \"$(cat {prompt_file})\" "
                f"--model {cli_model} --max-turns {max_turns} "
                f"--output-format json "
                f"--dangerously-skip-permissions"
            )
            timeout = max_turns * 60  # 1 min per turn
        else:
            # Limited prompt mode
            cmd = (
                f"NO_COLOR=1 HOME={os.environ.get('CONTAINER_HOME', '/home/appuser')} "
                f"{CLAUDE_CLI_PATH} -p \"$(cat {prompt_file})\" "
                f"--model {cli_model} --max-turns {max_turns} "
                f"--output-format json"
            )
            timeout = 180

        result = subprocess.run(
            cmd,
            shell=True,
            capture_output=True,
            text=True,
            timeout=timeout,
            stdin=subprocess.DEVNULL,
            cwd=cwd,
        )

        if result.returncode != 0:
            log.error(
                "%s error (exit %d): %s",
                _provider_label("claude"),
                result.returncode,
                result.stderr[:500],
            )
            return None, None

        output = result.stdout.strip()
        if not output:
            log.warning("%s returned empty output", _provider_label("claude"))
            return None, None

        text, usage = _parse_claude_json_output(output)
        return (text if text else None), usage

    except subprocess.TimeoutExpired:
        log.error("%s timed out after %ds", _provider_label("claude"), timeout)
        return None, None
    except Exception as e:
        log.error("Unexpected error running %s: %s", _provider_label("claude"), e)
        return None, None
    finally:
        if prompt_file and os.path.exists(prompt_file):
            os.unlink(prompt_file)


def _run_codex_cli(
    prompt: str,
    model: str = "gpt-5.4",
    cwd: str | None = None,
) -> str | None:
    """Run Codex CLI with the given prompt and return the output.

    Uses 'codex exec' in non-interactive mode with full sandbox bypass.
    Returns None on timeout, non-zero exit code, or empty output.

    .. todo:: Add structured JSON output parsing when Codex CLI supports a
       ``--output-format json`` flag (or equivalent) so that token usage can
       be captured and logged via ``_log_usage``.
    """
    prompt_file = None
    timeout = 600  # 10 min max
    try:
        with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False) as f:
            f.write(prompt)
            prompt_file = f.name

        cmd = (
            f"NO_COLOR=1 {CODEX_CLI_PATH} exec "
            f"\"$(cat {prompt_file})\" "
            f"-m {model} "
            f"--dangerously-bypass-approvals-and-sandbox"
        )
        result = subprocess.run(
            cmd,
            shell=True,
            capture_output=True,
            text=True,
            timeout=timeout,
            stdin=subprocess.DEVNULL,
            cwd=cwd,
        )

        if result.returncode != 0:
            log.error(
                "%s error (exit %d): %s",
                _provider_label("codex"),
                result.returncode,
                result.stderr[:500],
            )
            return None

        output = result.stdout.strip()
        if not output:
            log.warning("%s returned empty output", _provider_label("codex"))
            return None

        # Strip codex metadata header/footer lines, keep only content
        lines = output.split("\n")
        content_lines = []
        skip_header = True
        for line in lines:
            if skip_header:
                if line.startswith((
                    "OpenAI Codex", "--------", "workdir:", "model:",
                    "provider:", "approval:", "sandbox:", "reasoning",
                    "session id:",
                )):
                    continue
                skip_header = False
            # Strip footer metadata
            if line.strip() in ("codex", "tokens used") or line.strip().isdigit():
                continue
            content_lines.append(line)

        clean_output = "\n".join(content_lines).strip()
        return clean_output if clean_output else None

    except subprocess.TimeoutExpired:
        log.error("%s timed out after %ds", _provider_label("codex"), timeout)
        return None
    except Exception as e:
        log.error("Unexpected error running %s: %s", _provider_label("codex"), e)
        return None
    finally:
        if prompt_file and os.path.exists(prompt_file):
            os.unlink(prompt_file)


def _extract_tool_call(text: str) -> dict | None:
    """Extract a tool_call JSON block from Claude's response.

    Returns the tool call dict or None if no valid tool call is found.
    """
    pattern = r"```json\s*\n?(.*?)\n?\s*```"
    for match in re.findall(pattern, text, re.DOTALL):
        try:
            parsed = json.loads(match.strip())
            if "tool_call" in parsed:
                return parsed["tool_call"]
        except json.JSONDecodeError:
            continue
    return None


def _execute_tool(
    tool_name: str,
    tool_input: dict,
    registry: ToolRegistry,
    config: BotConfig,
) -> str:
    """Execute a tool by name and return the result content string."""
    tool = registry.get(tool_name)
    if not tool:
        return f"Unknown tool: {tool_name}"

    extra: dict = {}
    if tool_name in ("create_issue", "query_issues"):
        extra["repo"] = config.project.github_repo
        token_var = config.project.github_token_var
        extra["token"] = os.environ.get(token_var, os.environ.get("GITHUB_TOKEN", ""))
    elif tool_name == "search_code":
        extra["repo_path"] = config.project.repo_path
        extra["max_turns"] = config.claude.max_turns

    result = tool.execute(**tool_input, **extra)
    return result.content


def _detect_analysis_thread(
    publisher: ResponsePublisher,
    channel_id: str,
    thread_ts: str,
) -> int | None:
    """Check if a thread is an analysis notification thread.

    Returns the issue number if the parent message matches analysis patterns,
    None otherwise.

    Note: Analysis thread detection requires fetching the thread root message,
    which is currently only supported for Slack (``SlackResponsePublisher``).
    On other platforms, this always returns ``None``.
    """
    if not thread_ts:
        return None

    if not isinstance(publisher, SlackResponsePublisher):
        return None

    try:
        result = publisher._client.conversations_history(
            channel=channel_id,
            latest=thread_ts,
            inclusive=True,
            limit=1,
        )
        messages = result.get("messages", [])
        if not messages:
            return None

        parent_text = messages[0].get("text", "")
        match = re.search(r"이슈 #(\d+)", parent_text)
        if match:
            return int(match.group(1))
        return None
    except Exception as e:
        log.warning("Failed to detect analysis thread: %s", e)
        return None


def _fetch_issue_context(
    issue_number: int,
    repo: str = "",
) -> str:
    """Fetch GitHub issue body and analysis comments as context string."""
    token = os.environ.get("GITHUB_TOKEN", "")
    if not token:
        return ""
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
    }
    try:
        issue_resp = requests.get(
            f"https://api.github.com/repos/{repo}/issues/{issue_number}",
            headers=headers,
            timeout=10,
        )
        if not issue_resp.ok:
            return ""
        issue = issue_resp.json()

        comments_resp = requests.get(
            f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments",
            headers=headers,
            timeout=10,
        )
        comments = comments_resp.json() if comments_resp.ok else []

        sections = [
            f"## GitHub 이슈 #{issue_number}: {issue.get('title', '')}",
            f"Labels: {', '.join(l['name'] for l in issue.get('labels', []))}",
            f"\n{issue.get('body', '')[:3000]}",
        ]

        for comment in comments:
            body = comment.get("body", "")
            if any(marker in body for marker in ["🏛️", "🤝", "🎓", "❓", "🚀", "✅"]):
                sections.append(f"\n---\n{body[:2000]}")

        return "\n".join(sections)
    except Exception as e:
        log.warning("Failed to fetch issue context: %s", e)
        return ""


def _forward_to_github_issue(
    issue_number: int,
    user_message: str,
    repo: str = "",
) -> bool:
    """Post user's Slack message as a GitHub issue comment."""
    token = os.environ.get("GITHUB_TOKEN", "")
    if not token:
        return False
    try:
        resp = requests.post(
            f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments",
            json={"body": f"💬 **Slack 사용자 답변:**\n\n{user_message}"},
            headers={
                "Authorization": f"Bearer {token}",
                "Accept": "application/vnd.github+json",
            },
            timeout=10,
        )
        return resp.ok
    except Exception:
        return False


def _build_system_prompt(config: BotConfig) -> str:
    parts = []
    if config.persona.personality:
        parts.append(config.persona.personality)
    if config.project.github_repo:
        parts.append(f"Project repository: {config.project.github_repo}")
    if config.project.repo_path:
        parts.append(f"Local repo path: {config.project.repo_path}")
    parts.append("Respond concisely. Use Korean unless instructed otherwise.")
    return "\n\n".join(parts)


def _apply_response_prefix(text: str, config: BotConfig) -> str:
    """Ensure configured response prefix is present exactly once."""
    prefix = (config.persona.response_prefix or "").strip()
    body = (text or "").strip()

    if not prefix or not body:
        return text

    if body.startswith(prefix):
        return body

    return f"{prefix}\n\n{body}"


def _format_history_section(
    title: str,
    messages: list[dict],
    current_text: str,
    max_messages: int,
) -> str:
    """Format recent messages for prompt context."""
    if not messages:
        return ""

    filtered = list(messages)
    if filtered and filtered[-1].get("role") == "user" and filtered[-1].get("content") == current_text:
        filtered = filtered[:-1]

    if not filtered:
        return ""

    lines = []
    for msg in filtered[-max_messages:]:
        role_label = "User" if msg["role"] == "user" else "Assistant"
        lines.append(f"{role_label}: {msg['content']}")

    if not lines:
        return ""
    return f"\n\n## {title}\n" + "\n".join(lines)


def process_message(
    msg_data: dict,
    bots: dict[str, BotConfig],
    registry: ToolRegistry,
    store: MessageStore,
    memory_search: HybridSearch,
    extractor: MemoryExtractor,
) -> None:
    """Process a single message from the Redis stream.

    Phase 1: Call Claude CLI with system prompt, tool descriptions, and user
    message. If Claude requests a tool call, execute it.
    Phase 2: If a tool was called, send a followup prompt with the tool result
    to generate the final user-facing response.
    """
    bot_id = msg_data.get("bot_id", "")
    channel_id = msg_data.get("channel_id", "")
    thread_ts = msg_data.get("thread_ts", "")
    user_id = msg_data.get("user_id", "")
    text = msg_data.get("text", "")
    message_ts = msg_data.get("message_ts", "")
    bot_token = msg_data.get("bot_token", "")
    platform = msg_data.get("platform", "slack")

    config = bots.get(bot_id)
    if not config:
        log.warning("Unknown bot_id: %s", bot_id)
        return

    publisher = _get_publisher(msg_data, bots)

    try:
        # Persist user message before processing
        store.save_message(bot_id, channel_id, thread_ts, user_id, "user", text)

        # Detect if this is an analysis notification thread
        analysis_issue = _detect_analysis_thread(publisher, channel_id, thread_ts)
        if analysis_issue:
            log.info("Detected analysis thread for issue #%d", analysis_issue)

            # Forward user message to GitHub issue
            _forward_to_github_issue(analysis_issue, text)

            # Inject issue context into the conversation
            issue_context = _fetch_issue_context(analysis_issue)
            if issue_context:
                msg_data["_issue_context"] = issue_context
                msg_data["_analysis_issue"] = analysis_issue

        # Recent history: only last 3 messages for immediate context
        thread_history = store.get_thread_messages(
            bot_id, channel_id, thread_ts, limit=3
        )
        channel_history = store.get_channel_messages(
            bot_id,
            channel_id,
            limit=3,
            exclude_thread_ts=thread_ts if thread_ts else None,
        )

        system_prompt = _build_system_prompt(config)

        # Build minimal recent history (last 3 only)
        history_text = ""
        if len(thread_history) > 1:
            history_text += _format_history_section(
                "Recent thread conversation",
                thread_history,
                text,
                max_messages=3,
            )
        elif channel_history:
            history_text += _format_history_section(
                "Recent channel conversation",
                channel_history,
                text,
                max_messages=3,
            )

        if thread_history and channel_history:
            history_text += _format_history_section(
                "Recent channel background",
                channel_history,
                current_text="",
                max_messages=2,
            )

        provider = (config.claude.provider or "claude").lower()
        model = config.claude.model
        max_turns = config.claude.max_turns
        is_full_agent = config.claude.full_agent

        # Search relevant memories (best-effort, non-blocking)
        memory_context = ""
        try:
            memories = memory_search.search(bot_id, text, top_k=5)
            if memories:
                memory_lines = ["## 기억하고 있는 관련 정보:"]
                for mem in memories:
                    memory_lines.append(f"- [{mem['category']}] {mem['content']}")
                memory_context = "\n".join(memory_lines)
        except Exception as e:
            log.warning("Memory search failed (non-blocking): %s", e)

        if is_full_agent:
            # Full agent mode: just pass the message with system context.
            # Claude CLI handles everything (file ops, bash, etc.) natively.
            memory_section = f"\n\n{memory_context}" if memory_context else ""
            history_section = f"\n\n{history_text}" if history_text else ""
            issue_section = ""
            if msg_data.get("_issue_context"):
                issue_section = (
                    f"\n\n## GitHub 이슈 분석 컨텍스트\n"
                    f"이 스레드는 GitHub 이슈 #{msg_data['_analysis_issue']} 분석과 관련됩니다.\n"
                    f"아래 이슈와 분석 결과를 참고하여 사용자의 질문에 답변하세요.\n\n"
                    f"{msg_data['_issue_context']}"
                )
            full_prompt = (
                f"{system_prompt}"
                f"{memory_section}"
                f"{issue_section}"
                f"{history_section}"
                f"\n\n사용자의 메시지에 직접 답변하세요. 내부 동작을 설명하지 마세요.\n\n"
                f"사용자: {text}"
            )
            # No tool descriptions needed — CLI has its own tools
            if provider == "codex":
                response_text = _run_codex_cli(
                    full_prompt,
                    model=model,
                    cwd=config.project.repo_path or None,
                )
                usage = None
            else:
                response_text, usage = _run_claude_cli(
                    full_prompt,
                    model=model,
                    max_turns=max_turns,
                    full_agent=True,
                    cwd=config.project.repo_path or None,
                )
            _maybe_log_usage(usage, bot_id, user_id, model, tool_name="claude_cli")
            final_text = _require_response(response_text, provider)
        else:
            # Limited mode: use tool descriptions + JSON tool_call parsing
            tool_descriptions = _build_tool_descriptions(
                registry, config.tools_enabled or None
            )

            # Phase 1 prompt: intent detection with optional tool use
            issue_section = ""
            if msg_data.get("_issue_context"):
                issue_section = (
                    f"\n\n## GitHub 이슈 분석 컨텍스트\n"
                    f"이 스레드는 GitHub 이슈 #{msg_data['_analysis_issue']} 분석과 관련됩니다.\n\n"
                    f"{msg_data['_issue_context']}\n\n"
                )
            phase1_prompt = (
                f"## System\n{system_prompt}\n\n"
                f"{memory_context}"
                f"{issue_section}\n\n"
                f"{tool_descriptions}"
                f"{history_text}\n\n"
                f"## Current message from user:\n{text}"
            )

            if provider == "codex":
                response_text = _run_codex_cli(
                    phase1_prompt,
                    model=model,
                    cwd=config.project.repo_path or None,
                )
                usage = None
            else:
                response_text, usage = _run_claude_cli(
                    phase1_prompt, model=model, max_turns=max_turns
                )
            _maybe_log_usage(usage, bot_id, user_id, model, tool_name="claude_cli")

            response_text = _require_response(response_text, provider)

            tool_call = _extract_tool_call(response_text)
            if tool_call:
                tool_name = tool_call.get("name", "")
                tool_args = tool_call.get("arguments", {})
                log.info("Tool call detected: %s(%s)", tool_name, tool_args)

                tool_result = _execute_tool(tool_name, tool_args, registry, config)
                log.info("Tool result (first 200 chars): %s", tool_result[:200])

                # Phase 2 prompt: synthesise tool result into final response
                phase2_prompt = (
                    f"## System\n{system_prompt}\n\n"
                    f"## User's original message:\n{text}\n\n"
                    f"## Tool executed: {tool_name}\n"
                    f"## Tool result:\n{tool_result}\n\n"
                    "Based on the tool result above, provide a helpful response to the"
                    " user. Respond in Korean."
                )

                if provider == "codex":
                    final_text = _run_codex_cli(
                        phase2_prompt,
                        model=model,
                        cwd=config.project.repo_path or None,
                    )
                    p2_usage = None
                else:
                    final_text, p2_usage = _run_claude_cli(
                        phase2_prompt, model=model, max_turns=max_turns
                    )
                _maybe_log_usage(p2_usage, bot_id, user_id, model, tool_name="claude_cli")
                if not final_text:
                    final_text = tool_result  # fallback: surface raw tool result
            else:
                final_text = response_text

        final_text = _apply_response_prefix(final_text, config)

        publisher.send_message(
            channel_id=channel_id,
            text=final_text,
            thread_id=thread_ts if thread_ts != message_ts else None,
        )

        store.save_message(
            bot_id, channel_id, thread_ts, "system", "assistant", final_text
        )

        # Background: extract memories from this conversation (non-blocking)
        if config.memory.auto_extract:
            try:
                recent = store.get_recent_messages(
                    bot_id, channel_id, thread_ts=thread_ts, limit=8
                )
                if len(recent) >= 4:
                    extractor.extract_and_store(bot_id, user_id, recent)
            except Exception as e:
                log.warning("Memory extraction failed (non-blocking): %s", e)

        try:
            publisher.remove_reaction(
                channel_id=channel_id,
                message_id=message_ts,
                emoji="hourglass_flowing_sand",
            )
        except Exception:
            pass
        try:
            publisher.add_reaction(
                channel_id=channel_id,
                message_id=message_ts,
                emoji="white_check_mark",
            )
        except Exception:
            pass

        log.info("Processed message for bot %s in %s", bot_id, channel_id)

    except Exception as e:
        log.error("Error processing message: %s", e, exc_info=True)
        try:
            publisher.remove_reaction(
                channel_id=channel_id,
                message_id=message_ts,
                emoji="hourglass_flowing_sand",
            )
        except Exception:
            pass
        try:
            publisher.add_reaction(
                channel_id=channel_id,
                message_id=message_ts,
                emoji="x",
            )
        except Exception:
            pass
        try:
            publisher.send_message(
                channel_id=channel_id,
                text=_user_visible_error_message(e),
                thread_id=thread_ts if thread_ts != message_ts else None,
            )
        except Exception:
            pass


def _routing_key(msg: dict) -> str:
    """Derive a serialisation key for a decoded message.

    Messages in the same thread are pinned to that thread so replies arrive
    in order.  Top-level channel messages from the same user are grouped
    together so rapid-fire messages can be merged before processing.
    """
    thread_ts = msg.get("thread_ts", "")
    message_ts = msg.get("message_ts", "")
    if thread_ts and thread_ts != message_ts:
        return f"thread:{thread_ts}"
    return f"channel:{msg['channel_id']}:{msg['user_id']}"


class _PendingItem:
    """A merged batch of messages awaiting dispatch to the thread-pool."""

    __slots__ = ("msg_ids", "merged_msg")

    def __init__(self, msg_ids: list, merged_msg: dict) -> None:
        self.msg_ids = msg_ids
        self.merged_msg = merged_msg


class MessageDispatcher:
    """Routes incoming stream messages to per-key serial queues.

    Design
    ------
    * A short *merge window* (``MERGE_WINDOW`` seconds) coalesces rapid-fire
      messages that share the same routing key before handing them off to a
      worker.  This prevents a user who sends three quick messages from
      triggering three separate LLM calls.
    * Within a routing key, messages are processed strictly in FIFO order —
      the next item is only submitted once the previous one completes.
    * Across different routing keys, work runs in parallel up to
      ``MAX_CONCURRENT`` threads.
    """

    def __init__(
        self,
        bots: dict,
        registry,
        store,
        memory_search,
        extractor,
        max_workers: int = MAX_CONCURRENT,
        merge_window: int = MERGE_WINDOW,
    ) -> None:
        self._bots = bots
        self._registry = registry
        self._store = store
        self._memory_search = memory_search
        self._extractor = extractor
        self._merge_window = merge_window

        # Per-routing-key FIFO queues of _PendingItem objects.
        self._queues: dict[str, collections.deque] = {}
        # Routing keys whose worker slot is currently occupied.
        self._active: set[str] = set()
        # Messages accumulating during the merge window, keyed by routing key.
        self._merge_buffers: dict[str, list[tuple[str, dict]]] = {}
        # Live threading.Timer objects, one per active routing key.
        self._merge_timers: dict[str, threading.Timer] = {}

        self._lock = threading.Lock()
        self._executor = concurrent.futures.ThreadPoolExecutor(
            max_workers=max_workers,
            thread_name_prefix="msg-worker",
        )

    # ------------------------------------------------------------------
    # Public interface
    # ------------------------------------------------------------------

    def dispatch(self, msg_id: str, decoded_msg: dict, redis_client) -> None:
        """Accept a new message and start or extend its merge window."""
        key = _routing_key(decoded_msg)

        with self._lock:
            if key in self._merge_timers:
                # Extend: cancel existing timer, accumulate message, restart.
                self._merge_timers[key].cancel()

            self._merge_buffers.setdefault(key, []).append((msg_id, decoded_msg))

            timer = threading.Timer(
                self._merge_window,
                self._flush_merge_buffer,
                args=(key, redis_client),
            )
            self._merge_timers[key] = timer
            timer.daemon = True
            timer.start()

    def shutdown(self, wait: bool = True) -> None:
        """Cancel pending timers and shut down the thread-pool."""
        with self._lock:
            for timer in self._merge_timers.values():
                timer.cancel()
            self._merge_timers.clear()
        self._executor.shutdown(wait=wait)

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _flush_merge_buffer(self, routing_key: str, redis_client) -> None:
        """Merge buffered messages and enqueue the combined item.

        Called by the merge-window timer on expiry.
        """
        with self._lock:
            self._merge_timers.pop(routing_key, None)
            buffered = self._merge_buffers.pop(routing_key, [])

        if not buffered:
            return

        msg_ids = [entry[0] for entry in buffered]
        msgs = [entry[1] for entry in buffered]

        # Merge: concatenate text, keep first thread_ts, last message_ts.
        merged_text = "\n".join(m.get("text", "") for m in msgs if m.get("text"))
        merged_msg = dict(msgs[0])
        merged_msg["text"] = merged_text
        merged_msg["message_ts"] = msgs[-1].get("message_ts", merged_msg.get("message_ts", ""))

        item = _PendingItem(msg_ids=msg_ids, merged_msg=merged_msg)

        with self._lock:
            queue = self._queues.setdefault(routing_key, collections.deque())
            queue.append((item, redis_client))
            self._try_process_next(routing_key)

    def _try_process_next(self, routing_key: str) -> None:
        """Submit the next queued item if the routing key is idle.

        Must be called with ``self._lock`` held.
        """
        if routing_key in self._active:
            return
        queue = self._queues.get(routing_key)
        if not queue:
            return

        item, redis_client = queue.popleft()
        self._active.add(routing_key)
        self._executor.submit(
            self._process_and_continue,
            routing_key,
            item,
            redis_client,
        )

    def _process_and_continue(
        self,
        routing_key: str,
        item: _PendingItem,
        redis_client,
    ) -> None:
        """Process one merged item, ACK all its stream IDs, then pop the next."""
        try:
            process_message(
                item.merged_msg,
                self._bots,
                self._registry,
                self._store,
                self._memory_search,
                self._extractor,
            )
        except Exception as e:
            log.error(
                "Dispatcher: unhandled error for key %s: %s",
                routing_key,
                e,
                exc_info=True,
            )
        finally:
            # ACK all stream IDs that were merged into this batch.
            try:
                redis_client.xack(STREAM_KEY, CONSUMER_GROUP, *item.msg_ids)
            except Exception as e:
                log.error("Failed to ACK message IDs %s: %s", item.msg_ids, e)

            with self._lock:
                self._active.discard(routing_key)
                self._try_process_next(routing_key)


def _decode_stream_message(msg_data: dict) -> dict:
    return {
        k.decode() if isinstance(k, bytes) else k:
        v.decode() if isinstance(v, bytes) else v
        for k, v in msg_data.items()
    }


def _dispatch_entries(
    redis_client,
    entries,
    dispatcher: MessageDispatcher,
) -> None:
    """Feed stream entries into the dispatcher (non-blocking)."""
    for _stream, messages in entries:
        for msg_id, msg_data in messages:
            decoded = _decode_stream_message(msg_data)
            dispatcher.dispatch(
                msg_id.decode() if isinstance(msg_id, bytes) else msg_id,
                decoded,
                redis_client,
            )


def _claim_pending(
    redis_client,
    dispatcher: MessageDispatcher,
) -> bool:
    """Re-claim idle pending messages and route them through the dispatcher."""
    claimed_any = False
    next_start_id = "0-0"

    while True:
        next_start_id, messages, _deleted = redis_client.xautoclaim(
            STREAM_KEY,
            CONSUMER_GROUP,
            CONSUMER_NAME,
            min_idle_time=PENDING_IDLE_MS,
            start_id=next_start_id,
            count=10,
        )
        if not messages:
            break

        claimed_any = True
        _dispatch_entries(redis_client, [(STREAM_KEY, messages)], dispatcher)

        if next_start_id == "0-0":
            break

    return claimed_any


def main():
    redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
    redis_client = redis.from_url(redis_url)

    try:
        redis_client.xgroup_create(STREAM_KEY, CONSUMER_GROUP, id="0", mkstream=True)
        log.info("Created consumer group: %s", CONSUMER_GROUP)
    except redis.exceptions.ResponseError as e:
        if "BUSYGROUP" not in str(e):
            raise
        log.info("Consumer group already exists: %s", CONSUMER_GROUP)

    bots_dir = os.environ.get("BOTS_DIR", "/app/bots")
    configs = load_all_bots(bots_dir)
    bots = {c.id: c for c in configs}
    log.info("Loaded %d bot config(s)", len(bots))

    registry = _build_tool_registry()
    store = MessageStore()
    memory_search = HybridSearch()
    extractor = MemoryExtractor()

    # Verify Claude CLI availability at startup
    try:
        check = subprocess.run(
            f"HOME={os.environ.get('CONTAINER_HOME', '/home/appuser')} {CLAUDE_CLI_PATH} --version",
            shell=True,
            capture_output=True,
            text=True,
            timeout=10,
            stdin=subprocess.DEVNULL,
        )
        log.info("Claude CLI version: %s", check.stdout.strip())
    except Exception as e:
        log.warning("Claude CLI check failed: %s (will retry on first message)", e)

    # Verify Codex CLI availability
    try:
        check = subprocess.run(
            f"{CODEX_CLI_PATH} --version",
            shell=True,
            capture_output=True,
            text=True,
            timeout=10,
            stdin=subprocess.DEVNULL,
        )
        log.info("Codex CLI version: %s", check.stdout.strip())
    except Exception as e:
        log.warning("Codex CLI check failed: %s (codex provider won't work)", e)

    dispatcher = MessageDispatcher(
        bots=bots,
        registry=registry,
        store=store,
        memory_search=memory_search,
        extractor=extractor,
        max_workers=MAX_CONCURRENT,
        merge_window=MERGE_WINDOW,
    )

    # Start analysis request consumer in background
    start_analysis_consumer(redis_client)

    # Start credential health probe in background
    from slack_bot.credential_probe import start_credential_probe
    probe_thread = start_credential_probe()
    log.info("Credential probe started (interval: 30m)")

    log.info(
        "Worker %s started. Consuming from %s (max_workers=%d, merge_window=%ds)...",
        CONSUMER_NAME,
        STREAM_KEY,
        MAX_CONCURRENT,
        MERGE_WINDOW,
    )

    try:
        while True:
            try:
                _claim_pending(redis_client, dispatcher)
                entries = redis_client.xreadgroup(
                    CONSUMER_GROUP,
                    CONSUMER_NAME,
                    {STREAM_KEY: ">"},
                    count=10,
                    block=1000,
                )

                if not entries:
                    _restart_worker_if_requested()
                    continue

                _dispatch_entries(redis_client, entries, dispatcher)
                _restart_worker_if_requested()

            except KeyboardInterrupt:
                raise
            except Exception as e:
                log.error("Worker error: %s", e)
                time.sleep(5)
    except KeyboardInterrupt:
        log.info("Worker shutting down...")
    finally:
        dispatcher.shutdown(wait=True)


if __name__ == "__main__":
    main()
