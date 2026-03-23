"""Discord platform adapter using discord.py.

Install the optional dependency with::

    pip install discord.py

The adapter publishes messages to the same Redis Stream as
the Slack and Mattermost adapters so that the worker layer
processes all platform messages identically.
"""

from __future__ import annotations

import asyncio
import logging
import threading
from typing import Any

import redis
import requests

from slack_bot.platforms.base import PlatformAdapter, ResponsePublisher

log = logging.getLogger(__name__)

_STREAM_KEY = "customclaw:slack-messages"
_HOURGLASS = "hourglass_flowing_sand"

# Map common emoji names used across platforms to Discord Unicode characters.
_EMOJI_MAP: dict[str, str] = {
    "hourglass_flowing_sand": "\u23f3",
    "white_check_mark": "\u2705",
    "x": "\u274c",
}

_DISCORD_MSG_LIMIT = 2000


def _resolve_emoji(name: str) -> str:
    """Resolve a named emoji to its Unicode character for the Discord API.

    Falls back to the original name when no mapping is found, allowing
    custom guild emoji names to pass through unchanged.

    Args:
        name: Emoji name without surrounding colons.

    Returns:
        Unicode character string, or the original *name* if unmapped.
    """
    return _EMOJI_MAP.get(name, name)


class DiscordAdapter(PlatformAdapter):
    """Receives messages from Discord via gateway and publishes to Redis Stream.

    One :class:`DiscordAdapter` manages all configs whose ``platform`` field
    equals ``"discord"``.  All bots share a single ``discord.Client`` instance
    and are distinguished by their configured allowed channels.

    Args:
        configs: List of :class:`~slack_bot.config.loader.BotConfig`
            instances for Discord bots.
        redis_client: Connected Redis client used to publish messages.
    """

    def __init__(
        self,
        configs: list[Any],
        redis_client: redis.Redis,
    ) -> None:
        try:
            import discord  # noqa: F401  (verify availability at init time)
        except ImportError as exc:
            raise ImportError(
                "discord.py is required for Discord support. "
                "Install it with: pip install discord.py"
            ) from exc

        if not configs:
            raise ValueError("At least one BotConfig is required")

        self._configs = configs
        self._redis = redis_client
        self._running = False
        self._client: Any = None  # discord.Client, typed as Any to avoid hard dep

        # channel_id → config (channel-scoped routing)
        self._channel_map: dict[str, Any] = {}
        for config in configs:
            for channel in (config.security.allowed_channels or []):
                self._channel_map[channel] = config

        bot_names = [c.id for c in configs]
        log.info(
            "DiscordAdapter initialised for %d bot(s): %s",
            len(configs),
            bot_names,
        )

    # ------------------------------------------------------------------
    # PlatformAdapter interface
    # ------------------------------------------------------------------

    def start(self) -> None:
        """Connect to the Discord gateway and block until stopped.

        Creates a dedicated asyncio event loop for this thread so that
        multiple adapters can run concurrently without sharing loop state.
        All configured bots must share the same Discord token; if tokens
        differ, only the first config's token is used.
        """
        import discord

        self._running = True
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)

        intents = discord.Intents.default()
        intents.message_content = True
        client = discord.Client(intents=intents)
        self._client = client

        @client.event
        async def on_ready() -> None:
            log.info(
                "DiscordAdapter connected as %s (id=%s)",
                client.user,
                client.user.id if client.user else "unknown",
            )

        @client.event
        async def on_message(message: discord.Message) -> None:
            await self._handle_message(message)

        token = self._configs[0].discord.token
        try:
            loop.run_until_complete(client.start(token))
        except Exception:
            log.exception("DiscordAdapter event loop exited with error")
        finally:
            loop.close()

    def stop(self) -> None:
        """Close the Discord gateway connection."""
        self._running = False
        if self._client is not None:
            try:
                # Schedule close on the client's own event loop
                asyncio.run_coroutine_threadsafe(
                    self._client.close(),
                    self._client.loop,
                )
            except Exception:
                log.exception("Error closing Discord client")

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a Discord message.

        Schedules the coroutine on the Discord client's event loop so this
        method can be called safely from non-async contexts.

        Args:
            channel_id: Discord channel snowflake ID.
            message_id: Discord message snowflake ID.
            emoji: Emoji name without colons (e.g. ``"white_check_mark"``).
        """
        if self._client is None:
            return
        try:
            asyncio.run_coroutine_threadsafe(
                self._add_reaction_async(channel_id, message_id, emoji),
                self._client.loop,
            )
        except Exception:
            log.debug(
                "Could not schedule add_reaction for message %s in channel %s",
                message_id,
                channel_id,
            )

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove the bot's emoji reaction from a Discord message.

        Args:
            channel_id: Discord channel snowflake ID.
            message_id: Discord message snowflake ID.
            emoji: Emoji name without colons.
        """
        if self._client is None:
            return
        try:
            asyncio.run_coroutine_threadsafe(
                self._remove_reaction_async(channel_id, message_id, emoji),
                self._client.loop,
            )
        except Exception:
            log.debug(
                "Could not schedule remove_reaction for message %s in channel %s",
                message_id,
                channel_id,
            )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    async def _add_reaction_async(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Coroutine that fetches the message and adds a reaction.

        Args:
            channel_id: Discord channel snowflake ID.
            message_id: Discord message snowflake ID.
            emoji: Emoji name without colons.
        """
        try:
            channel = self._client.get_channel(int(channel_id))
            if channel is None:
                channel = await self._client.fetch_channel(int(channel_id))
            message = await channel.fetch_message(int(message_id))
            await message.add_reaction(_resolve_emoji(emoji))
        except Exception:
            log.debug(
                "Failed to add reaction '%s' to message %s", emoji, message_id
            )

    async def _remove_reaction_async(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Coroutine that fetches the message and removes the bot's reaction.

        Args:
            channel_id: Discord channel snowflake ID.
            message_id: Discord message snowflake ID.
            emoji: Emoji name without colons.
        """
        try:
            channel = self._client.get_channel(int(channel_id))
            if channel is None:
                channel = await self._client.fetch_channel(int(channel_id))
            message = await channel.fetch_message(int(message_id))
            await message.remove_reaction(_resolve_emoji(emoji), self._client.user)
        except Exception:
            log.debug(
                "Failed to remove reaction '%s' from message %s", emoji, message_id
            )

    async def _handle_message(self, message: Any) -> None:
        """Validate and publish an incoming Discord message to the Redis Stream.

        Skips bot-authored messages, enforces channel allow-lists, adds an
        hourglass reaction, then enqueues the message for the worker layer.

        Args:
            message: ``discord.Message`` instance from the ``on_message`` event.
        """
        # Ignore messages sent by any bot (including ourselves)
        if message.author.bot:
            return

        channel_id = str(message.channel.id)
        matched_config = self._resolve_config(channel_id)
        if matched_config is None:
            return

        # Enforce user allow-list
        user_id = str(message.author.id)
        if (
            matched_config.security.allowed_users
            and user_id not in matched_config.security.allowed_users
        ):
            return

        # Optimistic hourglass reaction while the worker processes
        try:
            await message.add_reaction(_resolve_emoji(_HOURGLASS))
        except Exception:
            pass  # Non-critical; proceed regardless

        # thread_ts: prefer reply reference, fall back to the message's own ID
        thread_ts = (
            str(message.reference.message_id)
            if message.reference and message.reference.message_id
            else str(message.id)
        )

        message_data: dict[str, str] = {
            "bot_id": matched_config.id,
            "channel_id": channel_id,
            "thread_ts": thread_ts,
            "user_id": user_id,
            "text": message.content,
            "message_ts": str(message.id),
            "bot_token": matched_config.discord.token,
            "platform": "discord",
        }
        self._redis.xadd(
            _STREAM_KEY,
            {k: v for k, v in message_data.items() if v},
        )
        log.info(
            "Published Discord message from user=%s channel=%s bot=%s",
            user_id,
            channel_id,
            matched_config.id,
        )

    def _resolve_config(self, channel_id: str) -> Any | None:
        """Return the most-specific config for *channel_id*.

        When a channel-specific mapping exists, that config is returned.
        Otherwise the first config that has no channel restriction is used.
        Returns ``None`` when the channel is not covered by any config.

        Args:
            channel_id: Discord channel snowflake ID string.

        Returns:
            Matching :class:`~slack_bot.config.loader.BotConfig`, or
            ``None`` if the channel should be ignored.
        """
        if channel_id in self._channel_map:
            return self._channel_map[channel_id]

        # Fallback: any config that imposes no channel restrictions
        for config in self._configs:
            if not config.security.allowed_channels:
                return config

        return None


class DiscordResponsePublisher(ResponsePublisher):
    """Publishes responses back to Discord via the REST API.

    Uses the ``requests`` library to call the Discord REST API directly,
    avoiding the overhead of a full ``discord.py`` client for response-only
    use (mirrors the approach of other platform publishers).

    Discord messages are limited to 2 000 characters.  Long messages are
    automatically split into sequential chunks; only the first chunk's ID
    is returned.

    Args:
        bot_token: Discord bot token (without the ``Bot `` prefix — the
            class adds it automatically).
    """

    def __init__(self, bot_token: str) -> None:
        self._token = bot_token
        self._base_url = "https://discord.com/api/v10"
        self._headers = {
            "Authorization": f"Bot {bot_token}",
            "Content-Type": "application/json",
        }

    # ------------------------------------------------------------------
    # ResponsePublisher interface
    # ------------------------------------------------------------------

    def send_message(
        self,
        channel_id: str,
        text: str,
        thread_id: str | None = None,
    ) -> str:
        """Post a message to Discord and return the first chunk's message ID.

        Long messages are split at the 2 000-character limit and sent as
        consecutive messages in the same channel / thread.

        Args:
            channel_id: Discord channel snowflake ID.
            text: Message body (plain text or Discord markdown).
            thread_id: Message ID to reply to (creates an inline reply).

        Returns:
            The ``id`` of the first message chunk sent, or ``""`` on failure.
        """
        chunks = [
            text[i : i + _DISCORD_MSG_LIMIT]
            for i in range(0, len(text), _DISCORD_MSG_LIMIT)
        ]
        first_id: str | None = None

        for chunk in chunks:
            body: dict[str, Any] = {"content": chunk}
            if thread_id:
                body["message_reference"] = {"message_id": thread_id}

            resp = requests.post(
                f"{self._base_url}/channels/{channel_id}/messages",
                headers=self._headers,
                json=body,
                timeout=10,
            )
            resp.raise_for_status()
            msg_id: str = resp.json()["id"]
            if first_id is None:
                first_id = msg_id

        return first_id or ""

    def add_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Add an emoji reaction to a Discord message.

        Args:
            channel_id: Discord channel snowflake ID.
            message_id: Discord message snowflake ID.
            emoji: Emoji name without colons (e.g. ``"white_check_mark"``).
        """
        encoded = requests.utils.quote(_resolve_emoji(emoji))
        resp = requests.put(
            f"{self._base_url}/channels/{channel_id}/messages"
            f"/{message_id}/reactions/{encoded}/@me",
            headers=self._headers,
            timeout=10,
        )
        resp.raise_for_status()

    def remove_reaction(
        self,
        channel_id: str,
        message_id: str,
        emoji: str,
    ) -> None:
        """Remove the bot's emoji reaction from a Discord message.

        Args:
            channel_id: Discord channel snowflake ID.
            message_id: Discord message snowflake ID.
            emoji: Emoji name without colons.
        """
        encoded = requests.utils.quote(_resolve_emoji(emoji))
        resp = requests.delete(
            f"{self._base_url}/channels/{channel_id}/messages"
            f"/{message_id}/reactions/{encoded}/@me",
            headers=self._headers,
            timeout=10,
        )
        resp.raise_for_status()

    def get_thread_replies(
        self,
        channel_id: str,
        thread_id: str,
        limit: int = 10,
    ) -> list[dict[str, Any]]:
        """Retrieve messages from a Discord thread channel.

        In Discord, a thread is itself a channel.  When *thread_id* refers
        to a thread channel ID, messages are fetched from that channel
        directly.  When *thread_id* is an ordinary message ID (i.e. a reply
        reference), the method falls back to fetching the parent channel's
        recent messages.

        Args:
            channel_id: Parent channel snowflake ID (used as fallback).
            thread_id: Thread channel ID or reply-parent message ID.
            limit: Maximum number of messages to return (most recent).

        Returns:
            List of dicts with keys ``user_id``, ``text``, and ``ts``
            (Discord message snowflake ID).
        """
        # Attempt to read from the thread channel first.
        target_channel = thread_id or channel_id
        resp = requests.get(
            f"{self._base_url}/channels/{target_channel}/messages",
            headers=self._headers,
            params={"limit": min(limit, 100)},
            timeout=10,
        )

        if resp.status_code == 404:
            # thread_id was a message ID, not a channel — fall back to parent
            resp = requests.get(
                f"{self._base_url}/channels/{channel_id}/messages",
                headers=self._headers,
                params={"limit": min(limit, 100)},
                timeout=10,
            )

        resp.raise_for_status()
        messages: list[dict[str, Any]] = resp.json()

        return [
            {
                "user_id": str(msg.get("author", {}).get("id", "")),
                "text": msg.get("content", ""),
                "ts": str(msg.get("id", "")),
            }
            for msg in reversed(messages)  # API returns newest-first; reverse to oldest-first
        ]
