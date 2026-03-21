"""Slack platform adapter using Slack Bolt and slack-sdk."""

from __future__ import annotations

import logging
from typing import Any

import redis
from slack_bolt import App
from slack_bolt.adapter.socket_mode import SocketModeHandler
from slack_sdk import WebClient

from slack_bot.config.loader import BotConfig
from slack_bot.platforms.base import PlatformAdapter, ResponsePublisher

log = logging.getLogger(__name__)

_STREAM_KEY = "customclaw:slack-messages"
_HOURGLASS = "hourglass_flowing_sand"


class SlackAdapter(PlatformAdapter):
    """Receives messages from Slack via Socket Mode and publishes to Redis Stream.

    A single :class:`SlackAdapter` instance can handle multiple
    :class:`~slack_bot.config.loader.BotConfig` objects that share the
    same ``slack_app_token``.  The adapter routes each incoming message to
    the most specific config (channel-scoped first, then default) before
    publishing to the stream.

    Args:
        configs: One or more :class:`BotConfig` instances sharing the
            same ``slack_app_token``.
        redis_client: Connected Redis client used to publish messages.
    """

    def __init__(
        self,
        configs: list[BotConfig],
        redis_client: redis.Redis,
    ) -> None:
        if not configs:
            raise ValueError("At least one BotConfig is required")

        self._configs = configs
        self._redis = redis_client

        # All configs in a group share the same tokens
        self._bot_token: str = configs[0].slack_bot_token
        self._app_token: str = configs[0].slack_app_token

        # channel_id → config (channel-scoped bots take priority)
        self._channel_map: dict[str, BotConfig] = {}
        self._default_config: BotConfig = configs[0]

        for config in configs:
            if config.security.allowed_channels:
                for channel in config.security.allowed_channels:
                    self._channel_map[channel] = config
            else:
                self._default_config = config

        self._app = App(token=self._bot_token)
        self._client = WebClient(token=self._bot_token)
        self._handler: SocketModeHandler | None = None

        self._setup_handlers()

        bot_names = [c.id for c in configs]
        log.info(
            "SlackAdapter initialised for %d bot(s): %s",
            len(configs),
            bot_names,
        )

    # ------------------------------------------------------------------
    # PlatformAdapter interface
    # ------------------------------------------------------------------

    def start(self) -> None:
        """Start Socket Mode and block until :meth:`stop` is called."""
        self._handler = SocketModeHandler(self._app, self._app_token)
        self._handler.start()

    def stop(self) -> None:
        """Close the Socket Mode connection."""
        if self._handler is not None:
            try:
                self._handler.close()
            except Exception:
                log.exception("Error closing SlackAdapter SocketModeHandler")
            finally:
                self._handler = None

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a Slack message.

        Args:
            channel_id: Slack channel ID.
            message_id: Message timestamp (``ts``).
            emoji: Emoji name without colons (e.g. ``"white_check_mark"``).
        """
        try:
            self._client.reactions_add(
                channel=channel_id,
                name=emoji,
                timestamp=message_id,
            )
        except Exception:
            log.exception(
                "Failed to add reaction :%s: to message %s in %s",
                emoji,
                message_id,
                channel_id,
            )

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove an emoji reaction from a Slack message.

        Args:
            channel_id: Slack channel ID.
            message_id: Message timestamp (``ts``).
            emoji: Emoji name without colons.
        """
        try:
            self._client.reactions_remove(
                channel=channel_id,
                name=emoji,
                timestamp=message_id,
            )
        except Exception:
            log.exception(
                "Failed to remove reaction :%s: from message %s in %s",
                emoji,
                message_id,
                channel_id,
            )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _resolve_config(self, channel_id: str) -> BotConfig | None:
        """Return the most-specific :class:`BotConfig` for *channel_id*."""
        return self._channel_map.get(channel_id, self._default_config)

    def _setup_handlers(self) -> None:
        """Register Slack Bolt event handlers on the internal :class:`App`."""

        @self._app.event("message")
        def handle_message(event, say):  # noqa: ARG001
            # Ignore edited/deleted/bot messages
            if event.get("subtype"):
                return

            channel_id: str = event.get("channel", "")
            user_id: str = event.get("user", "")
            text: str = event.get("text", "")
            thread_ts: str = event.get("thread_ts") or event.get("ts", "")
            message_ts: str = event.get("ts", "")

            config = self._resolve_config(channel_id)
            if config is None:
                return

            # Enforce channel allow-list for the resolved config
            if (
                config.security.allowed_channels
                and channel_id not in config.security.allowed_channels
            ):
                return

            # Enforce user allow-list
            if (
                config.security.allowed_users
                and user_id not in config.security.allowed_users
            ):
                return

            # Optimistic hourglass reaction while the worker processes
            try:
                self._client.reactions_add(
                    channel=channel_id,
                    name=_HOURGLASS,
                    timestamp=message_ts,
                )
            except Exception:
                pass  # Non-critical; proceed regardless

            # Publish to Redis Stream
            message_data: dict[str, str] = {
                "bot_id": config.id,
                "channel_id": channel_id,
                "thread_ts": thread_ts,
                "user_id": user_id,
                "text": text,
                "message_ts": message_ts,
                "bot_token": config.slack_bot_token,
                "platform": "slack",
            }
            self._redis.xadd(
                _STREAM_KEY,
                # Drop empty strings to keep the stream payload compact
                {k: v for k, v in message_data.items() if v},
            )
            log.info(
                "Published Slack message from user=%s channel=%s bot=%s",
                user_id,
                channel_id,
                config.id,
            )


