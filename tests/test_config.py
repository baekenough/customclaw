"""Tests for bot_engine.config.loader — BotConfig hexagonal decoupling."""
import json
import os
from pathlib import Path
from unittest.mock import patch

import pytest

from bot_engine.config.loader import (
    AirflowConfig,
    BotConfig,
    ClaudeConfig,
    DiscordConfig,
    MemoryConfig,
    MattermostConfig,
    PersonaConfig,
    ProjectConfig,
    SecurityConfig,
    _resolve_env,
    _validate_bot_config,
    _as_dict,
    _as_list,
    load_bot_from_db_row,
)


# ---------------------------------------------------------------------------
# BotConfig defaults
# ---------------------------------------------------------------------------


class TestBotConfigDefaults:
    """BotConfig fields should all have sensible defaults."""

    def test_all_fields_have_defaults(self):
        """BotConfig can be instantiated with no arguments."""
        config = BotConfig()
        assert config.id == ""
        assert config.name == ""
        assert config.slack_app_token == ""
        assert config.slack_bot_token == ""
        assert config.platform == "slack"

    def test_platform_defaults_to_slack(self):
        config = BotConfig()
        assert config.platform == "slack"

    def test_slack_tokens_optional(self):
        """Slack tokens default to empty — not required for non-Slack platforms."""
        config = BotConfig(id="test-bot", name="Test", platform="discord")
        assert config.slack_app_token == ""
        assert config.slack_bot_token == ""

    def test_backward_compatible_with_keyword_args(self):
        """Existing code using keyword args should still work."""
        config = BotConfig(
            id="mybot",
            name="My Bot",
            slack_app_token="xapp-123",
            slack_bot_token="xoxb-456",
            platform="slack",
        )
        assert config.id == "mybot"
        assert config.name == "My Bot"
        assert config.slack_app_token == "xapp-123"
        assert config.slack_bot_token == "xoxb-456"

    def test_discord_config_without_slack(self):
        """Discord bot should not need Slack tokens."""
        config = BotConfig(
            id="discord-bot",
            name="Discord Bot",
            platform="discord",
            discord=DiscordConfig(token="discord-token-123"),
        )
        assert config.platform == "discord"
        assert config.discord.token == "discord-token-123"
        assert config.slack_app_token == ""
        assert config.slack_bot_token == ""

    def test_mattermost_config_without_slack(self):
        """Mattermost bot should not need Slack tokens."""
        config = BotConfig(
            id="mm-bot",
            name="MM Bot",
            platform="mattermost",
            mattermost=MattermostConfig(
                url="https://mm.example.com", token="mm-token"
            ),
        )
        assert config.platform == "mattermost"
        assert config.mattermost.token == "mm-token"
        assert config.slack_app_token == ""

    def test_nested_dataclass_defaults_are_isolated(self):
        """Mutable default factories must not share state between instances."""
        a = BotConfig()
        b = BotConfig()
        a.channels.append("C_TEST")
        assert "C_TEST" not in b.channels

    def test_tools_enabled_defaults_to_empty_list(self):
        config = BotConfig()
        assert config.tools_enabled == []

    def test_discord_config_defaults(self):
        config = DiscordConfig()
        assert config.token == ""
        assert config.guild_id == ""

    def test_mattermost_config_defaults(self):
        config = MattermostConfig()
        assert config.url == ""
        assert config.token == ""
        assert config.port == 8065


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------


