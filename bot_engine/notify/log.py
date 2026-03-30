"""Log-only notification backend (no-op fallback)."""

from __future__ import annotations

import logging

from bot_engine.notify.base import NotifyBackend

log = logging.getLogger(__name__)


class LogNotifyBackend(NotifyBackend):
    """Logs messages instead of sending them. Used when no platform is configured."""

    def __init__(self, default_channel: str = "") -> None:
        self._default_channel = default_channel

    def send_message(
        self,
        channel: str,
        text: str,
        *,
        thread_ts: str = "",
        unfurl_links: bool = False,
    ) -> str:
        log.info(
            "[notify:log] channel=%s text=%s",
            channel or self._default_channel or "(none)",
            text[:200],
        )
        return ""

    def add_reaction(self, channel: str, timestamp: str, emoji: str) -> None:
        log.debug("[notify:log] reaction=%s ts=%s", emoji, timestamp)