class SlackResponsePublisher(ResponsePublisher):
    """Publishes responses back to Slack via the Web API.

    Args:
        bot_token: Slack bot OAuth token (``xoxb-…``).
    """

    def __init__(self, bot_token: str) -> None:
        self._client = WebClient(token=bot_token)

    # ------------------------------------------------------------------
    # ResponsePublisher interface
    # ------------------------------------------------------------------

    def send_message(
        self,
        channel_id: str,
        text: str,
        thread_id: str | None = None,
    ) -> str:
        """Post a message to Slack and return its timestamp.

        Args:
            channel_id: Slack channel ID.
            text: Message body (supports Slack markdown).
            thread_id: ``thread_ts`` to reply within an existing thread.

        Returns:
            The ``ts`` of the newly posted message.
        """
        kwargs: dict[str, Any] = {"channel": channel_id, "text": text}
        if thread_id:
            kwargs["thread_ts"] = thread_id

        response = self._client.chat_postMessage(**kwargs)
        return response["ts"]

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a Slack message.

        Args:
            channel_id: Slack channel ID.
            message_id: Message timestamp (``ts``).
            emoji: Emoji name without colons.
        """
        self._client.reactions_add(
            channel=channel_id,
            name=emoji,
            timestamp=message_id,
        )

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove an emoji reaction from a Slack message.

        Args:
            channel_id: Slack channel ID.
            message_id: Message timestamp (``ts``).
            emoji: Emoji name without colons.
        """
        self._client.reactions_remove(
            channel=channel_id,
            name=emoji,
            timestamp=message_id,
        )

    def get_thread_replies(
        self,
        channel_id: str,
        thread_id: str,
        limit: int = 10,
    ) -> list[dict[str, Any]]:
        """Retrieve replies in a Slack thread.

        Args:
            channel_id: Slack channel containing the thread.
            thread_id: ``thread_ts`` of the root message.
            limit: Maximum number of replies to return.

        Returns:
            List of dicts with keys ``user_id``, ``text``, and ``ts``.
        """
        response = self._client.conversations_replies(
            channel=channel_id,
            ts=thread_id,
            limit=limit,
        )
        return [
            {
                "user_id": msg.get("user", ""),
                "text": msg.get("text", ""),
                "ts": msg.get("ts", ""),
            }
            for msg in response.get("messages", [])
        ]
