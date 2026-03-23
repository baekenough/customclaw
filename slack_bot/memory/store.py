"""Message storage with PostgreSQL."""

from __future__ import annotations

import logging
import os
import re

import psycopg2
from psycopg2 import pool

log = logging.getLogger(__name__)

_KOREAN_PARTICLES = (
    "으로는",
    "에서는",
    "에게서",
    "까지는",
    "부터는",
    "이라도",
    "이라서",
    "이랑",
    "처럼",
    "보다",
    "까지",
    "부터",
    "에서",
    "에게",
    "한테",
    "으로",
    "라고",
    "이고",
    "이며",
    "였다",
    "이다",
    "하면",
    "하니",
    "하다",
    "하는",
    "하고",
    "해서",
    "하자",
    "하라",
    "이나",
    "라도",
    "조차",
    "마저",
    "밖에",
    "이다",
    "이다",
    "은",
    "는",
    "이",
    "가",
    "을",
    "를",
    "에",
    "의",
    "도",
    "만",
    "와",
    "과",
    "로",
    "라",
    "야",
)
_TOKEN_RE = re.compile(r"[0-9A-Za-z가-힣]{2,}")


def _normalize_text(text: str) -> str:
    return re.sub(r"\s+", " ", (text or "").strip().lower())


def _query_tokens(text: str) -> list[str]:
    normalized = _normalize_text(text)
    raw_tokens = _TOKEN_RE.findall(normalized)
    seen: set[str] = set()
    tokens: list[str] = []
    for token in raw_tokens:
        candidates = {token}
        for suffix in _KOREAN_PARTICLES:
            if token.endswith(suffix) and len(token) - len(suffix) >= 2:
                candidates.add(token[: -len(suffix)])
        for candidate in candidates:
            if len(candidate) < 2 or candidate in seen:
                continue
            seen.add(candidate)
            tokens.append(candidate)
    return tokens


def _score_text_match(query_text: str, content: str) -> float:
    query_norm = _normalize_text(query_text)
    content_norm = _normalize_text(content)
    if not query_norm or not content_norm:
        return 0.0

    score = 0.0
    if query_norm in content_norm:
        score += 10.0

    if len(query_norm) <= 3 and query_norm in {"해", "해줘", "해봐"}:
        for keyword in ("계속", "이어서", "보강", "개선", "점검", "확인"):
            if keyword in content_norm:
                score += 2.0

    query_tokens = _query_tokens(query_norm)
    content_tokens = set(_query_tokens(content_norm))
    if not query_tokens or not content_tokens:
        return score

    overlap = 0
    partial = 0
    for token in query_tokens:
        if token in content_tokens:
            overlap += 1
            continue
        if any(token in other or other in token for other in content_tokens):
            partial += 1

    score += overlap * 3.0
    score += partial * 1.5
    score += overlap / max(len(query_tokens), 1)
    if overlap and any(token in {"계속", "이어서", "보강", "개선", "점검", "확인"} for token in query_tokens):
        score += 2.0
    return score


def _compact_content(content: str, limit: int = 220) -> str:
    normalized = _normalize_text(content)
    if len(normalized) <= limit:
        return normalized
    return normalized[: limit - 3].rstrip() + "..."


