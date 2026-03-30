"""Shared test fixtures for customclaw tests.

Heavy runtime dependencies (psycopg2, redis, slack-bolt, opensearchpy, etc.)
are mocked at the sys.modules level so that unit tests can run without a live
database or messaging platform.  The stubs are just rich enough to satisfy
import-time attribute accesses; individual tests patch finer-grained behaviour
as needed.
"""
import sys
import types
from unittest.mock import MagicMock

import pytest


def _make_stub(name: str) -> types.ModuleType:
    """Return a minimal stub module that satisfies simple attribute accesses."""
    mod = types.ModuleType(name)
    mod.__spec__ = None  # type: ignore[attr-defined]
    return mod


# ---------------------------------------------------------------------------
# psycopg2 stub
# ---------------------------------------------------------------------------

if "psycopg2" not in sys.modules:
    _psycopg2 = _make_stub("psycopg2")
    _psycopg2.connect = MagicMock()  # type: ignore[attr-defined]

    _psycopg2_extras = _make_stub("psycopg2.extras")

    _psycopg2_pool = _make_stub("psycopg2.pool")
    _psycopg2_pool.ThreadedConnectionPool = MagicMock()  # type: ignore[attr-defined]
    _psycopg2_pool.SimpleConnectionPool = MagicMock()  # type: ignore[attr-defined]
    _psycopg2.pool = _psycopg2_pool  # type: ignore[attr-defined]

    sys.modules["psycopg2"] = _psycopg2
    sys.modules["psycopg2.extras"] = _psycopg2_extras
    sys.modules["psycopg2.pool"] = _psycopg2_pool

# ---------------------------------------------------------------------------
# yaml stub — loader.py imports yaml at module level
# ---------------------------------------------------------------------------

if "yaml" not in sys.modules:
    _yaml = _make_stub("yaml")
    _yaml.safe_load = MagicMock(return_value={})  # type: ignore[attr-defined]
    sys.modules["yaml"] = _yaml

# ---------------------------------------------------------------------------
# redis stub — prevents import errors in worker.py and registry.py
# ---------------------------------------------------------------------------

if "redis" not in sys.modules:
    _redis_mod = _make_stub("redis")
    _redis_mod.Redis = MagicMock()  # type: ignore[attr-defined]
    _redis_mod.StrictRedis = MagicMock()  # type: ignore[attr-defined]
    _redis_exceptions = _make_stub("redis.exceptions")
    _redis_mod.exceptions = _redis_exceptions
    sys.modules["redis"] = _redis_mod
    sys.modules["redis.exceptions"] = _redis_exceptions

# ---------------------------------------------------------------------------
# slack_sdk stub — provides a realistic WebClient that tests can patch
# ---------------------------------------------------------------------------

if "slack_sdk" not in sys.modules:
    _slack_sdk = _make_stub("slack_sdk")

    class _WebClient:
        """Minimal WebClient stub — methods are replaced per-test via patch."""

        def __init__(self, token: str = "") -> None:
            self.token = token

        def chat_postMessage(self, **kwargs):  # noqa: N802
            return {"ok": True, "ts": ""}

        def reactions_add(self, **kwargs):
            return {"ok": True}

    _slack_sdk.WebClient = _WebClient  # type: ignore[attr-defined]
    sys.modules["slack_sdk"] = _slack_sdk

# ---------------------------------------------------------------------------
# opensearchpy stub — opensearch_client.py uses OpenSearch at import time
# ---------------------------------------------------------------------------

if "opensearchpy" not in sys.modules:
    _opensearchpy = _make_stub("opensearchpy")

    class _OpenSearch:
        """Minimal OpenSearch stub."""

        def __init__(self, *args, **kwargs):
            pass

    _opensearchpy.OpenSearch = _OpenSearch  # type: ignore[attr-defined]
    _opensearchpy.exceptions = _make_stub("opensearchpy.exceptions")
    _opensearchpy.helpers = _make_stub("opensearchpy.helpers")
    sys.modules["opensearchpy"] = _opensearchpy
    sys.modules["opensearchpy.exceptions"] = _opensearchpy.exceptions
    sys.modules["opensearchpy.helpers"] = _opensearchpy.helpers

# ---------------------------------------------------------------------------
# Other optional heavy deps — stub if absent; tests use pytest.skip as needed
# ---------------------------------------------------------------------------

_slack_bolt = _make_stub("slack_bolt")
_slack_bolt.App = MagicMock()  # type: ignore[attr-defined]
sys.modules.setdefault("slack_bolt", _slack_bolt)

_slack_bolt_async = _make_stub("slack_bolt.async_app")
sys.modules.setdefault("slack_bolt.async_app", _slack_bolt_async)

_slack_bolt_adapter = _make_stub("slack_bolt.adapter")
sys.modules.setdefault("slack_bolt.adapter", _slack_bolt_adapter)

_slack_bolt_socket = _make_stub("slack_bolt.adapter.socket_mode")
_slack_bolt_socket.SocketModeHandler = MagicMock()  # type: ignore[attr-defined]
sys.modules.setdefault("slack_bolt.adapter.socket_mode", _slack_bolt_socket)

for _dep in [
    "discord",
    "mattermostdriver",
    "anthropic",
]:
    if _dep not in sys.modules:
        sys.modules[_dep] = _make_stub(_dep)
