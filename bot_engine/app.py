"""BotManager — loads all bots and runs multi-platform adapters."""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading

import redis

from bot_engine.config.loader import BotConfig, load_all_bots
from bot_engine.platforms.base import PlatformAdapter
from bot_engine.runtime_control import (
    consume_restart_request,
    is_supervised_runtime,
)

try:
    from bot_engine.platforms.slack_adapter import SlackAdapter
    _HAS_SLACK = True
except ImportError:
    _HAS_SLACK = False

try:
    from bot_engine.platforms.mattermost_adapter import MattermostAdapter

    _HAS_MATTERMOST = True
except ImportError:
    _HAS_MATTERMOST = False

try:
    from bot_engine.platforms.discord_adapter import DiscordAdapter

    _HAS_DISCORD = True
except ImportError:
    _HAS_DISCORD = False

logging.basicConfig(
    level=logging.DEBUG,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
log = logging.getLogger(__name__)


class BotManager:
    """Loads bot configurations and manages per-platform adapter lifecycles.

    Each call to :meth:`load_bots` groups configs by platform and creates one
    :class:`~bot_engine.platforms.base.PlatformAdapter` per platform group.
    :meth:`start` then runs every adapter in a daemon thread and blocks until
    a shutdown signal is received.
    """

    def __init__(self) -> None:
        self.redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
        self.redis_client = redis.from_url(self.redis_url)
        self._adapters: list[PlatformAdapter] = []

    def load_bots(self) -> None:
        """Discover all bot configs and build platform adapters."""
        bots_dir = os.environ.get("BOTS_DIR", "/app/bots")
        configs = load_all_bots(bots_dir)
        log.info("Loaded %d bot configuration(s)", len(configs))

        # Split configs by platform
        slack_configs = [c for c in configs if c.platform == "slack"]
        mm_configs = [c for c in configs if c.platform == "mattermost"]
        discord_configs = [c for c in configs if c.platform == "discord"]
        unknown = [
            c for c in configs if c.platform not in {"slack", "mattermost", "discord"}
        ]
        if unknown:
            log.warning(
                "Ignoring %d config(s) with unrecognised platform(s): %s",
                len(unknown),
                [c.id for c in unknown],
            )

        # Build Slack adapters (group by app_token to share Socket Mode connections)
        if slack_configs:
            if not _HAS_SLACK:
                log.error(
                    "slack-bolt is not installed; skipping %d Slack "
                    "bot(s).  Install it with: pip install slack-bolt",
                    len(slack_configs),
                )
            else:
                try:
                    self._adapters.extend(
                        self._build_slack_adapters(slack_configs)
                    )
                except Exception:
                    log.exception(
                        "Failed to initialise Slack adapter(s) for %d bot(s); "
                        "skipping Slack platform",
                        len(slack_configs),
                    )

        # Build Mattermost adapters
        if mm_configs:
            if not _HAS_MATTERMOST:
                log.error(
                    "mattermostdriver is not installed; skipping %d Mattermost "
                    "bot(s).  Install it with: pip install mattermostdriver",
                    len(mm_configs),
                )
            else:
                try:
                    adapter = MattermostAdapter(mm_configs, self.redis_client)
                    self._adapters.append(adapter)
                    log.info(
                        "Initialised MattermostAdapter for %d bot(s): %s",
                        len(mm_configs),
                        [c.id for c in mm_configs],
                    )
                except Exception:
                    log.exception(
                        "Failed to initialise Mattermost adapter; skipping",
                    )

        # Build Discord adapters
        if discord_configs:
            if not _HAS_DISCORD:
                log.error(
                    "discord.py is not installed; skipping %d Discord "
                    "bot(s).  Install it with: pip install discord.py",
                    len(discord_configs),
                )
            else:
                try:
                    adapter = DiscordAdapter(discord_configs, self.redis_client)
                    self._adapters.append(adapter)
                    log.info(
                        "Initialised DiscordAdapter for %d bot(s): %s",
                        len(discord_configs),
                        [c.id for c in discord_configs],
                    )
                except Exception:
                    log.exception(
                        "Failed to initialise Discord adapter; skipping",
                    )

    def _build_slack_adapters(
        self, configs: list[BotConfig]
    ) -> list[SlackAdapter]:
        """Group Slack configs by *slack_app_token* and return one adapter each.

        Multiple bots that share an app token are served by a single Socket
        Mode connection, so they are grouped together into one
        :class:`SlackAdapter`.
        """
        token_groups: dict[str, list[BotConfig]] = {}
        for config in configs:
            token_groups.setdefault(config.slack_app_token, []).append(config)

        adapters: list[SlackAdapter] = []
        for group_configs in token_groups.values():
            adapter = SlackAdapter(group_configs, self.redis_client)
            adapters.append(adapter)
            bot_names = [c.id for c in group_configs]
            if len(group_configs) > 1:
                log.info("Shared Socket Mode for Slack bots: %s", bot_names)
            else:
                log.info("Initialised Slack bot: %s", bot_names[0])
        return adapters

    def start(self) -> None:
        """Start all adapters and block until shutdown."""
        if not self._adapters:
            log.error("No bots configured. Exiting.")
            sys.exit(1)

        # Launch each adapter in its own daemon thread
        threads: list[threading.Thread] = []
        for i, adapter in enumerate(self._adapters):
            thread = threading.Thread(
                target=adapter.start,
                name=f"adapter-{i}",
                daemon=True,
            )
            thread.start()
            threads.append(thread)
            log.info(
                "Started adapter %d/%d: %s",
                i + 1,
                len(self._adapters),
                type(adapter).__name__,
            )

        log.info(
            "All %d adapter(s) running. Waiting for messages...",
            len(self._adapters),
        )

        # Block the main thread until SIGINT / SIGTERM
        shutdown_event = threading.Event()

        def handle_signal(signum, frame):  # noqa: ARG001
            log.info("Received signal %d, shutting down...", signum)
            shutdown_event.set()

        signal.signal(signal.SIGINT, handle_signal)
        signal.signal(signal.SIGTERM, handle_signal)

        while not shutdown_event.wait(1):
            if is_supervised_runtime():
                continue
            request = consume_restart_request("app")
            if request:
                log.warning("Restart requested for app: %s", request)
                os.execv(
                    sys.executable,
                    [sys.executable, "-m", "bot_engine.supervisor", "app"],
                )

        # Graceful shutdown: stop all adapters, then wait for threads
        log.info("Stopping %d adapter(s)...", len(self._adapters))
        for adapter in self._adapters:
            try:
                adapter.stop()
            except Exception:
                log.exception("Error stopping adapter %s", type(adapter).__name__)

        for thread in threads:
            thread.join(timeout=10)

        log.info("Shutdown complete.")


def main():
    manager = BotManager()
    manager.load_bots()
    manager.start()


if __name__ == "__main__":
    main()