class TestBotConfigValidation:
    """_validate_bot_config enforces platform-specific requirements."""

    def test_slack_missing_both_tokens_raises(self):
        config = BotConfig(id="test", name="test", platform="slack")
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_slack_missing_app_token_raises(self):
        config = BotConfig(
            id="test", name="test", platform="slack", slack_bot_token="xoxb-1"
        )
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_slack_missing_bot_token_raises(self):
        config = BotConfig(
            id="test", name="test", platform="slack", slack_app_token="xapp-1"
        )
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_slack_with_both_tokens_passes(self):
        config = BotConfig(
            id="test",
            name="test",
            platform="slack",
            slack_app_token="xapp-1",
            slack_bot_token="xoxb-1",
        )
        _validate_bot_config(config)  # should not raise

    def test_discord_missing_token_raises(self):
        config = BotConfig(id="test", name="test", platform="discord")
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_discord_with_token_passes(self):
        config = BotConfig(
            id="test",
            name="test",
            platform="discord",
            discord=DiscordConfig(token="discord-tok"),
        )
        _validate_bot_config(config)  # should not raise

    def test_mattermost_missing_url_and_token_raises(self):
        config = BotConfig(id="test", name="test", platform="mattermost")
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_mattermost_missing_url_raises(self):
        config = BotConfig(
            id="test",
            name="test",
            platform="mattermost",
            mattermost=MattermostConfig(token="tok"),
        )
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_mattermost_missing_token_raises(self):
        config = BotConfig(
            id="test",
            name="test",
            platform="mattermost",
            mattermost=MattermostConfig(url="https://mm.example.com"),
        )
        with pytest.raises(ValueError, match="missing required fields"):
            _validate_bot_config(config)

    def test_mattermost_with_url_and_token_passes(self):
        config = BotConfig(
            id="test",
            name="test",
            platform="mattermost",
            mattermost=MattermostConfig(
                url="https://mm.example.com", token="mm-tok"
            ),
        )
        _validate_bot_config(config)  # should not raise

    def test_unsupported_platform_raises(self):
        config = BotConfig(id="test", name="test", platform="telegram")
        with pytest.raises(ValueError, match="unsupported platform"):
            _validate_bot_config(config)

    def test_validation_error_includes_bot_id(self):
        config = BotConfig(id="mybot-123", name="test", platform="slack")
        with pytest.raises(ValueError, match="mybot-123"):
            _validate_bot_config(config)


# ---------------------------------------------------------------------------
# _resolve_env
# ---------------------------------------------------------------------------


class TestResolveEnv:
    """_resolve_env substitutes ${ENV_VAR} references."""

    def test_resolves_env_var(self):
        with patch.dict(os.environ, {"MY_TOKEN": "secret-value"}):
            assert _resolve_env("${MY_TOKEN}") == "secret-value"

    def test_missing_env_var_returns_empty_string(self):
        key = "DEFINITELY_NOT_SET_XYZ_12345"
        os.environ.pop(key, None)
        assert _resolve_env(f"${{{key}}}") == ""

    def test_plain_string_returned_unchanged(self):
        assert _resolve_env("xoxb-1234") == "xoxb-1234"

    def test_partial_template_returned_unchanged(self):
        # Only exact ${...} is substituted
        assert _resolve_env("prefix-${TOKEN") == "prefix-${TOKEN"

    def test_non_string_returned_unchanged(self):
        assert _resolve_env(123) == 123  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# _as_dict / _as_list
# ---------------------------------------------------------------------------


class TestAsDictAslist:
    """Utility converters used during DB row loading."""

    def test_as_dict_with_dict(self):
        d = {"key": "value"}
        assert _as_dict(d) == d

    def test_as_dict_with_json_string(self):
        assert _as_dict('{"key": "value"}') == {"key": "value"}

    def test_as_dict_with_none(self):
        assert _as_dict(None) == {}

    def test_as_dict_with_empty_string(self):
        assert _as_dict("") == {}

    def test_as_list_with_list(self):
        lst = ["a", "b"]
        assert _as_list(lst) == lst

    def test_as_list_with_json_string(self):
        assert _as_list('["a", "b"]') == ["a", "b"]

    def test_as_list_with_none(self):
        assert _as_list(None) == []

    def test_as_list_with_empty_string(self):
        assert _as_list("") == []


# ---------------------------------------------------------------------------
# load_bot_from_db_row
# ---------------------------------------------------------------------------


