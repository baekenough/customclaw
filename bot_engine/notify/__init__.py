"""Pluggable notification backends for alerting and messaging."""

from bot_engine.notify.base import NotifyBackend
from bot_engine.notify.log import LogNotifyBackend

__all__ = ["NotifyBackend", "LogNotifyBackend", "create_backend"]


def create_backend(
    token: str = "", channel: str = "", platform: str = "log"
) -> NotifyBackend:
    """Create the appropriate notification backend.

    When a Slack token is provided, returns a SlackNotifyBackend.
    Otherwise, returns LogNotifyBackend (safe no-op fallback).
    """
    if token and platform != "log":
        try:
            from bot_engine.notify.slack import SlackNotifyBackend

            return SlackNotifyBackend(token=token, default_channel=channel)
        except ImportError:
            import logging

            logging.getLogger(__name__).warning(
                "slack-sdk not installed; falling back to log-only notifications"
            )
    return LogNotifyBackend(default_channel=channel)
