"""Auto-extract facts, decisions, and preferences from conversations using Claude CLI."""

from __future__ import annotations

import json
import logging
import os
import re
import subprocess
import uuid

import psycopg2

from bot_engine.memory.opensearch_client import OpenSearchClient
from bot_engine.memory.store import MessageStore

log = logging.getLogger(__name__)

CLAUDE_CLI_PATH = os.environ.get("CLAUDE_CLI_PATH", "claude")

EXTRACTION_PROMPT_TEMPLATE = """Analyze the following conversation and extract important information that should be remembered for future conversations.

Extract ONLY genuinely important items in these categories:
- **fact**: Factual information about the project, user, or system
- **decision**: Decisions made during the conversation
- **preference**: User preferences about how things should be done

Important extraction rules:
- Prefer durable behavior preferences over one-off task requests.
- If the user gives a short imperative follow-up like "해", "계속 해", "이어서 해", or "보강해", use the surrounding conversation to rewrite it into a clear standalone preference when it reinforces an ongoing instruction.
- When the user asks the assistant to keep checking, keep improving, or continue without waiting, extract that as a **preference**.
- Keep each memory self-contained and specific enough to reuse later.

Conversation:
{conversation}

Respond with a JSON array ONLY. No other text. Example:
[
  {{"category": "fact", "content": "사용자는 Airflow 3.1.8을 사용 중"}},
  {{"category": "decision", "content": "마스터 봇은 sonnet 모델을 사용하기로 결정"}},
  {{"category": "preference", "content": "사용자는 간결한 답변을 선호"}}
]

If nothing important to extract, respond with: []"""

_CONTINUE_PATTERNS = (
    "계속 해",
    "계속해",
    "이어서 해",
    "이어서해",
    "계속 보강",
    "보강해",
    "보강 할건 계속 보강해",
    "보강할건 계속 보강해",
)
_PROACTIVE_PATTERNS = (
    "기다리지",
    "대기하지",
    "먼저 말",
    "이어서 말",
    "추가로 얘기",
    "계속 점검",
    "끝까지 점검",
)