def _make_row(
    bot_id="test-bot",
    name="Test Bot",
    slack_app_token="xapp-1",
    slack_bot_token="xoxb-1",
    channels=None,
    persona=None,
    project=None,
    airflow=None,
    tools=None,
    claude=None,
    memory=None,
    security=None,
    platform="slack",
    discord=None,
    mattermost=None,
):
    """Build a synthetic DB row tuple matching the 15-column schema."""
    return (
        bot_id,
        name,
        slack_app_token,
        slack_bot_token,
        channels or [],
        persona or {},
        project or {},
        airflow or {},
        tools or {},
        claude or {},
        memory or {},
        security or {},
        platform,
        discord or {},
        mattermost or {},
    )


class TestLoadBotFromDbRow:
    """load_bot_from_db_row parses 12- and 15-column schemas."""

    def test_full_15_column_row(self):
        row = _make_row()
        config = load_bot_from_db_row(row)
        assert config.id == "test-bot"
        assert config.name == "Test Bot"
        assert config.platform == "slack"
        assert config.slack_app_token == "xapp-1"
        assert config.slack_bot_token == "xoxb-1"

    def test_backward_compat_12_column_row(self):
        """12-column rows (pre-hexagonal) should default platform to 'slack'."""
        row_12 = (
            "legacy-bot",
            "Legacy",
            "xapp-2",
            "xoxb-2",
            [],
            {},
            {},
            {},
            {},
            {},
            {},
            {},
        )
        config = load_bot_from_db_row(row_12)
        assert config.platform == "slack"
        assert config.id == "legacy-bot"

    def test_discord_row_parsed(self):
        row = _make_row(
            platform="discord",
            slack_app_token="",
            slack_bot_token="",
            discord={"token": "disc-tok", "guild_id": "G123"},
        )
        config = load_bot_from_db_row(row)
        assert config.platform == "discord"
        assert config.discord.token == "disc-tok"
        assert config.discord.guild_id == "G123"

    def test_mattermost_row_parsed(self):
        row = _make_row(
            platform="mattermost",
            slack_app_token="",
            slack_bot_token="",
            mattermost={"url": "https://mm.example.com", "token": "mm-tok", "port": 443},
        )
        config = load_bot_from_db_row(row)
        assert config.platform == "mattermost"
        assert config.mattermost.url == "https://mm.example.com"
        assert config.mattermost.token == "mm-tok"
        assert config.mattermost.port == 443

    def test_channels_parsed_from_list(self):
        row = _make_row(channels=["C_ABC", "C_DEF"])
        config = load_bot_from_db_row(row)
        assert config.channels == ["C_ABC", "C_DEF"]

    def test_channels_parsed_from_json_string(self):
        row = _make_row(channels='["C_ABC", "C_DEF"]')
        config = load_bot_from_db_row(row)
        assert config.channels == ["C_ABC", "C_DEF"]

    def test_claude_config_parsed(self):
        row = _make_row(
            claude={"provider": "codex", "model": "opus", "max_turns": 5, "full_agent": True}
        )
        config = load_bot_from_db_row(row)
        assert config.claude.provider == "codex"
        assert config.claude.model == "opus"
        assert config.claude.max_turns == 5
        assert config.claude.full_agent is True

    def test_security_config_parsed(self):
        row = _make_row(
            security={
                "allowed_channels": ["C_SEC"],
                "mention_only": True,
                "dangerous_tools": ["bash"],
            }
        )
        config = load_bot_from_db_row(row)
        assert config.security.allowed_channels == ["C_SEC"]
        assert config.security.mention_only is True
        assert config.security.dangerous_tools == ["bash"]

    def test_env_var_resolved_in_tokens(self):
        with patch.dict(os.environ, {"MY_APP_TOKEN": "xapp-resolved"}):
            row = _make_row(slack_app_token="${MY_APP_TOKEN}")
            config = load_bot_from_db_row(row)
            assert config.slack_app_token == "xapp-resolved"

    def test_none_platform_defaults_to_slack(self):
        row = _make_row(platform=None)
        config = load_bot_from_db_row(row)
        assert config.platform == "slack"
