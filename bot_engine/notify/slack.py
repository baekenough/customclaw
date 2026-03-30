"""Slack notification backend using slack-sdk."""

from __future__ import annotations

import logging

from bot_engine.notify.base import NotifyBackend

log = logging.getLogger(__name__)


class SlackNotifyBackend(NotifyBackend):
    """Sends notifications via Slack Web API."""

    def __init__(self, token: str, default_channel: str = "") -> None:
        from slack_sdk import WebClient  # lazy import — slack-sdk is optional

        self._client = WebClient(token=token)
        self._default_channel = default_channel

    def send_message(
        self,
        channel: str,
        text: str,
        *,
        thread_ts: str = "",
        unfurl_links: bool = False,
    ) -> str:
        ch = channel or self._default_channel
        if not ch:
            log.warning("No channel specified for Slack notification")
            return ""
        try:
            kwargs: dict = {
                "channel": ch,
                "text": text,
                "unfurl_links": unfurl_links,
            }
            if thread_ts:
                kwargs["thread_ts"] = thread_ts
            resp = self._client.chat_postMessage(**kwargs)
            return resp.get("ts", "") if resp.get("ok") else ""
        except Exception as e:
            log.warning("Slack notification failed (non-blocking): %s", e)
            return ""

    def add_reaction(self, channel: str, timestamp: str, emoji: str) -> None:
        ch = channel or self._default_channel
        try:
            self._client.reactions_add(channel=ch, name=emoji, timestamp=timestamp)
        except Exception:
            pass  # best-effort
