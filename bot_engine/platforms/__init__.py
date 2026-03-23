"""Multi-platform messaging adapters for customclaw."""

from bot_engine.platforms.base import MessageEvent, PlatformAdapter, ResponsePublisher
from bot_engine.platforms.slack_adapter import SlackAdapter, SlackResponsePublisher

__all__ = [
    "MessageEvent",
    "PlatformAdapter",
    "ResponsePublisher",
    "SlackAdapter",
    "SlackResponsePublisher",
]

try:
    from bot_engine.platforms.mattermost_adapter import (
        MattermostAdapter,
        MattermostResponsePublisher,
    )

    __all__ += ["MattermostAdapter", "MattermostResponsePublisher"]
except ImportError:
    # mattermostdriver is an optional dependency
    pass
