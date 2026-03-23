"""Bot configuration loader. Reads from YAML files (bootstrap) or database."""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from pathlib import Path

import psycopg2
import yaml


@dataclass
class PersonaConfig:
    display_name: str = ""
    description: str = ""
    personality: str = ""
    response_prefix: str = ""


@dataclass
class ProjectConfig:
    repo_path: str = ""
    github_repo: str = ""
    github_token_var: str = ""


@dataclass
class AirflowConfig:
    dag_prefix: str = ""


@dataclass
class ClaudeConfig:
    provider: str = "claude"  # "claude" or "codex"
    model: str = "sonnet"
    max_turns: int = 10
    full_agent: bool = False


@dataclass
class MemoryConfig:
    context_window: int = 20
    auto_extract: bool = True


@dataclass
class SecurityConfig:
    allowed_channels: list[str] = field(default_factory=list)
    allowed_users: list[str] = field(default_factory=list)
    dangerous_tools: list[str] = field(default_factory=list)
    mention_only: bool = False


@dataclass
class MattermostConfig:
    url: str = ""
    token: str = ""
    port: int = 8065


@dataclass
class DiscordConfig:
    token: str = ""
    guild_id: str = ""


@dataclass
class BotConfig:
    id: str
    name: str
    slack_app_token: str
    slack_bot_token: str
    platform: str = "slack"
    mattermost: MattermostConfig = field(default_factory=MattermostConfig)
    discord: DiscordConfig = field(default_factory=DiscordConfig)
    channels: list[str] = field(default_factory=list)
    persona: PersonaConfig = field(default_factory=PersonaConfig)
    project: ProjectConfig = field(default_factory=ProjectConfig)
    airflow: AirflowConfig = field(default_factory=AirflowConfig)
    tools_enabled: list[str] = field(default_factory=list)
    claude: ClaudeConfig = field(default_factory=ClaudeConfig)
    memory: MemoryConfig = field(default_factory=MemoryConfig)
    security: SecurityConfig = field(default_factory=SecurityConfig)


def _resolve_env(value: str) -> str:
    """Resolve ${ENV_VAR} references in string values."""
    if isinstance(value, str) and value.startswith("${") and value.endswith("}"):
        env_key = value[2:-1]
        return os.environ.get(env_key, "")
    return value


def _validate_bot_config(config: BotConfig) -> None:
    """Validate platform-specific required fields.

    Raises:
        ValueError: If required fields for the chosen platform are missing.
    """
    if config.platform == "slack":
        missing = [
            field
            for field, value in [
                ("slack_app_token", config.slack_app_token),
                ("slack_bot_token", config.slack_bot_token),
            ]
            if not value
        ]
        if missing:
            raise ValueError(
                f"Bot '{config.id}' (platform=slack) missing required fields: "
                + ", ".join(missing)
            )
    elif config.platform == "mattermost":
        missing = [
            field
            for field, value in [
                ("mattermost.url", config.mattermost.url),
                ("mattermost.token", config.mattermost.token),
            ]
            if not value
        ]
        if missing:
            raise ValueError(
                f"Bot '{config.id}' (platform=mattermost) missing required fields: "
                + ", ".join(missing)
            )
    elif config.platform == "discord":
        missing = [
            field
            for field, value in [
                ("discord.token", config.discord.token),
            ]
            if not value
        ]
        if missing:
            raise ValueError(
                f"Bot '{config.id}' (platform=discord) missing required fields: "
                + ", ".join(missing)
            )
    else:
        raise ValueError(
            f"Bot '{config.id}' has unsupported platform: '{config.platform}'. "
            "Supported values: 'slack', 'mattermost', 'discord'."
        )


def load_bot_from_yaml(path: Path) -> BotConfig:
    """Load a single bot configuration from a YAML file."""
    with open(path) as f:
        data = yaml.safe_load(f)

    slack = data.get("slack", {})
    mattermost_data = data.get("mattermost", {})
    discord_data = data.get("discord", {})
    persona_data = data.get("persona", {})
    project_data = data.get("project", {})
    airflow_data = data.get("airflow", {})
    claude_data = data.get("claude", {})
    memory_data = data.get("memory", {})
    security_data = data.get("security", {})
    tools_data = data.get("tools", {})

    config = BotConfig(
        id=data["name"],
        name=data["name"],
        platform=data.get("platform", "slack"),
        slack_app_token=_resolve_env(slack.get("app_token", "")),
        slack_bot_token=_resolve_env(slack.get("bot_token", "")),
        mattermost=MattermostConfig(
            url=_resolve_env(mattermost_data.get("url", "")),
            token=_resolve_env(mattermost_data.get("token", "")),
            port=mattermost_data.get("port", 8065),
        ),
        discord=DiscordConfig(
            token=_resolve_env(discord_data.get("token", "")),
            guild_id=discord_data.get("guild_id", ""),
        ),
        channels=slack.get("channels", []),
        persona=PersonaConfig(
            display_name=persona_data.get("display_name", ""),
            description=persona_data.get("description", ""),
            personality=persona_data.get("personality", ""),
            response_prefix=persona_data.get("response_prefix", ""),
        ),
        project=ProjectConfig(
            repo_path=project_data.get("repo_path", ""),
            github_repo=project_data.get("github_repo", ""),
            github_token_var=project_data.get("github_token_var", ""),
        ),
        airflow=AirflowConfig(dag_prefix=airflow_data.get("dag_prefix", "")),
        tools_enabled=tools_data.get("enabled", []),
        claude=ClaudeConfig(
            provider=claude_data.get("provider", "claude"),
            model=claude_data.get("model", "sonnet"),
            max_turns=claude_data.get("max_turns", 10),
            full_agent=claude_data.get("full_agent", False),
        ),
        memory=MemoryConfig(
            context_window=memory_data.get("context_window", 20),
            auto_extract=memory_data.get("auto_extract", True),
        ),
        security=SecurityConfig(
            allowed_channels=security_data.get("allowed_channels", []),
            allowed_users=security_data.get("allowed_users", []),
            dangerous_tools=security_data.get("dangerous_tools", []),
            mention_only=security_data.get("mention_only", False),
        ),
    )
    _validate_bot_config(config)
    return config


