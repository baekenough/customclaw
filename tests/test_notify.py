"""Tests for bot_engine.notify — pluggable notification backends.

slack_sdk is stubbed by conftest.py so SlackNotifyBackend can always be
imported.  Each test that needs specific WebClient behaviour patches it
directly on the backend instance.
"""
import pytest
from unittest.mock import patch, MagicMock

from bot_engine.notify.base import NotifyBackend
from bot_engine.notify.log import LogNotifyBackend
from bot_engine.notify import create_backend
from bot_engine.notify.slack import SlackNotifyBackend


class TestNotifyBackend:
    """NotifyBackend is an abstract base class — cannot be instantiated."""

    def test_cannot_instantiate_abc(self):
        with pytest.raises(TypeError):
            NotifyBackend()


class TestLogNotifyBackend:
    """LogNotifyBackend is the no-op fallback."""

    def test_send_message_returns_empty_string(self):
        backend = LogNotifyBackend()
        result = backend.send_message("channel", "hello")
        assert result == ""

    def test_send_message_with_thread(self):
        backend = LogNotifyBackend()
        result = backend.send_message("ch", "text", thread_ts="123.456")
        assert result == ""

    def test_add_reaction_does_not_raise(self):
        backend = LogNotifyBackend()
        backend.add_reaction("ch", "123.456", "thumbsup")  # should not raise

    def test_default_channel_stored(self):
        backend = LogNotifyBackend(default_channel="C123")
        assert backend._default_channel == "C123"

    def test_default_channel_defaults_to_empty_string(self):
        backend = LogNotifyBackend()
        assert backend._default_channel == ""

    def test_send_message_unfurl_links_kwarg_accepted(self):
        """LogNotifyBackend accepts unfurl_links keyword arg per the ABC interface."""
        backend = LogNotifyBackend()
        result = backend.send_message("ch", "text", unfurl_links=True)
        assert result == ""

    def test_is_notify_backend_subclass(self):
        backend = LogNotifyBackend()
        assert isinstance(backend, NotifyBackend)


class TestSlackNotifyBackend:
    """SlackNotifyBackend wraps slack_sdk.WebClient.

    slack_sdk is provided by the conftest stub.  We patch WebClient methods on
    the backend instance to control return values without touching the real API.
    """

    def _make_backend(self, channel: str = "C123") -> SlackNotifyBackend:
        return SlackNotifyBackend(token="xoxb-test", default_channel=channel)

    def test_send_message_calls_web_client(self):
        backend = self._make_backend()
        mock_response = {"ok": True, "ts": "1234567890.123456"}
        with patch.object(
            backend._client, "chat_postMessage", return_value=mock_response
        ) as mock_post:
            result = backend.send_message("C123", "Hello world")
            mock_post.assert_called_once_with(
                channel="C123",
                text="Hello world",
                unfurl_links=False,
            )
            assert result == "1234567890.123456"

    def test_send_message_with_thread_ts(self):
        backend = self._make_backend()
        mock_response = {"ok": True, "ts": "111.222"}
        with patch.object(
            backend._client, "chat_postMessage", return_value=mock_response
        ) as mock_post:
            result = backend.send_message("C123", "Reply", thread_ts="000.111")
            mock_post.assert_called_once_with(
                channel="C123",
                text="Reply",
                unfurl_links=False,
                thread_ts="000.111",
            )
            assert result == "111.222"

    def test_send_message_uses_default_channel(self):
        backend = SlackNotifyBackend(token="xoxb-test", default_channel="C_DEFAULT")
        mock_response = {"ok": True, "ts": "111.222"}
        with patch.object(
            backend._client, "chat_postMessage", return_value=mock_response
        ) as mock_post:
            backend.send_message("", "Hello")  # empty channel → default
            mock_post.assert_called_once_with(
                channel="C_DEFAULT",
                text="Hello",
                unfurl_links=False,
            )

    def test_send_message_returns_empty_on_exception(self):
        backend = self._make_backend()
        with patch.object(
            backend._client, "chat_postMessage", side_effect=Exception("API error")
        ):
            result = backend.send_message("C123", "Hello")
            assert result == ""

    def test_send_message_returns_empty_when_not_ok(self):
        backend = self._make_backend()
        mock_response = {"ok": False, "error": "channel_not_found"}
        with patch.object(
            backend._client, "chat_postMessage", return_value=mock_response
        ):
            result = backend.send_message("C123", "Hello")
            assert result == ""

    def test_add_reaction_calls_web_client(self):
        backend = self._make_backend()
        with patch.object(backend._client, "reactions_add") as mock_react:
            backend.add_reaction("C123", "111.222", "thumbsup")
            mock_react.assert_called_once_with(
                channel="C123", name="thumbsup", timestamp="111.222"
            )

    def test_add_reaction_swallows_errors(self):
        backend = self._make_backend()
        with patch.object(
            backend._client, "reactions_add", side_effect=Exception("fail")
        ):
            backend.add_reaction("C123", "111.222", "thumbsup")  # should not raise

    def test_send_message_no_channel_returns_empty(self):
        """When both channel arg and default_channel are empty, return ''."""
        backend = SlackNotifyBackend(token="xoxb-test", default_channel="")
        result = backend.send_message("", "Hello")
        assert result == ""

    def test_is_notify_backend_subclass(self):
        backend = self._make_backend()
        assert isinstance(backend, NotifyBackend)

    def test_default_channel_stored(self):
        backend = SlackNotifyBackend(token="xoxb-test", default_channel="C_STORED")
        assert backend._default_channel == "C_STORED"


class TestCreateBackend:
    """create_backend() factory function."""

    def test_no_token_returns_log_backend(self):
        backend = create_backend(token="", channel="C123")
        assert isinstance(backend, LogNotifyBackend)

    def test_log_platform_returns_log_backend(self):
        """Explicitly requesting 'log' platform always gives LogNotifyBackend."""
        backend = create_backend(token="xoxb-test", channel="C123", platform="log")
        assert isinstance(backend, LogNotifyBackend)

    def test_with_token_and_slack_platform_returns_slack_backend(self):
        backend = create_backend(token="xoxb-test", channel="C123", platform="slack")
        assert isinstance(backend, SlackNotifyBackend)

    def test_default_platform_is_log(self):
        """Default platform parameter is 'log', so token alone is not enough."""
        backend = create_backend(token="xoxb-test", channel="C123")
        # platform defaults to "log" in create_backend signature
        assert isinstance(backend, LogNotifyBackend)

    def test_channel_passed_to_log_backend_as_default(self):
        backend = create_backend(token="", channel="C_DEFAULT")
        assert isinstance(backend, LogNotifyBackend)
        assert backend._default_channel == "C_DEFAULT"

    def test_channel_passed_to_slack_backend_as_default(self):
        backend = create_backend(
            token="xoxb-test", channel="C_DEFAULT", platform="slack"
        )
        assert isinstance(backend, SlackNotifyBackend)
        assert backend._default_channel == "C_DEFAULT"

    def test_empty_token_always_returns_log_backend(self):
        for platform in ("slack", "log", "discord"):
            backend = create_backend(token="", channel="C123", platform=platform)
            assert isinstance(backend, LogNotifyBackend), (
                f"Expected LogNotifyBackend for platform={platform!r} with empty token"
            )
