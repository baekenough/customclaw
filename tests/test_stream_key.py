"""Tests for stream key consistency across the codebase.

The canonical stream key is defined once in bot_engine.platforms.base and
must be mirrored exactly in every platform adapter and the worker.
A mismatch would cause messages to be published to a different stream than
the worker is reading from — a silent data-loss bug.
"""
import pytest


class TestStreamKeyConsistency:
    """All platform adapters and the worker must use the same stream key."""

    def test_platform_stream_key_defined(self):
        from bot_engine.platforms.base import PLATFORM_STREAM_KEY

        assert PLATFORM_STREAM_KEY == "customclaw:platform-messages"

    def test_worker_stream_key_matches(self):
        from bot_engine.worker import STREAM_KEY
        from bot_engine.platforms.base import PLATFORM_STREAM_KEY

        assert STREAM_KEY == PLATFORM_STREAM_KEY

    def test_slack_adapter_stream_key_matches(self):
        try:
            from bot_engine.platforms.slack_adapter import _STREAM_KEY
            from bot_engine.platforms.base import PLATFORM_STREAM_KEY

            assert _STREAM_KEY == PLATFORM_STREAM_KEY
        except ImportError:
            pytest.skip("slack-bolt not installed")

    def test_discord_adapter_stream_key_matches(self):
        try:
            from bot_engine.platforms.discord_adapter import _STREAM_KEY
            from bot_engine.platforms.base import PLATFORM_STREAM_KEY

            assert _STREAM_KEY == PLATFORM_STREAM_KEY
        except ImportError:
            pytest.skip("discord.py not installed")

    def test_mattermost_adapter_stream_key_matches(self):
        try:
            from bot_engine.platforms.mattermost_adapter import _STREAM_KEY
            from bot_engine.platforms.base import PLATFORM_STREAM_KEY

            assert _STREAM_KEY == PLATFORM_STREAM_KEY
        except ImportError:
            pytest.skip("mattermostdriver not installed")

    def test_stream_key_not_slack_prefixed(self):
        """Stream key must NOT contain 'slack' in its name — it is platform-agnostic."""
        from bot_engine.platforms.base import PLATFORM_STREAM_KEY

        assert "slack" not in PLATFORM_STREAM_KEY

    def test_stream_key_uses_platform_namespace(self):
        """Stream key must use the project namespace, not a vendor namespace."""
        from bot_engine.platforms.base import PLATFORM_STREAM_KEY

        assert PLATFORM_STREAM_KEY.startswith("customclaw:")

    def test_all_adapters_use_identical_literal(self):
        """Cross-verify: all known _STREAM_KEY values are the same string."""
        expected = "customclaw:platform-messages"
        keys = {}

        try:
            from bot_engine.platforms.slack_adapter import _STREAM_KEY as slack_key

            keys["slack"] = slack_key
        except ImportError:
            pass

        try:
            from bot_engine.platforms.discord_adapter import _STREAM_KEY as discord_key

            keys["discord"] = discord_key
        except ImportError:
            pass

        try:
            from bot_engine.platforms.mattermost_adapter import (
                _STREAM_KEY as mm_key,
            )

            keys["mattermost"] = mm_key
        except ImportError:
            pass

        for adapter, key in keys.items():
            assert key == expected, (
                f"Adapter '{adapter}' uses stream key '{key}', "
                f"expected '{expected}'"
            )
