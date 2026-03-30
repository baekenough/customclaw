"""Bot management tools for the customclaw master bot."""

from __future__ import annotations

import json
import logging
import os

import psycopg2
import redis

from bot_engine.runtime_control import request_restart
from bot_engine.tools.base import BaseTool, ToolDefinition, ToolResult

log = logging.getLogger(__name__)


def _get_db_conn():
    dsn = os.environ.get("DATABASE_DSN", "")
    return psycopg2.connect(dsn) if dsn else None


def _notify_hot_reload(bot_id: str) -> None:
    """Notify config change via Pub/Sub. Worker hot-reloads; app restarts."""
    redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
    r = redis.from_url(redis_url)
    r.publish("customclaw:bot-config-changed", bot_id)
    request_restart(
        "app",
        requested_by="bot_config_changed",
        reason=f"bot:{bot_id}",
    )


class CreateBotTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="create_bot",
            description=(
                "Create a new bot configuration. Supports Slack, Mattermost, and Discord. "
                "Provide the platform and credentials dict for the chosen platform. "
                "For Slack: credentials must include app_token (xapp-...) and bot_token (xoxb-...). "
                "For Mattermost: credentials must include url and token. "
                "For Discord: credentials must include token."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "bot_id": {
                        "type": "string",
                        "description": "Unique bot identifier (kebab-case, e.g., 'omc-bot')",
                    },
                    "name": {
                        "type": "string",
                        "description": "Display name for the bot",
                    },
                    "platform": {
                        "type": "string",
                        "enum": ["slack", "mattermost", "discord"],
                        "description": "Target platform for the bot",
                    },
                    "credentials": {
                        "type": "object",
                        "description": (
                            "Platform credentials. "
                            "Slack: {app_token, bot_token}. "
                            "Mattermost: {url, token, port?}. "
                            "Discord: {token, guild_id?}."
                        ),
                    },
                    "slack_app_token": {
                        "type": "string",
                        "description": "Deprecated. Use credentials.app_token instead.",
                    },
                    "slack_bot_token": {
                        "type": "string",
                        "description": "Deprecated. Use credentials.bot_token instead.",
                    },
                    "personality": {
                        "type": "string",
                        "description": "Bot personality/system prompt description",
                    },
                    "github_repo": {
                        "type": "string",
                        "description": "GitHub repository (owner/repo format)",
                        "default": "",
                    },
                    "repo_path": {
                        "type": "string",
                        "description": "Local repository path on server",
                        "default": "",
                    },
                    "tools_enabled": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "List of tool names to enable",
                        "default": [
                            "create_issue",
                            "query_issues",
                            "search_code",
                            "get_dag_status",
                            "list_dag_runs",
                            "trigger_dag",
                        ],
                    },
                    "model": {
                        "type": "string",
                        "enum": ["sonnet", "opus", "haiku"],
                        "default": "sonnet",
                    },
                },
                "required": ["bot_id", "name", "platform", "personality"],
            },
        )

    def _resolve_credentials(self, kwargs: dict) -> dict:
        """Resolve credentials from either the unified dict or legacy Slack fields."""
        credentials = dict(kwargs.get("credentials") or {})
        if not credentials:
            # Backward compat: promote legacy Slack token fields to credentials
            slack_app_token = kwargs.get("slack_app_token", "")
            slack_bot_token = kwargs.get("slack_bot_token", "")
            if slack_app_token or slack_bot_token:
                credentials = {
                    "app_token": slack_app_token,
                    "bot_token": slack_bot_token,
                }
        return credentials

    def _validate_credentials(self, platform: str, credentials: dict) -> str | None:
        """Return an error message if credentials are invalid, else None."""
        if platform == "slack":
            app_token = credentials.get("app_token", "")
            bot_token = credentials.get("bot_token", "")
            if not app_token or not app_token.startswith("xapp-"):
                return "Slack credentials.app_token is required and must start with 'xapp-'."
            if not bot_token or not bot_token.startswith("xoxb-"):
                return "Slack credentials.bot_token is required and must start with 'xoxb-'."
        elif platform == "mattermost":
            if not credentials.get("url") or not credentials.get("token"):
                return "Mattermost credentials must include 'url' and 'token'."
        elif platform == "discord":
            if not credentials.get("token"):
                return "Discord credentials must include 'token'."
        return None

    def execute(self, **kwargs) -> ToolResult:
        bot_id = kwargs.get("bot_id", "")
        name = kwargs.get("name", "")
        platform = kwargs.get("platform", "")
        personality = kwargs.get("personality", "")
        github_repo = kwargs.get("github_repo", "")
        repo_path = kwargs.get("repo_path", "")
        tools_enabled = kwargs.get("tools_enabled", [])
        model = kwargs.get("model", "sonnet")

        if not bot_id:
            return ToolResult(content="bot_id is required.", is_error=True)

        credentials = self._resolve_credentials(kwargs)
        error = self._validate_credentials(platform, credentials)
        if error:
            return ToolResult(content=error, is_error=True)

        # Preserve legacy columns for Slack bots to support old schema readers
        slack_app_token = credentials.get("app_token", "") if platform == "slack" else ""
        slack_bot_token = credentials.get("bot_token", "") if platform == "slack" else ""

        conn = _get_db_conn()
        if not conn:
            return ToolResult(content="Database connection not available", is_error=True)

        try:
            with conn.cursor() as cur:
                cur.execute("SELECT id FROM bots WHERE id = %s", (bot_id,))
                if cur.fetchone():
                    return ToolResult(
                        content=f"Bot '{bot_id}' already exists. Use update_bot to modify.",
                        is_error=True,
                    )

                cur.execute(
                    """INSERT INTO bots
                        (id, name, slack_app_token, slack_bot_token, channels,
                         persona, project, tools, claude, memory, security,
                         platform, credentials, is_active)
                       VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, true)""",
                    (
                        bot_id,
                        name,
                        slack_app_token,
                        slack_bot_token,
                        json.dumps([]),
                        json.dumps({"display_name": name, "personality": personality}),
                        json.dumps({"github_repo": github_repo, "repo_path": repo_path}),
                        json.dumps({"enabled": tools_enabled}),
                        json.dumps({"model": model, "max_turns": 10}),
                        json.dumps({"context_window": 20, "auto_extract": True}),
                        json.dumps({
                            "allowed_channels": [],
                            "allowed_users": [],
                            "dangerous_tools": ["trigger_dag", "create_issue"],
                        }),
                        platform,
                        json.dumps(credentials),
                    ),
                )
            conn.commit()

            _notify_hot_reload(bot_id)

            return ToolResult(
                content=(
                    f"Bot '{bot_id}' created successfully!\n"
                    f"- Name: {name}\n"
                    f"- Platform: {platform}\n"
                    f"- Model: {model}\n"
                    f"- Tools: {', '.join(tools_enabled)}\n"
                    f"- GitHub: {github_repo or 'not configured'}\n\n"
                    "The bot runtime restart was requested automatically."
                )
            )
        except Exception as e:
            conn.rollback()
            return ToolResult(content=f"Failed to create bot: {e}", is_error=True)
        finally:
            conn.close()


class ListBotsTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="list_bots",
            description="List all registered bots and their status.",
            input_schema={
                "type": "object",
                "properties": {
                    "include_inactive": {
                        "type": "boolean",
                        "default": False,
                        "description": "Include deactivated bots",
                    },
                },
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        include_inactive = kwargs.get("include_inactive", False)

        conn = _get_db_conn()
        if not conn:
            return ToolResult(content="Database connection not available", is_error=True)

        try:
            with conn.cursor() as cur:
                if include_inactive:
                    cur.execute(
                        "SELECT id, name, is_active, persona, project, claude, created_at"
                        " FROM bots ORDER BY created_at"
                    )
                else:
                    cur.execute(
                        "SELECT id, name, is_active, persona, project, claude, created_at"
                        " FROM bots WHERE is_active = true ORDER BY created_at"
                    )

                rows = cur.fetchall()

            if not rows:
                return ToolResult(content="No bots registered yet. Use create_bot to create one.")

            lines = [f"Registered bots ({len(rows)}):"]
            for row in rows:
                bot_id, name, is_active, persona, project, claude, created_at = row
                status = "active" if is_active else "inactive"

                persona_data = (
                    persona if isinstance(persona, dict) else json.loads(persona) if persona else {}
                )
                project_data = (
                    project if isinstance(project, dict) else json.loads(project) if project else {}
                )
                claude_data = (
                    claude if isinstance(claude, dict) else json.loads(claude) if claude else {}
                )

                lines.append(
                    f"\n- **{bot_id}** ({status})\n"
                    f"  Name: {name}\n"
                    f"  Model: {claude_data.get('model', 'sonnet')}\n"
                    f"  GitHub: {project_data.get('github_repo', 'N/A')}\n"
                    f"  Created: {created_at}"
                )

            return ToolResult(content="\n".join(lines))
        except Exception as e:
            return ToolResult(content=f"Failed to list bots: {e}", is_error=True)
        finally:
            conn.close()


class UpdateBotTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="update_bot",
            description=(
                "Update an existing bot's configuration (personality, tools, model, etc.)."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "bot_id": {"type": "string", "description": "Bot ID to update"},
                    "personality": {
                        "type": "string",
                        "description": "New personality/system prompt",
                    },
                    "github_repo": {
                        "type": "string",
                        "description": "New GitHub repository",
                    },
                    "repo_path": {
                        "type": "string",
                        "description": "New local repo path",
                    },
                    "model": {
                        "type": "string",
                        "enum": ["sonnet", "opus", "haiku"],
                    },
                    "tools_enabled": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "New list of enabled tools",
                    },
                    "is_active": {
                        "type": "boolean",
                        "description": "Activate or deactivate the bot",
                    },
                },
                "required": ["bot_id"],
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        bot_id = kwargs.get("bot_id", "")

        conn = _get_db_conn()
        if not conn:
            return ToolResult(content="Database connection not available", is_error=True)

        try:
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT persona, project, tools, claude FROM bots WHERE id = %s",
                    (bot_id,),
                )
                row = cur.fetchone()
                if not row:
                    return ToolResult(content=f"Bot '{bot_id}' not found.", is_error=True)

                persona, project, tools, claude = row
                persona = (
                    persona if isinstance(persona, dict) else json.loads(persona) if persona else {}
                )
                project = (
                    project if isinstance(project, dict) else json.loads(project) if project else {}
                )
                tools_data = (
                    tools if isinstance(tools, dict) else json.loads(tools) if tools else {}
                )
                claude_data = (
                    claude if isinstance(claude, dict) else json.loads(claude) if claude else {}
                )

                updates: list[str] = []
                params: list = []

                if "personality" in kwargs:
                    persona["personality"] = kwargs["personality"]
                    updates.append("persona = %s")
                    params.append(json.dumps(persona))

                if "github_repo" in kwargs or "repo_path" in kwargs:
                    if "github_repo" in kwargs:
                        project["github_repo"] = kwargs["github_repo"]
                    if "repo_path" in kwargs:
                        project["repo_path"] = kwargs["repo_path"]
                    updates.append("project = %s")
                    params.append(json.dumps(project))

                if "model" in kwargs:
                    claude_data["model"] = kwargs["model"]
                    updates.append("claude = %s")
                    params.append(json.dumps(claude_data))

                if "tools_enabled" in kwargs:
                    tools_data["enabled"] = kwargs["tools_enabled"]
                    updates.append("tools = %s")
                    params.append(json.dumps(tools_data))

                if "is_active" in kwargs:
                    updates.append("is_active = %s")
                    params.append(kwargs["is_active"])

                if not updates:
                    return ToolResult(content="No fields to update specified.")

                updates.append("updated_at = NOW()")
                params.append(bot_id)

                query = f"UPDATE bots SET {', '.join(updates)} WHERE id = %s"
                cur.execute(query, params)

            conn.commit()
            _notify_hot_reload(bot_id)

            return ToolResult(
                content=f"Bot '{bot_id}' updated successfully. Runtime restart requested."
            )
        except Exception as e:
            conn.rollback()
            return ToolResult(content=f"Failed to update bot: {e}", is_error=True)
        finally:
            conn.close()


class DeleteBotTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="delete_bot",
            description=(
                "Deactivate (soft-delete) a bot. The bot will disconnect from Slack."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "bot_id": {"type": "string", "description": "Bot ID to deactivate"},
                },
                "required": ["bot_id"],
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        bot_id = kwargs.get("bot_id", "")

        conn = _get_db_conn()
        if not conn:
            return ToolResult(content="Database connection not available", is_error=True)

        try:
            with conn.cursor() as cur:
                cur.execute(
                    "UPDATE bots SET is_active = false, updated_at = NOW() WHERE id = %s",
                    (bot_id,),
                )
                if cur.rowcount == 0:
                    return ToolResult(content=f"Bot '{bot_id}' not found.", is_error=True)

            conn.commit()
            _notify_hot_reload(bot_id)

            return ToolResult(
                content=f"Bot '{bot_id}' deactivated. Runtime restart requested."
            )
        except Exception as e:
            conn.rollback()
            return ToolResult(content=f"Failed to delete bot: {e}", is_error=True)
        finally:
            conn.close()


class RestartRuntimeTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="restart_runtime",
            description=(
                "Request a safe runtime restart for the worker, app, or both. "
                "Use this when code/config changes require a process restart."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "target": {
                        "type": "string",
                        "enum": ["worker", "app", "all"],
                        "description": "Which runtime to restart",
                        "default": "worker",
                    },
                    "reason": {
                        "type": "string",
                        "description": "Short reason for the restart request",
                        "default": "",
                    },
                },
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        target = kwargs.get("target", "worker")
        reason = kwargs.get("reason", "")

        if target not in {"worker", "app", "all"}:
            return ToolResult(content="Invalid restart target.", is_error=True)

        if target == "all":
            request_restart("worker", requested_by="tool:restart_runtime", reason=reason)
            request_restart("app", requested_by="tool:restart_runtime", reason=reason)
            return ToolResult(content="Worker와 app 재시작을 요청했습니다.")

        request_restart(target, requested_by="tool:restart_runtime", reason=reason)
        return ToolResult(content=f"{target} 재시작을 요청했습니다.")
