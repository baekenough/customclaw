"""Per-connection Socket Mode manager with channel-based routing."""

from __future__ import annotations

import logging

import redis
from slack_bolt import App
from slack_bolt.adapter.socket_mode import SocketModeHandler
from slack_sdk import WebClient

from bot_engine.config.loader import BotConfig

log = logging.getLogger(__name__)


class BotRunner:
    """Manages a single Socket Mode connection that routes to one or more bots."""

    def __init__(self, configs: list[BotConfig], redis_client: redis.Redis) -> None:
        if not configs:
            raise ValueError("At least one BotConfig is required")

        self.configs = configs
        self.redis_client = redis_client

        # All configs share the same tokens
        self.bot_token = configs[0].slack_bot_token
        self.app_token = configs[0].slack_app_token

        # Build channel → config mapping
        self._channel_map: dict[str, BotConfig] = {}
        self._default_config: BotConfig | None = None

        for config in configs:
            if config.security.allowed_channels:
                for chan in config.security.allowed_channels:
                    self._channel_map[chan] = config
            else:
                # Bot with no channel restriction acts as fallback
                self._default_config = config

        # If no default, use first config as fallback
        if self._default_config is None:
            self._default_config = configs[0]

        self.app = App(token=self.bot_token)
        self.client = WebClient(token=self.bot_token)
        self._setup_handlers()

        bot_names = [c.id for c in configs]
        log.info("BotRunner initialized for %d bot(s): %s", len(configs), bot_names)

    def _resolve_config(self, channel_id: str) -> BotConfig | None:
        """Find the bot config for a given channel."""
        config = self._channel_map.get(channel_id)
        if config:
            return config
        return self._default_config

    def _setup_handlers(self) -> None:
        @self.app.event("message")
        def handle_message(event, say):
            if event.get("subtype"):
                return

            channel_id = event.get("channel", "")
            user_id = event.get("user", "")
            text = event.get("text", "")
            thread_ts = event.get("thread_ts") or event.get("ts", "")
            message_ts = event.get("ts", "")

            config = self._resolve_config(channel_id)
            if config is None:
                return

            # Check allowed channels (for the resolved config)
            if config.security.allowed_channels:
                if channel_id not in config.security.allowed_channels:
                    return

            # Check allowed users
            if config.security.allowed_users:
                if user_id not in config.security.allowed_users:
                    return

            # Add hourglass reaction
            try:
                self.client.reactions_add(
                    channel=channel_id,
                    name="hourglass_flowing_sand",
                    timestamp=message_ts,
                )
            except Exception:
                pass

            # Publish to Redis Stream with the resolved bot_id
            message_data = {
                "bot_id": config.id,
                "channel_id": channel_id,
                "thread_ts": thread_ts,
                "user_id": user_id,
                "text": text,
                "message_ts": message_ts,
                "bot_token": config.slack_bot_token,
            }
            self.redis_client.xadd(
                "customclaw:slack-messages",
                {k: v for k, v in message_data.items() if v},
            )
            log.info(
                "Published message from %s in %s to Redis Stream (bot: %s)",
                user_id,
                channel_id,
                config.id,
            )

    def start(self) -> SocketModeHandler:
        handler = SocketModeHandler(self.app, self.app_token)
        return handler
