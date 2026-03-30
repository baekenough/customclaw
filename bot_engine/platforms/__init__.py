"""Multi-platform messaging adapters for customclaw."""

from bot_engine.platforms.base import (
    MessageEvent,
    NoOpResponsePublisher,
    PlatformAdapter,
    ResponsePublisher,
)

__all__ = [
    "MessageEvent",
    "NoOpResponsePublisher",
    "PlatformAdapter",
    "ResponsePublisher",
]

try:
    from bot_engine.platforms.slack_adapter import SlackAdapter, SlackResponsePublisher

    __all__ += ["SlackAdapter", "SlackResponsePublisher"]
except ImportError:
    # slack-bolt is an optional dependency
    pass

try:
    from bot_engine.platforms.mattermost_adapter import (
        MattermostAdapter,
        MattermostResponsePublisher,
    )

    __all__ += ["MattermostAdapter", "MattermostResponsePublisher"]
except ImportError:
    # mattermostdriver is an optional dependency
    pass

try:
    from bot_engine.platforms.discord_adapter import (
        DiscordAdapter,
        DiscordResponsePublisher,
    )

    __all__ += ["DiscordAdapter", "DiscordResponsePublisher"]
except ImportError:
    # discord.py is an optional dependency
    pass
