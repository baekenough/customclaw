"""Hot-reload config subscriber via Redis Pub/Sub."""
from __future__ import annotations

import logging
import threading
import time
from typing import Callable

import redis

log = logging.getLogger(__name__)

CONFIG_CHANNEL = "customclaw:bot-config-changed"


def start_config_subscriber(
    redis_url: str,
    on_reload: Callable[[str], None],
) -> threading.Thread:
    """Start a daemon thread that subscribes to config change events.

    Uses a dedicated Redis connection for Pub/Sub (Redis requirement).
    Calls on_reload(bot_id) when a config change event is received.
    Reconnects automatically on connection loss.
    """

    def _listen() -> None:
        while True:
            try:
                r = redis.from_url(redis_url)
                pubsub = r.pubsub()
                pubsub.subscribe(CONFIG_CHANNEL)
                log.info(
                    "Config hot-reload subscriber started on channel: %s",
                    CONFIG_CHANNEL,
                )

                for message in pubsub.listen():
                    if message["type"] != "message":
                        continue
                    bot_id = message["data"]
                    if isinstance(bot_id, bytes):
                        bot_id = bot_id.decode()
                    log.info("Config reload event received for bot: %s", bot_id)
                    try:
                        on_reload(bot_id)
                    except Exception:
                        log.exception(
                            "Error during config hot-reload for bot: %s", bot_id
                        )
            except redis.ConnectionError:
                log.warning(
                    "Config subscriber lost Redis connection, reconnecting in 5s..."
                )
                time.sleep(5)
            except Exception:
                log.exception(
                    "Config subscriber unexpected error, restarting in 5s..."
                )
                time.sleep(5)

    thread = threading.Thread(target=_listen, name="config-hot-reload", daemon=True)
    thread.start()
    return thread


def publish_config_change(redis_client: redis.Redis, bot_id: str) -> None:
    """Publish a config change event. Called by bot management tools."""
    redis_client.publish(CONFIG_CHANNEL, bot_id)
