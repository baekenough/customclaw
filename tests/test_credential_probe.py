"""Tests for bot_engine.credential_probe — notification backend integration."""
import os
from unittest.mock import patch, MagicMock

import pytest

import bot_engine.credential_probe as cp


def _reset_module_state():
    """Reset module-level cache and state between tests."""
    cp._alert_backend = None
    cp._previous_states.clear()


# ---------------------------------------------------------------------------
# _get_alert_backend
# ---------------------------------------------------------------------------


class TestAlertBackend:
    """_get_alert_backend should create the appropriate backend."""

    def setup_method(self):
        _reset_module_state()

    def test_creates_log_backend_without_token(self):
        with patch.dict(os.environ, {"CUSTOMCLAW_SLACK_BOT_TOKEN": ""}, clear=False):
            backend = cp._get_alert_backend()
            from bot_engine.notify.log import LogNotifyBackend

            assert isinstance(backend, LogNotifyBackend)

    def test_creates_slack_backend_with_token(self):
        try:
            from bot_engine.notify.slack import SlackNotifyBackend
        except ImportError:
            pytest.skip("slack-sdk not installed")

        with patch.dict(
            os.environ,
            {
                "CUSTOMCLAW_SLACK_BOT_TOKEN": "xoxb-test-token",
                "CREDENTIAL_ALERT_CHANNEL": "C_TEST",
            },
            clear=False,
        ):
            _reset_module_state()
            backend = cp._get_alert_backend()
            assert isinstance(backend, SlackNotifyBackend)

    def test_alert_backend_is_cached(self):
        """Second call must return the exact same object (no re-creation)."""
        with patch.dict(os.environ, {"CUSTOMCLAW_SLACK_BOT_TOKEN": ""}, clear=False):
            b1 = cp._get_alert_backend()
            b2 = cp._get_alert_backend()
            assert b1 is b2

    def test_cache_reset_creates_new_backend(self):
        with patch.dict(os.environ, {"CUSTOMCLAW_SLACK_BOT_TOKEN": ""}, clear=False):
            b1 = cp._get_alert_backend()
            _reset_module_state()
            b2 = cp._get_alert_backend()
            assert b1 is not b2


# ---------------------------------------------------------------------------
# _maybe_alert
# ---------------------------------------------------------------------------


class TestMaybeAlert:
    """_maybe_alert should only fire on non-error → error transitions."""

    def setup_method(self):
        _reset_module_state()

    def test_first_error_triggers_alert(self):
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "error", "auth failed")
            mock_alert.assert_called_once_with("test-provider", "auth failed")

    def test_ok_status_does_not_trigger_alert(self):
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "ok", None)
            mock_alert.assert_not_called()

    def test_unconfigured_status_does_not_trigger_alert(self):
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "unconfigured", None)
            mock_alert.assert_not_called()

    def test_error_to_error_does_not_retrigger_alert(self):
        """Repeated error state must not spam alerts."""
        cp._previous_states["test-provider"] = "error"
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "error", "still failing")
            mock_alert.assert_not_called()

    def test_ok_to_error_triggers_alert(self):
        cp._previous_states["test-provider"] = "ok"
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "error", "just broke")
            mock_alert.assert_called_once_with("test-provider", "just broke")

    def test_unconfigured_to_error_triggers_alert(self):
        """Transitioning from any non-error state to error must alert."""
        cp._previous_states["test-provider"] = "unconfigured"
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "error", "suddenly broken")
            mock_alert.assert_called_once()

    def test_ok_to_ok_does_not_trigger(self):
        cp._previous_states["test-provider"] = "ok"
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "ok", None)
            mock_alert.assert_not_called()

    def test_error_to_ok_recovery_does_not_trigger(self):
        """Recovering to 'ok' after an error must not send a second alert."""
        cp._previous_states["test-provider"] = "error"
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("test-provider", "ok", None)
            mock_alert.assert_not_called()

    def test_state_is_updated_after_call(self):
        """After _maybe_alert, the provider's state must reflect new status."""
        cp._maybe_alert("test-provider", "ok", None)
        assert cp._previous_states["test-provider"] == "ok"

        cp._maybe_alert("test-provider", "error", "broke")
        assert cp._previous_states["test-provider"] == "error"

    def test_independent_providers_tracked_separately(self):
        """Alerts for one provider must not affect another."""
        cp._previous_states["provider-a"] = "ok"
        # provider-b has no previous state
        with patch.object(cp, "_send_alert") as mock_alert:
            cp._maybe_alert("provider-a", "error", "a broke")
            cp._maybe_alert("provider-b", "ok", None)
            # Only provider-a triggered an alert
            mock_alert.assert_called_once_with("provider-a", "a broke")