class MessageStore:
    """PostgreSQL message and memory storage."""

    def __init__(self) -> None:
        dsn = os.environ.get("DATABASE_DSN", "")
        self._pool = pool.SimpleConnectionPool(1, 5, dsn) if dsn else None

    def save_message(
        self,
        bot_id: str,
        channel_id: str,
        thread_ts: str,
        user_id: str,
        role: str,
        content: str,
    ) -> None:
        if not self._pool:
            return
        normalized_thread_ts = self._normalize_thread_ts(thread_ts)
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """INSERT INTO messages (bot_id, channel_id, thread_ts, user_id, role, content)
                    VALUES (%s, %s, %s, %s, %s, %s)""",
                    (
                        bot_id,
                        channel_id,
                        normalized_thread_ts,
                        user_id,
                        role,
                        content,
                    ),
                )
            conn.commit()
        except Exception as e:
            log.warning("Failed to save message: %s", e)
            conn.rollback()
        finally:
            self._pool.putconn(conn)

    def _normalize_thread_ts(self, thread_ts: str | None) -> str:
        return thread_ts or ""

    def get_thread_messages(
        self, bot_id: str, channel_id: str, thread_ts: str | None, limit: int = 20
    ) -> list[dict]:
        """Get recent messages from a specific thread."""
        normalized_thread_ts = self._normalize_thread_ts(thread_ts)
        if not self._pool or not normalized_thread_ts:
            return []
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """SELECT role, content, timestamp FROM messages
                    WHERE bot_id = %s AND channel_id = %s AND thread_ts = %s
                    ORDER BY timestamp DESC LIMIT %s""",
                    (bot_id, channel_id, normalized_thread_ts, limit),
                )
                rows = cur.fetchall()
                return [
                    {"role": r[0], "content": r[1], "timestamp": str(r[2])}
                    for r in reversed(rows)
                ]
        except Exception as e:
            log.warning("Failed to get thread messages: %s", e)
            return []
        finally:
            self._pool.putconn(conn)

    def get_channel_messages(
        self,
        bot_id: str,
        channel_id: str,
        limit: int = 20,
        exclude_thread_ts: str | None = None,
    ) -> list[dict]:
        """Get recent channel-level messages, optionally excluding a thread."""
        if not self._pool:
            return []
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                if exclude_thread_ts:
                    cur.execute(
                        """SELECT role, content, timestamp FROM messages
                        WHERE bot_id = %s AND channel_id = %s AND thread_ts <> %s
                        ORDER BY timestamp DESC LIMIT %s""",
                        (bot_id, channel_id, self._normalize_thread_ts(exclude_thread_ts), limit),
                    )
                else:
                    cur.execute(
                        """SELECT role, content, timestamp FROM messages
                        WHERE bot_id = %s AND channel_id = %s
                        ORDER BY timestamp DESC LIMIT %s""",
                        (bot_id, channel_id, limit),
                    )
                rows = cur.fetchall()
                return [
                    {"role": r[0], "content": r[1], "timestamp": str(r[2])}
                    for r in reversed(rows)
                ]
        except Exception as e:
            log.warning("Failed to get channel messages: %s", e)
            return []
        finally:
            self._pool.putconn(conn)

    def get_recent_messages(
        self, bot_id: str, channel_id: str, thread_ts: str | None = None, limit: int = 20
    ) -> list[dict]:
        """Get recent messages for context.

        If thread_ts is provided and is a real thread, fetch thread messages.
        Otherwise, fetch recent channel-level messages.
        """
        if thread_ts:
            rows = self.get_thread_messages(bot_id, channel_id, thread_ts, limit)
            if len(rows) > 2:
                return rows
        return self.get_channel_messages(bot_id, channel_id, limit)

    # ------------------------------------------------------------------
    # Preference cache: always-inject user preferences regardless of query
    # ------------------------------------------------------------------

    _preference_cache: dict[str, tuple[float, list[str]]] = {}
    _PREFERENCE_TTL = 300  # 5 minutes

    def get_preferences(self, bot_id: str) -> list[str]:
        """Return all preference memories for a bot, with in-memory caching.

        Preferences are user directives (e.g. "use polite speech") that must
        be injected into every prompt regardless of query relevance.

        Cache is invalidated after 5 minutes or when ``invalidate_preference_cache``
        is called (e.g. after memory extraction).
        """
        import time as _time

        cached = self._preference_cache.get(bot_id)
        if cached:
            ts, prefs = cached
            if _time.time() - ts < self._PREFERENCE_TTL:
                return prefs

        if not self._pool:
            return []
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """SELECT content FROM memories
                    WHERE bot_id = %s AND category = 'preference'
                    ORDER BY created_at DESC LIMIT 20""",
                    (bot_id,),
                )
                prefs = [row[0] for row in cur.fetchall()]
            self._preference_cache[bot_id] = (_time.time(), prefs)
            return prefs
        except Exception as e:
            log.warning("Failed to load preferences for %s: %s", bot_id, e)
            return []
        finally:
            self._pool.putconn(conn)

    def invalidate_preference_cache(self, bot_id: str) -> None:
        """Clear cached preferences for a bot after new memories are extracted."""
        self._preference_cache.pop(bot_id, None)

    def search_memories(self, bot_id: str, query_text: str, limit: int = 5) -> list[dict]:
        """Fallback keyword search against PostgreSQL memories."""
        if not self._pool or not query_text.strip():
            return []
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """SELECT content, category, created_at
                    FROM memories
                    WHERE bot_id = %s
                    ORDER BY created_at DESC
                    LIMIT %s""",
                    (bot_id, max(limit * 20, 50)),
                )
                rows = cur.fetchall()
                scored = []
                for content, category, created_at in rows:
                    score = _score_text_match(query_text, content)
                    if score <= 0:
                        continue
                    scored.append(
                        {
                            "content": content,
                            "category": category,
                            "score": score,
                            "created_at": str(created_at),
                        }
                    )
                scored.sort(
                    key=lambda item: (item["score"], item.get("created_at", "")),
                    reverse=True,
                )
                return scored[:limit]
        except Exception as e:
            log.warning("Failed to search memories in PostgreSQL: %s", e)
            return []
        finally:
            self._pool.putconn(conn)

    def search_recent_messages(
        self, bot_id: str, query_text: str, limit: int = 3, lookback: int = 200
    ) -> list[dict]:
        """Search recent messages to cover extraction lag for fresh conversations."""
        if not self._pool or not query_text.strip():
            return []
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """SELECT role, content, timestamp
                    FROM messages
                    WHERE bot_id = %s
                    ORDER BY timestamp DESC
                    LIMIT %s""",
                    (bot_id, lookback),
                )
                rows = cur.fetchall()
                scored = []
                for role, content, timestamp in rows:
                    score = _score_text_match(query_text, content)
                    if score <= 0:
                        continue
                    if role != "user":
                        score *= 0.6
                    if len(content) > 400:
                        score *= 0.7
                    scored.append(
                        {
                            "content": _compact_content(content),
                            "category": "recent_context" if role == "user" else "recent_reply",
                            "score": score,
                            "created_at": str(timestamp),
                        }
                    )
                scored.sort(
                    key=lambda item: (item["score"], item.get("created_at", "")),
                    reverse=True,
                )
                return scored[:limit]
        except Exception as e:
            log.warning("Failed to search recent messages in PostgreSQL: %s", e)
            return []
        finally:
            self._pool.putconn(conn)

    def memory_exists(self, bot_id: str, category: str, content: str) -> bool:
        """Check for an existing near-identical memory to avoid duplicates."""
        if not self._pool or not content.strip():
            return False
        conn = self._pool.getconn()
        try:
            with conn.cursor() as cur:
                cur.execute(
                    """SELECT 1
                    FROM memories
                    WHERE bot_id = %s
                      AND category = %s
                      AND lower(trim(content)) = lower(trim(%s))
                    LIMIT 1""",
                    (bot_id, category, content),
                )
                return cur.fetchone() is not None
        except Exception as e:
            log.warning("Failed to check memory duplicate in PostgreSQL: %s", e)
            return False
        finally:
            self._pool.putconn(conn)
