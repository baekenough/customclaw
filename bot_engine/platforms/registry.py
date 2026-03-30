"""Platform registry — decouples adapter/publisher creation from platform names.

Ported from Go's ``PublisherFactory`` pattern in
``go/internal/platform/publisher_factory.go``.  Each platform adapter module
registers itself at import time so that :class:`PlatformRegistry` can
instantiate adapters and publishers without hard-coding platform names.
"""

from __future__ import annotations

import logging
from typing import Any, Callable

from bot_engine.platforms.base import (
    NoOpResponsePublisher,
    PlatformAdapter,
    ResponsePublisher,
)

log = logging.getLogger(__name__)

# Type aliases for factory callables.
AdapterFactory = Callable[..., PlatformAdapter]
PublisherFactory = Callable[..., ResponsePublisher]


class PlatformRegistry:
    """Singleton registry for platform adapter and publisher factories.

    Platform modules call :meth:`register` at import time::

        PlatformRegistry.register(
            "slack",
            adapter_factory=lambda configs, redis: SlackAdapter(configs, redis),
            publisher_factory=lambda bot_token, **kw: SlackResponsePublisher(bot_token=bot_token),
        )

    The orchestrator then uses :meth:`get_publisher` / :meth:`get_adapter`
    instead of ``if platform == "slack": ...`` chains.
    """

    _adapters: dict[str, AdapterFactory] = {}
    _publishers: dict[str, PublisherFactory] = {}

    @classmethod
    def register(
        cls,
        platform: str,
        *,
        adapter_factory: AdapterFactory | None = None,
        publisher_factory: PublisherFactory | None = None,
    ) -> None:
        """Register factories for *platform*.

        Either or both factories may be provided.  Calling ``register``
        again for the same platform silently overwrites the previous
        registration.
        """
        if adapter_factory is not None:
            cls._adapters[platform] = adapter_factory
        if publisher_factory is not None:
            cls._publishers[platform] = publisher_factory
        log.debug("registered platform: %s (adapter=%s, publisher=%s)",
                  platform, adapter_factory is not None, publisher_factory is not None)

    @classmethod
    def get_publisher(cls, platform: str, **kwargs: Any) -> ResponsePublisher:
        """Return a :class:`ResponsePublisher` for *platform*.

        Falls back to :class:`NoOpResponsePublisher` for unregistered
        platforms — the same behaviour as Go's ``noopPublisher``.
        """
        factory = cls._publishers.get(platform)
        if factory is None:
            log.warning("no publisher registered for platform=%s, using NoOp", platform)
            return NoOpResponsePublisher(platform=platform)
        return factory(**kwargs)

    @classmethod
    def get_adapter(
        cls, platform: str, *args: Any, **kwargs: Any,
    ) -> PlatformAdapter | None:
        """Return a :class:`PlatformAdapter` for *platform*, or ``None``."""
        factory = cls._adapters.get(platform)
        if factory is None:
            log.warning("no adapter registered for platform=%s", platform)
            return None
        return factory(*args, **kwargs)

    @classmethod
    def supported_platforms(cls) -> list[str]:
        """Return list of platforms with at least one factory registered."""
        return sorted(set(cls._adapters) | set(cls._publishers))

    @classmethod
    def has_adapter(cls, platform: str) -> bool:
        """Check if an adapter factory is registered for *platform*."""
        return platform in cls._adapters

    @classmethod
    def has_publisher(cls, platform: str) -> bool:
        """Check if a publisher factory is registered for *platform*."""
        return platform in cls._publishers

    @classmethod
    def clear(cls) -> None:
        """Remove all registrations.  Useful for testing."""
        cls._adapters.clear()
        cls._publishers.clear()