# ---------------------------------------------------------------------------
# _send_alert
# ---------------------------------------------------------------------------


class TestSendAlert:
    """_send_alert delegates to the configured notification backend."""

    def setup_method(self):
        _reset_module_state()

    def test_send_alert_calls_backend_send_message(self):
        mock_backend = MagicMock()
        with patch.object(cp, "_get_alert_backend", return_value=mock_backend):
            cp._send_alert("openai", "invalid API key")
            mock_backend.send_message.assert_called_once()
            call_kwargs = mock_backend.send_message.call_args
            # channel="" means use default channel
            assert call_kwargs.kwargs.get("channel", call_kwargs.args[0] if call_kwargs.args else "") == ""
            # text should mention the provider and error
            text_arg = (
                call_kwargs.kwargs.get("text")
                or (call_kwargs.args[1] if len(call_kwargs.args) > 1 else "")
            )
            assert "openai" in text_arg
            assert "invalid API key" in text_arg


# ---------------------------------------------------------------------------
# _check_openai (unit — no real HTTP)
# ---------------------------------------------------------------------------


class TestCheckOpenAI:
    """_check_openai should handle various HTTP response scenarios."""

    def test_returns_unconfigured_when_no_key(self):
        env = {k: v for k, v in os.environ.items() if k != "OPENAI_API_KEY"}
        with patch.dict(os.environ, env, clear=True):
            status, error = cp._check_openai()
            assert status == "unconfigured"
            assert error is None

    def test_returns_ok_on_200(self):
        mock_resp = MagicMock()
        mock_resp.status_code = 200
        with patch.dict(os.environ, {"OPENAI_API_KEY": "sk-test"}, clear=False):
            with patch("bot_engine.credential_probe.requests.get", return_value=mock_resp):
                status, error = cp._check_openai()
                assert status == "ok"
                assert error is None

    def test_returns_error_on_401(self):
        mock_resp = MagicMock()
        mock_resp.status_code = 401
        mock_resp.json.return_value = {
            "error": {"message": "Incorrect API key"}
        }
        with patch.dict(os.environ, {"OPENAI_API_KEY": "sk-bad"}, clear=False):
            with patch("bot_engine.credential_probe.requests.get", return_value=mock_resp):
                status, error = cp._check_openai()
                assert status == "error"
                assert error == "Incorrect API key"

    def test_returns_error_on_timeout(self):
        import requests as req

        with patch.dict(os.environ, {"OPENAI_API_KEY": "sk-test"}, clear=False):
            with patch(
                "bot_engine.credential_probe.requests.get",
                side_effect=req.Timeout(),
            ):
                status, error = cp._check_openai()
                assert status == "error"
                assert "timed out" in error.lower()


# ---------------------------------------------------------------------------
# _check_gemini (unit — no real HTTP)
# ---------------------------------------------------------------------------


class TestCheckGemini:
    """_check_gemini should handle various HTTP response scenarios."""

    def test_returns_unconfigured_when_no_key(self):
        env = {k: v for k, v in os.environ.items() if k != "GEMINI_API_KEY"}
        with patch.dict(os.environ, env, clear=True):
            status, error = cp._check_gemini()
            assert status == "unconfigured"
            assert error is None

    def test_returns_ok_on_200(self):
        mock_resp = MagicMock()
        mock_resp.status_code = 200
        with patch.dict(os.environ, {"GEMINI_API_KEY": "AIza-test"}, clear=False):
            with patch("bot_engine.credential_probe.requests.get", return_value=mock_resp):
                status, error = cp._check_gemini()
                assert status == "ok"
                assert error is None

    def test_returns_error_on_403(self):
        mock_resp = MagicMock()
        mock_resp.status_code = 403
        mock_resp.json.return_value = {"error": {"message": "API key invalid"}}
        with patch.dict(os.environ, {"GEMINI_API_KEY": "AIza-bad"}, clear=False):
            with patch("bot_engine.credential_probe.requests.get", return_value=mock_resp):
                status, error = cp._check_gemini()
                assert status == "error"
                assert error == "API key invalid"

    def test_returns_error_on_timeout(self):
        import requests as req

        with patch.dict(os.environ, {"GEMINI_API_KEY": "AIza-test"}, clear=False):
            with patch(
                "bot_engine.credential_probe.requests.get",
                side_effect=req.Timeout(),
            ):
                status, error = cp._check_gemini()
                assert status == "error"
                assert "timed out" in error.lower()
