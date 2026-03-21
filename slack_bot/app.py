"""BotManager — loads all bots and runs Socket Mode connections."""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading

import redis

from slack_bot.config.loader import load_all_bots
from slack_bot.bot_runner import BotRunner
from slack_bot.runtime_control import (
    consume_restart_request,
    is_supervised_runtime,
)

logging.basicConfig(
    level=logging.DEBUG,
    format="%(asctime)s [%(name)s] %(levelname)s: %(message)s",
)
log = logging.getLogger(__name__)


class BotManager:
    def __init__(self) -> None:
        self.redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
        self.redis_client = redis.from_url(self.redis_url)
        self.runners: dict[str, BotRunner] = {}
        self.handlers = []

    def load_bots(self) -> None:
        bots_dir = os.environ.get("BOTS_DIR", "/app/bots")
        configs = load_all_bots(bots_dir)
        log.info("Loaded %d bot configuration(s)", len(configs))

        # Group bots by app_token to share Socket Mode connections
        token_groups: dict[str, list] = {}
        for config in configs:
            token = config.slack_app_token
            if token not in token_groups:
                token_groups[token] = []
            token_groups[token].append(config)

        for token, group_configs in token_groups.items():
            runner = BotRunner(group_configs, self.redis_client)
            # Register under the first bot's id for reference
            group_id = "+".join(c.id for c in group_configs)
            self.runners[group_id] = runner
            bot_names = [c.id for c in group_configs]
            if len(group_configs) > 1:
                log.info("Shared Socket Mode for bots: %s", bot_names)
            else:
                log.info("Initialized bot: %s", bot_names[0])

    def start(self) -> None:
        if not self.runners:
            log.error("No bots configured. Exiting.")
            sys.exit(1)

        # Start each bot's Socket Mode handler in a thread
        for bot_id, runner in self.runners.items():
            handler = runner.start()
            self.handlers.append(handler)
            thread = threading.Thread(
                target=handler.start,
                name=f"bot-{bot_id}",
                daemon=True,
            )
            thread.start()
            log.info("Started Socket Mode for bot: %s", bot_id)

        log.info("All %d bot(s) running. Waiting for messages...", len(self.runners))

        # Block until signal
        shutdown_event = threading.Event()

        def handle_signal(signum, frame):
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
                os.execv(sys.executable, [sys.executable, "-m", "slack_bot.supervisor", "app"])

        # Cleanup
        for handler in self.handlers:
            try:
                handler.close()
            except Exception:
                pass
        log.info("Shutdown complete.")


def main():
    manager = BotManager()
    manager.load_bots()
    manager.start()


if __name__ == "__main__":
    main()
