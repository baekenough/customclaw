"""BotManager — loads all bots and runs multi-platform adapters."""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading
from collections import defaultdict

import redis

from bot_engine.config.loader import BotConfig, load_all_bots
from bot_engine.platforms.base import PlatformAdapter
from bot_engine.platforms.registry import PlatformRegistry
from bot_engine.runtime_control import (
    consume_restart_request,
    is_supervised_runtime,
)

# Trigger adapter auto-registration at import time.
try:
    import bot_engine.platforms.slack_adapter as _  # noqa: F401
except ImportError:
    pass

try:
    import bot_engine.platforms.mattermost_adapter as _  # noqa: F401
except ImportError:
    pass

try:
    import bot_engine.platforms.discord_adapter as _  # noqa: F401
except ImportError:
    pass

logging.basicConfig(
    level=logging.DEBUG,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
log = logging.getLogger(__name__)


class BotManager:
    """Loads bot configurations and manages per-platform adapter lifecycles.

    Each call to :meth:`load_bots` groups configs by platform and creates one
    :class:`~bot_engine.platforms.base.PlatformAdapter` per platform group.
    Platform adapters are instantiated via :class:`PlatformRegistry` — no
    hard-coded ``if platform == ...`` branches.

    :meth:`start` then runs every adapter in a daemon thread and blocks until
    a shutdown signal is received.
    """

    def __init__(self) -> None:
        self.redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
        self.redis_client = redis.from_url(self.redis_url)
        self._adapters: list[PlatformAdapter] = []

    def load_bots(self) -> None:
        """Discover all bot configs and build platform adapters via registry."""
        bots_dir = os.environ.get("BOTS_DIR", "/app/bots")
        configs = load_all_bots(bots_dir)
        log.info("Loaded %d bot configuration(s)", len(configs))

        # Group configs by platform
        by_platform: dict[str, list[BotConfig]] = defaultdict(list)
        for config in configs:
            by_platform[config.platform].append(config)

        for platform, platform_configs in by_platform.items():
            if not PlatformRegistry.has_adapter(platform):
                log.warning(
                    "No adapter registered for platform %r; "
                    "skipping %d bot(s): %s",
                    platform,
                    len(platform_configs),
                    [c.id for c in platform_configs],
                )
                continue

            adapters = self._build_adapters(platform, platform_configs)
            self._adapters.extend(adapters)

    def _build_adapters(
        self,
        platform: str,
        configs: list[BotConfig],
    ) -> list[PlatformAdapter]:
        """Instantiate adapter(s) for *platform* via the registry.

        Slack groups configs by ``slack_app_token`` so that bots sharing an
        app token reuse a single Socket Mode connection.  All other platforms
        receive all their configs in a single adapter instance.

        Args:
            platform: Platform name (e.g. ``"slack"``).
            configs: Non-empty list of configs for this platform.

        Returns:
            List of constructed :class:`PlatformAdapter` instances.
        """
        if platform == "slack":
            return self._build_slack_adapters(configs)

        try:
            adapter = PlatformRegistry.get_adapter(
                platform, configs, self.redis_client
            )
            if adapter is None:
                log.error(
                    "Registry returned None for platform %r; skipping %d bot(s)",
                    platform,
                    len(configs),
                )
                return []
            log.info(
                "Initialised %s adapter for %d bot(s): %s",
                type(adapter).__name__,
                len(configs),
                [c.id for c in configs],
            )
            return [adapter]
        except Exception:
            log.exception(
                "Failed to initialise %r adapter for %d bot(s); skipping",
                platform,
                len(configs),
            )
            return []

    def _build_slack_adapters(
        self, configs: list[BotConfig]
    ) -> list[PlatformAdapter]:
        """Group Slack configs by *slack_app_token* and return one adapter each.

        Multiple bots that share an app token are served by a single Socket
        Mode connection, so they are grouped together into one adapter instance
        constructed via the registry.

        Args:
            configs: All Slack :class:`BotConfig` instances.

        Returns:
            List of Slack :class:`PlatformAdapter` instances.
        """
        token_groups: dict[str, list[BotConfig]] = defaultdict(list)
        for config in configs:
            token_groups[config.slack_app_token].append(config)

        adapters: list[PlatformAdapter] = []
        for group_configs in token_groups.values():
            try:
                adapter = PlatformRegistry.get_adapter(
                    "slack", group_configs, self.redis_client
                )
                if adapter is None:
                    log.error(
                        "Registry returned None for Slack; skipping bots: %s",
                        [c.id for c in group_configs],
                    )
                    continue
                adapters.append(adapter)
                bot_names = [c.id for c in group_configs]
                if len(group_configs) > 1:
                    log.info("Shared Socket Mode for Slack bots: %s", bot_names)
                else:
                    log.info("Initialised Slack bot: %s", bot_names[0])
            except Exception:
                log.exception(
                    "Failed to initialise Slack adapter for bots: %s; skipping",
                    [c.id for c in group_configs],
                )
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
