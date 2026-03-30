"""Abstract base class for notification backends."""

from __future__ import annotations

from abc import ABC, abstractmethod


class NotifyBackend(ABC):
    """Platform-agnostic notification interface.

    Implementations handle message delivery to Slack, Discord, log, etc.
    All methods are best-effort: errors are logged, never raised.
    """

    @abstractmethod
    def send_message(
        self,
        channel: str,
        text: str,
        *,
        thread_ts: str = "",
        unfurl_links: bool = False,
    ) -> str:
        """Send a message. Returns message ID/timestamp, or "" on failure."""

    @abstractmethod
    def add_reaction(self, channel: str, timestamp: str, emoji: str) -> None:
        """Add an emoji reaction to a message."""
