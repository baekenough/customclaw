"""Hybrid search for memories. Currently keyword-only via OpenSearch nori.

Vector search (pgvector) will be added when Voyage API is available,
then results will be merged using RRF (Reciprocal Rank Fusion).
"""

from __future__ import annotations

import logging
import re

from bot_engine.memory.opensearch_client import OpenSearchClient
from bot_engine.memory.store import MessageStore

log = logging.getLogger(__name__)
_SPACE_RE = re.compile(r"\s+")
_SUFFIXES = (
    "해주세요",
    "해봐",
    "해줘",
    "이어서해",
    "계속해",
    "해",
    "되고",
    "되는중",
    "되는",
    "보강할건",
    "보강",
    "점검",
    "확인",
    "상태",
    "품질",
    "회수",
)
_ACTION_KEYWORDS = ("해", "하자", "해줘", "해봐", "보강", "개선", "점검", "확인", "계속", "이어서")


def _normalize_query(text: str) -> str:
    return _SPACE_RE.sub(" ", (text or "").strip().lower())


def _is_short_action_query(text: str) -> bool:
    normalized = _normalize_query(text)
    return normalized in {"해", "해줘", "해봐", "계속해", "이어서해", "계속 해", "이어서 해"}


def _expand_queries(text: str) -> list[str]:
    normalized = _normalize_query(text)
    if not normalized:
        return []

    variants = [normalized]
    collapsed = normalized.replace(" ", "")
    if collapsed != normalized:
        variants.append(collapsed)

    words = normalized.split()
    if len(words) > 1:
        variants.append(" ".join(words[-2:]))
        variants.append(" ".join(words[:2]))
        if len(words) > 2:
            variants.append(" ".join(words[-3:]))

    for suffix in _SUFFIXES:
        if normalized.endswith(suffix):
            stem = normalized[: -len(suffix)].strip()
            if len(stem) >= 2:
                variants.append(stem)

    if normalized in {"해", "해줘", "해봐", "계속해", "이어서해"}:
        variants.extend(_ACTION_KEYWORDS)

    if any(keyword in normalized for keyword in ("보강", "개선", "점검", "확인")):
        variants.extend(["계속", "이어서", "먼저 말", "기다리지 말고"])

    seen: set[str] = set()
    deduped: list[str] = []
    for variant in variants:
        cleaned = _normalize_query(variant)
        if (len(cleaned) < 2 and cleaned not in {"해"}) or cleaned in seen:
            continue
        seen.add(cleaned)
        deduped.append(cleaned)
    return deduped


class HybridSearch:
    """Search memories using OpenSearch keyword search (nori Korean analyzer).

    TODO: Add pgvector semantic search when Voyage API key is available,
    then merge results using RRF (Reciprocal Rank Fusion).
    """

    def __init__(self) -> None:
        self._os_client: OpenSearchClient | None = None
        self._store = MessageStore()
        try:
            self._os_client = OpenSearchClient()
        except Exception as e:
            log.warning("OpenSearch client init failed: %s", e)

    def search(self, bot_id: str, query_text: str, top_k: int = 5) -> list[dict]:
        """Search memories by keyword.

        Returns list of dicts with 'content', 'category', 'score'.
        Falls back to empty list when OpenSearch is unavailable.
        """
        results: list[dict] = []
        seen: set[tuple[str, str]] = set()
        queries = _expand_queries(query_text) or [query_text]
        is_short_action = _is_short_action_query(query_text)

        if is_short_action:
            for item in self._store.search_memories(bot_id, query_text, limit=top_k * 2):
                if item.get("category") == "preference":
                    item = {**item, "score": item.get("score", 0) + 4.0}
                key = (item.get("category", ""), item.get("content", ""))
                if key in seen:
                    continue
                seen.add(key)
                results.append(item)

            for item in self._store.search_recent_messages(
                bot_id, query_text, limit=max(1, top_k)
            ):
                if item.get("category") == "recent_context":
                    item = {**item, "score": item.get("score", 0) + 2.0}
                key = (item.get("category", ""), item.get("content", ""))
                if key in seen:
                    continue
                seen.add(key)
                results.append(item)

            results.sort(key=lambda item: item.get("score", 0), reverse=True)
            if len(results) >= min(top_k, 3):
                return results[:top_k]

        if self._os_client:
            try:
                for query in queries:
                    for item in self._os_client.search(bot_id, query, top_k=top_k):
                        key = (item.get("category", ""), item.get("content", ""))
                        if key in seen:
                            continue
                        seen.add(key)
                        results.append(item)
            except Exception as e:
                log.warning("OpenSearch memory search failed: %s", e)

        memory_result_count = len(results)

        if len(results) < top_k:
            for query in queries:
                fallback = self._store.search_memories(bot_id, query, limit=top_k)
                for item in fallback:
                    key = (item.get("category", ""), item.get("content", ""))
                    if key in seen:
                        continue
                    seen.add(key)
                    results.append(item)
                    if len(results) >= top_k:
                        break
                if len(results) >= top_k:
                    break

        memory_result_count = len(results)

        if memory_result_count < min(top_k, 3):
            for query in queries:
                recent = self._store.search_recent_messages(
                    bot_id, query, limit=max(1, top_k - len(results))
                )
                for item in recent:
                    key = (item.get("category", ""), item.get("content", ""))
                    if key in seen:
                        continue
                    seen.add(key)
                    results.append(item)
                    if len(results) >= top_k:
                        break
                if len(results) >= top_k:
                    break

        results.sort(key=lambda item: item.get("score", 0), reverse=True)
        return results[:top_k]
