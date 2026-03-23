"""Mattermost platform adapter using python-mattermost-driver.

Install the optional dependency with::

    pip install mattermostdriver

The adapter publishes messages to the same Redis Stream as
:class:`~bot_engine.platforms.slack_adapter.SlackAdapter` so that the
worker layer processes Slack and Mattermost messages identically.
"""

from __future__ import annotations

import json
import logging
import threading
from typing import Any

import redis

from bot_engine.platforms.base import PlatformAdapter, ResponsePublisher

log = logging.getLogger(__name__)

_STREAM_KEY = "customclaw:slack-messages"
_HOURGLASS = "hourglass_flowing_sand"


def _parse_mm_url(url: str) -> tuple[str, str]:
    """Extract scheme and bare hostname from a Mattermost URL.

    Returns:
        A ``(scheme, hostname)`` tuple where *scheme* is ``'https'``
        (default) or ``'http'``.

    Examples::

        >>> _parse_mm_url("https://mattermost.example.com")
        ('https', 'mattermost.example.com')
        >>> _parse_mm_url("http://localhost")
        ('http', 'localhost')
        >>> _parse_mm_url("mattermost.example.com")
        ('https', 'mattermost.example.com')
    """
    if url.startswith("http://"):
        return "http", url[len("http://"):].rstrip("/")
    if url.startswith("https://"):
        return "https", url[len("https://"):].rstrip("/")
    return "https", url.rstrip("/")  # Default to https


def _build_driver_options(config: Any) -> dict[str, Any]:
    """Construct the ``mattermostdriver.Driver`` options dict from a BotConfig.

    Args:
        config: A :class:`~bot_engine.config.loader.BotConfig` instance.

    Returns:
        Options dictionary accepted by ``mattermostdriver.Driver``.
    """
    url: str = config.mattermost.url
    scheme, hostname = _parse_mm_url(url)
    port: int = config.mattermost.port or 8065
    return {
        "url": hostname,
        "token": config.mattermost.token,
        "scheme": scheme,
        "port": port,
        "basepath": "/api/v4",
        "verify": True,
        "keepalive": True,
        "keepalive_delay": 5,
    }