def _as_dict(value) -> dict:
    if isinstance(value, dict):
        return value
    if not value:
        return {}
    if isinstance(value, str):
        return json.loads(value)
    return dict(value)


def _as_list(value) -> list:
    if isinstance(value, list):
        return value
    if not value:
        return []
    if isinstance(value, str):
        return json.loads(value)
    return list(value)


def load_bot_from_db_row(row) -> BotConfig:
    """Load a single bot configuration from a database row.

    Supports both old 12-column schema and new 15-column schema that
    adds platform, discord, and mattermost columns. Missing columns
    fall back to safe defaults for backward compatibility.
    """
    bot_id = row[0]
    name = row[1]
    slack_app_token = row[2]
    slack_bot_token = row[3]
    channels = row[4]
    persona_data = row[5]
    project_data = row[6]
    airflow_data = row[7]
    tools_data = row[8]
    claude_data = row[9]
    memory_data = row[10]
    security_data = row[11]
    platform = row[12] if len(row) > 12 else "slack"
    discord_data = row[13] if len(row) > 13 else {}
    mattermost_data = row[14] if len(row) > 14 else {}

    persona_data = _as_dict(persona_data)
    project_data = _as_dict(project_data)
    airflow_data = _as_dict(airflow_data)
    tools_data = _as_dict(tools_data)
    claude_data = _as_dict(claude_data)
    memory_data = _as_dict(memory_data)
    security_data = _as_dict(security_data)
    discord_data = _as_dict(discord_data)
    mattermost_data = _as_dict(mattermost_data)

    return BotConfig(
        id=bot_id,
        name=name,
        slack_app_token=_resolve_env(slack_app_token or ""),
        slack_bot_token=_resolve_env(slack_bot_token or ""),
        platform=platform or "slack",
        discord=DiscordConfig(
            token=_resolve_env(discord_data.get("token", "")),
            guild_id=discord_data.get("guild_id", ""),
        ),
        mattermost=MattermostConfig(
            url=_resolve_env(mattermost_data.get("url", "")),
            token=_resolve_env(mattermost_data.get("token", "")),
            port=mattermost_data.get("port", 8065),
        ),
        channels=_as_list(channels),
        persona=PersonaConfig(
            display_name=persona_data.get("display_name", name or bot_id or ""),
            description=persona_data.get("description", ""),
            personality=persona_data.get("personality", ""),
            response_prefix=persona_data.get("response_prefix", ""),
        ),
        project=ProjectConfig(
            repo_path=project_data.get("repo_path", ""),
            github_repo=project_data.get("github_repo", ""),
            github_token_var=project_data.get("github_token_var", ""),
        ),
        airflow=AirflowConfig(dag_prefix=airflow_data.get("dag_prefix", "")),
        tools_enabled=_as_list(tools_data.get("enabled", [])),
        claude=ClaudeConfig(
            provider=claude_data.get("provider", "claude"),
            model=claude_data.get("model", "sonnet"),
            max_turns=claude_data.get("max_turns", 10),
            full_agent=claude_data.get("full_agent", False),
        ),
        memory=MemoryConfig(
            context_window=memory_data.get("context_window", 20),
            auto_extract=memory_data.get("auto_extract", True),
        ),
        security=SecurityConfig(
            allowed_channels=_as_list(security_data.get("allowed_channels", [])),
            allowed_users=_as_list(security_data.get("allowed_users", [])),
            dangerous_tools=_as_list(security_data.get("dangerous_tools", [])),
        ),
    )


def load_all_bots_from_db() -> list[BotConfig]:
    """Load all active bot configurations from PostgreSQL."""
    dsn = os.environ.get("DATABASE_DSN", "")
    if not dsn:
        return []

    conn = psycopg2.connect(dsn)
    try:
        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT
                    id,
                    name,
                    slack_app_token,
                    slack_bot_token,
                    channels,
                    persona,
                    project,
                    airflow,
                    tools,
                    claude,
                    memory,
                    security,
                    platform,
                    discord,
                    mattermost
                FROM bots
                WHERE is_active = true
                ORDER BY created_at, id
                """
            )
            rows = cur.fetchall()
        configs = []
        for row in rows:
            try:
                configs.append(load_bot_from_db_row(row))
            except (ValueError, Exception) as e:
                print(f"[WARNING] Skipping bot {row[0]}: {e}")
        return configs
    finally:
        conn.close()


def load_all_bots(bots_dir: str = "/app/bots") -> list[BotConfig]:
    """Load all bot configurations, preferring PostgreSQL over YAML bootstrap."""
    configs_by_id: dict[str, BotConfig] = {}

    try:
        for config in load_all_bots_from_db():
            configs_by_id[config.id] = config
    except Exception as e:
        print(f"[WARNING] Failed to load bot configs from database: {e}")

    bots_path = Path(bots_dir)
    if bots_path.exists():
        for yaml_file in sorted(bots_path.glob("*.yaml")):
            try:
                config = load_bot_from_yaml(yaml_file)
                if config.id not in configs_by_id:
                    configs_by_id[config.id] = config
            except Exception as e:
                print(f"[WARNING] Failed to load bot config {yaml_file}: {e}")
    return list(configs_by_id.values())
