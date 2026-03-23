"""OpenSearch client for Korean full-text search with nori analyzer."""

from __future__ import annotations

import logging
import os
from datetime import datetime

from opensearchpy import OpenSearch

log = logging.getLogger(__name__)

INDEX_NAME = "customclaw-memories"

INDEX_SETTINGS = {
    "settings": {
        "analysis": {
            "analyzer": {
                "korean": {
                    "type": "custom",
                    "tokenizer": "nori_tokenizer",
                    "filter": ["nori_readingform", "lowercase"],
                }
            }
        }
    },
    "mappings": {
        "properties": {
            "bot_id": {"type": "keyword"},
            "user_id": {"type": "keyword"},
            "category": {"type": "keyword"},
            "content": {"type": "text", "analyzer": "korean"},
            "created_at": {"type": "date"},
        }
    },
}


class OpenSearchClient:
    """OpenSearch client for indexing and searching memories."""

    def __init__(self) -> None:
        url = os.environ.get("OPENSEARCH_URL", "http://opensearch:9200")
        self._client = OpenSearch(
            hosts=[url],
            use_ssl=False,
            verify_certs=False,
        )
        self._ensure_index()

    def _ensure_index(self) -> None:
        """Create the memories index if it doesn't exist."""
        try:
            if not self._client.indices.exists(index=INDEX_NAME):
                self._client.indices.create(index=INDEX_NAME, body=INDEX_SETTINGS)
                log.info("Created OpenSearch index: %s", INDEX_NAME)
        except Exception as e:
            log.warning("Failed to ensure OpenSearch index: %s", e)

    def index_memory(
        self,
        memory_id: str,
        bot_id: str,
        content: str,
        category: str,
        user_id: str = "",
        created_at: datetime | None = None,
    ) -> bool:
        """Index a memory document.

        Uses memory_id as document _id for consistency with PostgreSQL.
        """
        try:
            doc = {
                "bot_id": bot_id,
                "user_id": user_id,
                "category": category,
                "content": content,
                "created_at": (created_at or datetime.utcnow()).isoformat(),
            }
            self._client.index(index=INDEX_NAME, id=memory_id, body=doc)
            return True
        except Exception as e:
            log.warning("Failed to index memory %s: %s", memory_id, e)
            return False

    def search(self, bot_id: str, query: str, top_k: int = 5) -> list[dict]:
        """Search memories by Korean keyword using nori analyzer.

        Returns list of dicts with 'id', 'content', 'category', 'score'.
        """
        try:
            body = {
                "query": {
                    "bool": {
                        "must": [
                            {
                                "match": {
                                    "content": {
                                        "query": query,
                                        "analyzer": "korean",
                                    }
                                }
                            },
                        ],
                        "filter": [
                            {"term": {"bot_id": bot_id}},
                        ],
                    }
                },
                "size": top_k,
            }
            resp = self._client.search(index=INDEX_NAME, body=body)
            results = []
            for hit in resp["hits"]["hits"]:
                results.append(
                    {
                        "id": hit["_id"],
                        "content": hit["_source"]["content"],
                        "category": hit["_source"].get("category", ""),
                        "score": hit["_score"],
                    }
                )
            return results
        except Exception as e:
            log.warning("OpenSearch search failed: %s", e)
            return []

    def delete(self, memory_id: str) -> bool:
        """Delete a memory document."""
        try:
            self._client.delete(index=INDEX_NAME, id=memory_id)
            return True
        except Exception as e:
            log.warning("Failed to delete memory %s: %s", memory_id, e)
            return False