class MattermostAdapter(PlatformAdapter):
    """Receives messages from Mattermost via WebSocket and publishes to Redis Stream.

    One :class:`MattermostAdapter` manages all configs whose
    ``platform`` field equals ``"mattermost"``.  Each config spawns its
    own driver + WebSocket thread so that bots on different Mattermost
    servers can coexist.

    Args:
        configs: List of :class:`~bot_engine.config.loader.BotConfig`
            instances for Mattermost bots.
        redis_client: Connected Redis client used to publish messages.
    """

    def __init__(
        self,
        configs: list[Any],
        redis_client: redis.Redis,
    ) -> None:
        from mattermostdriver import Driver  # noqa: PLC0415  (optional dep)

        if not configs:
            raise ValueError("At least one BotConfig is required")

        self._configs = configs
        self._redis = redis_client
        self._running = False

        # bot_id → Driver
        self._drivers: dict[str, Driver] = {}
        # channel_id → config (channel-scoped routing)
        self._channel_map: dict[str, Any] = {}

        for config in configs:
            driver = Driver(_build_driver_options(config))
            self._drivers[config.id] = driver

            for channel in (config.security.allowed_channels or []):
                self._channel_map[channel] = config

        bot_names = [c.id for c in configs]
        log.info(
            "MattermostAdapter initialised for %d bot(s): %s",
            len(configs),
            bot_names,
        )

    # ------------------------------------------------------------------
    # PlatformAdapter interface
    # ------------------------------------------------------------------

    def start(self) -> None:
        """Login all drivers, start per-driver WebSocket threads, and block."""
        self._running = True
        threads: list[threading.Thread] = []

        for bot_id, driver in self._drivers.items():
            driver.login()
            config = next(c for c in self._configs if c.id == bot_id)
            thread = threading.Thread(
                target=self._run_websocket,
                args=(driver, config),
                name=f"mattermost-ws-{bot_id}",
                daemon=True,
            )
            thread.start()
            threads.append(thread)
            log.info("Started Mattermost WebSocket for bot: %s", bot_id)

        # Block on the first thread (mirrors SocketModeHandler.start())
        if threads:
            threads[0].join()

    def stop(self) -> None:
        """Disconnect all Mattermost WebSocket connections."""
        self._running = False
        for bot_id, driver in self._drivers.items():
            try:
                driver.disconnect()
            except Exception:
                log.exception("Error disconnecting Mattermost driver for bot: %s", bot_id)

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a Mattermost post.

        Iterates over drivers until one succeeds (handles multi-server setups).

        Args:
            channel_id: Unused for Mattermost reactions (kept for interface compatibility).
            message_id: Mattermost post ID.
            emoji: Emoji name without colons.
        """
        for bot_id, driver in self._drivers.items():
            try:
                driver.reactions.create_reaction(
                    options={
                        "user_id": driver.client.userid,
                        "post_id": message_id,
                        "emoji_name": emoji,
                    }
                )
                return
            except Exception:
                log.debug(
                    "Driver %s could not add reaction to post %s; trying next",
                    bot_id,
                    message_id,
                )

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove an emoji reaction from a Mattermost post.

        Args:
            channel_id: Unused for Mattermost reactions (kept for interface compatibility).
            message_id: Mattermost post ID.
            emoji: Emoji name without colons.
        """
        for bot_id, driver in self._drivers.items():
            try:
                driver.reactions.delete_reaction(
                    user_id=driver.client.userid,
                    post_id=message_id,
                    emoji_name=emoji,
                )
                return
            except Exception:
                log.debug(
                    "Driver %s could not remove reaction from post %s; trying next",
                    bot_id,
                    message_id,
                )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _run_websocket(self, driver: Any, config: Any) -> None:
        """Run the blocking WebSocket loop for a single driver.

        Each thread gets its own event loop to prevent cross-thread
        asyncio contention when multiple bots connect simultaneously.

        Args:
            driver: Logged-in ``mattermostdriver.Driver`` instance.
            config: Corresponding :class:`~bot_engine.config.loader.BotConfig`.
        """
        import asyncio

        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)

        async def _on_message(raw_message: str) -> None:
            try:
                data = json.loads(raw_message)
            except json.JSONDecodeError:
                return

            if data.get("event") != "posted":
                return

            try:
                post = json.loads(data["data"]["post"])
            except (KeyError, json.JSONDecodeError):
                return

            # Skip messages sent by this bot itself
            if post.get("user_id") == driver.client.userid:
                return

            self._dispatch_post(driver, config, post)

        driver.init_websocket(_on_message)

    def _resolve_config(self, channel_id: str, default: Any) -> Any | None:
        """Return the most-specific config for *channel_id*.

        Args:
            channel_id: Mattermost channel ID.
            default: Fallback config when no channel-specific mapping exists.

        Returns:
            Resolved config, or ``None`` if the channel is explicitly excluded.
        """
        if channel_id in self._channel_map:
            return self._channel_map[channel_id]
        # If no channel restriction on the default config, accept all channels
        if default.security.allowed_channels:
            return None
        return default

    def _dispatch_post(self, driver: Any, config: Any, post: dict[str, Any]) -> None:
        """Validate and publish a Mattermost post to the Redis Stream.

        Args:
            driver: Driver that received the post (used for reactions).
            config: Owning bot config (used for fallback channel routing).
            post: Decoded Mattermost post object.
        """
        channel_id: str = post["channel_id"]
        user_id: str = post.get("user_id", "")
        text: str = post.get("message", "")
        root_id: str = post.get("root_id", "")
        post_id: str = post["id"]

        matched_config = self._resolve_config(channel_id, config)
        if matched_config is None:
            return

        # Enforce user allow-list
        if (
            matched_config.security.allowed_users
            and user_id not in matched_config.security.allowed_users
        ):
            return

        # Optimistic hourglass reaction while the worker processes
        try:
            driver.reactions.create_reaction(
                options={
                    "user_id": driver.client.userid,
                    "post_id": post_id,
                    "emoji_name": _HOURGLASS,
                }
            )
        except Exception:
            pass  # Non-critical; proceed regardless

        # ``thread_ts`` maps to Mattermost's root_id (or post_id for root posts)
        thread_ts = root_id or post_id

        message_data: dict[str, str] = {
            "bot_id": matched_config.id,
            "channel_id": channel_id,
            "thread_ts": thread_ts,
            "user_id": user_id,
            "text": text,
            "message_ts": post_id,
            "bot_token": matched_config.mattermost.token,
            "platform": "mattermost",
            "platform_url": matched_config.mattermost.url,
        }
        self._redis.xadd(
            _STREAM_KEY,
            {k: v for k, v in message_data.items() if v},
        )
        log.info(
            "Published Mattermost message from user=%s channel=%s bot=%s",
            user_id,
            channel_id,
            matched_config.id,
        )


