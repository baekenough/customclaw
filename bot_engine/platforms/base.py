"""Abstract base classes for platform adapters."""

from __future__ import annotations

import logging
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any

_log = logging.getLogger(__name__)


@dataclass
class MessageEvent:
    """Platform-agnostic message event.

    Normalises platform-specific message representations into a single
    structure consumed by the Redis Stream and worker layer.
    """

    platform: str
    """Platform identifier: ``"slack"`` or ``"mattermost"``."""

    channel_id: str
    """Channel or room identifier on the originating platform."""

    user_id: str
    """Identifier of the user who sent the message."""

    text: str
    """Plain-text body of the message."""

    thread_id: str | None
    """Thread root identifier.

    Slack: ``thread_ts``.  Mattermost: ``root_id``.
    ``None`` when the message starts a new thread.
    """

    message_id: str
    """Unique message identifier on the originating platform.

    Slack: ``ts``.  Mattermost: ``post_id``.
    """

    raw: dict[str, Any] | None = field(default=None)
    """Original, unmodified platform event payload."""


class PlatformAdapter(ABC):
    """Receives messages from a messaging platform and publishes to Redis Stream.

    Implementations connect to a specific platform (Slack, Mattermost, …),
    listen for incoming messages, and enqueue them onto the shared Redis
    Stream so that the worker layer can process them platform-agnostically.
    """

    @abstractmethod
    def start(self) -> None:
        """Start listening for messages.

        This call is *blocking*: it returns only after :meth:`stop` is
        called (from another thread) or an unrecoverable error occurs.
        """

    @abstractmethod
    def stop(self) -> None:
        """Gracefully stop the adapter and release platform connections."""

    @abstractmethod
    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a message.

        Args:
            channel_id: Channel containing the message.
            message_id: Platform-specific message identifier.
            emoji: Emoji name without surrounding colons (e.g. ``"thumbsup"``).
        """

    @abstractmethod
    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove an emoji reaction from a message.

        Args:
            channel_id: Channel containing the message.
            message_id: Platform-specific message identifier.
            emoji: Emoji name without surrounding colons.
        """


class ResponsePublisher(ABC):
    """Publishes responses back to a messaging platform.

    The worker layer calls this after processing a message from the Redis
    Stream.  Implementations translate the generic call into platform-
    specific API requests.
    """

    @abstractmethod
    def send_message(
        self,
        channel_id: str,
        text: str,
        thread_id: str | None = None,
    ) -> str:
        """Send a message and return the platform message ID.

        Args:
            channel_id: Destination channel.
            text: Message body.
            thread_id: Optional thread root to reply within.

        Returns:
            Platform-specific identifier of the sent message.
        """

    @abstractmethod
    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a message.

        Args:
            channel_id: Channel containing the message.
            message_id: Platform-specific message identifier.
            emoji: Emoji name without surrounding colons.
        """

    @abstractmethod
    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove an emoji reaction from a message.

        Args:
            channel_id: Channel containing the message.
            message_id: Platform-specific message identifier.
            emoji: Emoji name without surrounding colons.
        """

    @abstractmethod
    def get_thread_replies(
        self,
        channel_id: str,
        thread_id: str,
        limit: int = 10,
    ) -> list[dict[str, Any]]:
        """Retrieve replies in a thread.

        Args:
            channel_id: Channel containing the thread.
            thread_id: Thread root identifier.
            limit: Maximum number of replies to return.

        Returns:
            List of reply dicts, each with keys ``user_id``, ``text``,
            and ``ts`` (platform timestamp / message ID).
        """


class NoOpResponsePublisher(ResponsePublisher):
    """No-op publisher for unsupported or unavailable platforms.

    Logs a warning for every call but never raises, ensuring the worker
    pipeline degrades gracefully when a platform adapter is missing.
    """

    def __init__(self, platform: str = "unknown") -> None:
        self._platform = platform

    def send_message(
        self,
        channel_id: str,
        text: str,
        thread_id: str | None = None,
    ) -> str:
        _log.warning(
            "NoOp send_message: platform=%s channel=%s (message dropped)",
            self._platform,
            channel_id,
        )
        return ""

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        _log.warning(
            "NoOp add_reaction: platform=%s channel=%s",
            self._platform,
            channel_id,
        )

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        pass

    def get_thread_replies(
        self,
        channel_id: str,
        thread_id: str,
        limit: int = 10,
    ) -> list[dict[str, Any]]:
        return []
