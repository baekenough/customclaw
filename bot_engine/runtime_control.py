"""Runtime control helpers for safe self-restart requests."""

from __future__ import annotations

import json
import os
import time

import redis

REDIS_URL = os.environ.get("REDIS_URL", "redis://redis:6379")
RESTART_KEY_PREFIX = "customclaw:control:restart"
SUPERVISED_ENV = "CUSTOMCLAW_SUPERVISED"
RUNTIME_TARGET_ENV = "CUSTOMCLAW_RUNTIME_TARGET"
RESTART_EXIT_CODE = 75


def get_redis_client() -> redis.Redis:
    return redis.from_url(REDIS_URL)


def _restart_key(target: str) -> str:
    return f"{RESTART_KEY_PREFIX}:{target}"


def request_restart(target: str, requested_by: str = "", reason: str = "") -> None:
    payload = {
        "target": target,
        "requested_by": requested_by,
        "reason": reason,
        "requested_at": int(time.time()),
    }
    client = get_redis_client()
    client.set(_restart_key(target), json.dumps(payload))


def consume_restart_request(target: str) -> dict | None:
    client = get_redis_client()
    key = _restart_key(target)
    payload = client.get(key)
    if not payload:
        return None

    client.delete(key)
    if isinstance(payload, bytes):
        payload = payload.decode()

    try:
        return json.loads(payload)
    except Exception:
        return {"target": target, "raw": payload}


def is_supervised_runtime() -> bool:
    return os.environ.get(SUPERVISED_ENV) == "1"