class MemoryExtractor:
    """Extract and store memories from conversations."""

    def __init__(self) -> None:
        self._dsn = os.environ.get("DATABASE_DSN", "")
        self._store = MessageStore()
        self._os_client: OpenSearchClient | None = None
        try:
            self._os_client = OpenSearchClient()
        except Exception as e:
            log.warning("OpenSearch client init failed: %s", e)

    def extract_and_store(
        self, bot_id: str, user_id: str, conversation: list[dict]
    ) -> int:
        """Extract memories from conversation and store in PostgreSQL + OpenSearch.

        Returns number of memories extracted.
        """
        if not conversation or len(conversation) < 2:
            return 0

        # Build conversation text from last 10 messages; truncate each to 500 chars
        conv_lines = []
        for msg in conversation[-10:]:
            role = "User" if msg.get("role") == "user" else "Assistant"
            content = msg.get("content", "")[:500]
            conv_lines.append(f"{role}: {content}")
        conv_text = "\n".join(conv_lines)

        prompt = EXTRACTION_PROMPT_TEMPLATE.format(conversation=conv_text)
        extracted = self._call_claude(prompt)
        if not extracted:
            return 0

        memories = self._parse_extraction(extracted)
        memories.extend(self._infer_preferences(conversation))
        if not memories:
            return 0

        stored = 0
        for mem in memories:
            try:
                memory_id = str(uuid.uuid4())
                category = mem.get("category", "fact")
                content = re.sub(r"\s+", " ", mem.get("content", "")).strip()
                if not content:
                    continue
                if self._store.memory_exists(bot_id, category, content):
                    continue

                if self._store_pg(memory_id, bot_id, user_id, category, content):
                    if self._os_client:
                        self._os_client.index_memory(
                            memory_id=memory_id,
                            bot_id=bot_id,
                            content=content,
                            category=category,
                            user_id=user_id,
                        )
                    stored += 1
            except Exception as e:
                log.warning("Failed to store memory: %s", e)

        if stored:
            log.info(
                "Extracted and stored %d memories for bot %s", stored, bot_id
            )
        return stored

    def _infer_preferences(self, conversation: list[dict]) -> list[dict]:
        """Derive durable preferences from short imperative user follow-ups."""
        user_messages = [
            re.sub(r"\s+", " ", (msg.get("content") or "")).strip()
            for msg in conversation
            if msg.get("role") == "user" and (msg.get("content") or "").strip()
        ]
        if not user_messages:
            return []

        joined = " ".join(user_messages).lower()
        inferred: list[dict] = []

        if any(pattern in joined for pattern in _CONTINUE_PATTERNS):
            inferred.append(
                {
                    "category": "preference",
                    "content": (
                        "사용자는 개선이나 보강 요청을 시작한 뒤에는 멈추지 말고 계속 이어서 보강하기를 원함"
                    ),
                }
            )

        if any(pattern in joined for pattern in _PROACTIVE_PATTERNS):
            inferred.append(
                {
                    "category": "preference",
                    "content": (
                        "사용자는 확인이나 점검 요청 후 추가로 볼 것이 있으면 기다리지 말고 먼저 이어서 보고하기를 원함"
                    ),
                }
            )

        # Extremely short follow-ups like "해" are only meaningful when the
        # surrounding conversation already indicates a continue/keep-going intent.
        if user_messages[-1] in {"해", "해줘", "해봐"} and any(
            pattern in joined for pattern in _CONTINUE_PATTERNS
        ):
            inferred.append(
                {
                    "category": "preference",
                    "content": (
                        "사용자는 짧은 후속 지시라도 직전 맥락을 이어받아 작업을 계속 진행하기를 원함"
                    ),
                }
            )

        deduped: list[dict] = []
        seen: set[tuple[str, str]] = set()
        for item in inferred:
            key = (item["category"], item["content"])
            if key in seen:
                continue
            seen.add(key)
            deduped.append(item)
        return deduped

    def _call_claude(self, prompt: str) -> str | None:
        """Call Claude CLI (haiku) for extraction.

        Returns raw output string or None on failure.
        """
        try:
            env = {
                **os.environ,
                "HOME": os.environ.get("CONTAINER_HOME", "/home/appuser"),
                "NO_COLOR": "1",
            }
            result = subprocess.run(
                [
                    CLAUDE_CLI_PATH, "-p", prompt,
                    "--model", "haiku",
                    "--max-turns", "1",
                ],
                env=env,
                capture_output=True,
                text=True,
                timeout=30,
                stdin=subprocess.DEVNULL,
            )
            if result.returncode != 0:
                return None
            return result.stdout.strip() or None
        except Exception as e:
            log.warning("Claude CLI extraction failed: %s", e)
            return None

    def _parse_extraction(self, text: str) -> list[dict]:
        """Parse JSON array from Claude's response.

        Handles plain JSON and markdown code blocks.
        """
        text = text.strip()
        if "```" in text:
            match = re.search(r"```(?:json)?\s*\n?(.*?)\n?\s*```", text, re.DOTALL)
            if match:
                text = match.group(1).strip()
        try:
            parsed = json.loads(text)
            if isinstance(parsed, list):
                return [m for m in parsed if isinstance(m, dict) and "content" in m]
        except json.JSONDecodeError:
            pass
        return []

    def _store_pg(
        self,
        memory_id: str,
        bot_id: str,
        user_id: str,
        category: str,
        content: str,
    ) -> bool:
        """Store memory in PostgreSQL.

        Uses a zero-vector placeholder for the embedding column until
        Voyage API is available.
        """
        if not self._dsn:
            return False
        try:
            conn = psycopg2.connect(self._dsn)
            with conn.cursor() as cur:
                zero_vec = "[" + ",".join(["0"] * 1024) + "]"
                cur.execute(
                    """INSERT INTO memories
                        (id, bot_id, user_id, category, content, embedding)
                        VALUES (%s, %s, %s, %s, %s, %s::vector)""",
                    (memory_id, bot_id, user_id, category, content, zero_vec),
                )
            conn.commit()
            conn.close()
            return True
        except Exception as e:
            log.warning("Failed to store memory in PostgreSQL: %s", e)
            return False