class MattermostResponsePublisher(ResponsePublisher):
    """Publishes responses back to Mattermost via the REST API.

    Args:
        token: Mattermost personal access token or bot token.
        url: Mattermost server URL (e.g. ``"https://mattermost.example.com"``).
        port: Mattermost API port (default: ``8065``).
    """

    def __init__(self, token: str, url: str, port: int = 8065) -> None:
        from mattermostdriver import Driver  # noqa: PLC0415  (optional dep)

        scheme, hostname = _parse_mm_url(url)
        self._driver = Driver(
            {
                "url": hostname,
                "token": token,
                "scheme": scheme,
                "port": port,
                "basepath": "/api/v4",
                # No persistent WebSocket needed for response-only use
                "keepalive": False,
            }
        )
        self._driver.login()

    # ------------------------------------------------------------------
    # ResponsePublisher interface
    # ------------------------------------------------------------------

    def send_message(
        self,
        channel_id: str,
        text: str,
        thread_id: str | None = None,
    ) -> str:
        """Post a message to Mattermost and return the post ID.

        Args:
            channel_id: Mattermost channel ID.
            text: Message body (supports Mattermost markdown).
            thread_id: Root post ID to reply within an existing thread.

        Returns:
            The ``id`` of the newly created post.
        """
        options: dict[str, Any] = {
            "channel_id": channel_id,
            "message": text,
        }
        if thread_id:
            options["root_id"] = thread_id

        result = self._driver.posts.create_post(options=options)
        return result["id"]

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a Mattermost post.

        Args:
            channel_id: Unused (kept for interface compatibility).
            message_id: Mattermost post ID.
            emoji: Emoji name without colons.
        """
        self._driver.reactions.create_reaction(
            options={
                "user_id": self._driver.client.userid,
                "post_id": message_id,
                "emoji_name": emoji,
            }
        )

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove an emoji reaction from a Mattermost post.

        Args:
            channel_id: Unused (kept for interface compatibility).
            message_id: Mattermost post ID.
            emoji: Emoji name without colons.
        """
        self._driver.reactions.delete_reaction(
            user_id=self._driver.client.userid,
            post_id=message_id,
            emoji_name=emoji,
        )

    def get_thread_replies(
        self,
        channel_id: str,
        thread_id: str,
        limit: int = 10,
    ) -> list[dict[str, Any]]:
        """Retrieve replies in a Mattermost thread.

        Args:
            channel_id: Unused (kept for interface compatibility).
            thread_id: Root post ID of the thread.
            limit: Maximum number of replies to return (most recent).

        Returns:
            List of dicts with keys ``user_id``, ``text``, and ``ts``
            (where ``ts`` holds the Mattermost post ID).
        """
        result = self._driver.posts.get_thread(post_id=thread_id)
        posts: dict[str, Any] = result.get("posts", {})
        order: list[str] = result.get("order", [])

        return [
            {
                "user_id": posts[post_id].get("user_id", ""),
                "text": posts[post_id].get("message", ""),
                "ts": post_id,
            }
            for post_id in order[-limit:]
            if post_id in posts
        ]
