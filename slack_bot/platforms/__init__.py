"""Multi-platform messaging adapters for customclaw."""

from slack_bot.platforms.base import MessageEvent, PlatformAdapter, ResponsePublisher
from slack_bot.platforms.slack_adapter import SlackAdapter, SlackResponsePublisher

__all__ = [
    "MessageEvent",
    "PlatformAdapter",
    "ResponsePublisher",
    "SlackAdapter",
    "SlackResponsePublisher",
]

try:
    from slack_bot.platforms.mattermost_adapter import (
        MattermostAdapter,
        MattermostResponsePublisher,
    )

    __all__ += ["MattermostAdapter", "MattermostResponsePublisher"]
except ImportError:
    # mattermostdriver is an optional dependency
    pass
