"""
agentnav_issue_analyzer DAG.

Analyzes AgentNav GitHub issues (docs-drift reports) by identifying which
documentation sources changed and publishing per-source analysis requests
to dedicated Redis Streams. Separate analyzer containers consume these
streams and perform deep analysis using the appropriate CLI tool.

Triggered by docs_drift_monitor DAG or manually via Airflow CLI/API.

Graph:
    fetch_issue_details → identify_sources → publish_analysis_requests
"""

from __future__ import annotations

import logging
import os
import re
from datetime import datetime, timedelta, timezone

import requests
from airflow.exceptions import AirflowFailException
from airflow.models.param import Param
from airflow.sdk import Variable, dag, task

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
GITHUB_API_ISSUES_URL = "https://api.github.com/repos/{repo}/issues"
DEFAULT_REQUEST_TIMEOUT = 30  # seconds

STREAM_MAP = {
    "claude-code": "customclaw:claude-analysis",
    "codex": "customclaw:codex-analysis",
    "gemini-cli": "customclaw:gemini-analysis",
}

AGENTNAV_DOC_URLS = {
    "claude-code": "https://agentnav.baekenough.com/claude-code/agents.md",
    "codex": "https://agentnav.baekenough.com/gpt-codex/agents.md",
    "gemini-cli": "https://agentnav.baekenough.com/gemini-cli/agents.md",
}

# Maximum characters of AgentNav document to include in each Redis message.
AGENTNAV_DOC_MAX_CHARS = 8_000


# ---------------------------------------------------------------------------
# Module-level pure helper functions
# ---------------------------------------------------------------------------


def _github_headers(token: str) -> dict[str, str]:
    """Build standard GitHub API request headers.

    Args:
        token: GitHub personal access token.

    Returns:
        Dict of HTTP headers for GitHub API requests.
    """
    return {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }


def _resolve_github_token() -> str:
    """Resolve GitHub token from Airflow Variable or environment variable.

    Lookup order:
    1. Airflow Variable ``agentnav_github_token``
    2. Environment variable ``GITHUB_TOKEN``

    Returns:
        The token string.

    Raises:
        AirflowFailException: If no token is found in either location.
    """
    token = Variable.get("agentnav_github_token", default=None)
    if token:
        return token

    token = os.environ.get("GITHUB_TOKEN")
    if token:
        return token

    raise AirflowFailException(
        "GitHub token not found. Set the Airflow Variable 'agentnav_github_token' "
        "or the environment variable 'GITHUB_TOKEN'."
    )


def _extract_source_sections(body: str) -> dict[str, dict[str, str]]:
    """Parse an issue body to find per-source change sections.

    Scans the issue body for level-2 Markdown headers matching known source
    names (``## claude-code``, ``## codex``, ``## gemini-cli``). For each
    found source, extracts the lines under the "Changes Detected" sub-section,
    categorized by ``- **Added:**`` and ``- **Removed:**`` markers.

    Args:
        body: The raw GitHub issue body text.

    Returns:
        Dict mapping source name to a dict with ``added_text`` and
        ``removed_text`` keys. Only sources that appear in the body are
        included.
    """
    known_sources = set(STREAM_MAP.keys())
    results: dict[str, dict[str, str]] = {}

    lines = body.splitlines()
    current_source: str | None = None
    in_changes_section = False
    current_category: str | None = None  # "added" or "removed"

    for line in lines:
        stripped = line.strip()

        # Detect a top-level source header: "## claude-code" etc.
        h2_match = re.match(r"^##\s+(\S+)\s*$", stripped)
        if h2_match:
            candidate = h2_match.group(1)
            if candidate in known_sources:
                current_source = candidate
                in_changes_section = False
                current_category = None
                if current_source not in results:
                    results[current_source] = {"added_text": "", "removed_text": ""}
            else:
                current_source = None
                in_changes_section = False
                current_category = None
            continue

        if current_source is None:
            continue

        # Detect the "Changes Detected" sub-section header.
        if re.match(r"^###\s+Changes\s+Detected", stripped, re.IGNORECASE):
            in_changes_section = True
            current_category = None
            continue

        # Any other h3 ends the changes section.
        if stripped.startswith("###"):
            in_changes_section = False
            current_category = None
            continue

        if not in_changes_section:
            continue

        # Detect category markers: "- **Added:**" or "- **Removed:**"
        if re.match(r"^-\s+\*\*Added:?\*\*", stripped):
            current_category = "added"
            continue
        if re.match(r"^-\s+\*\*Removed:?\*\*", stripped):
            current_category = "removed"
            continue

        if current_category is None:
            continue

        # Extract content from indented list items: "  - `content`"
        item_match = re.match(r"^-\s+(.+)$", stripped)
        if not item_match:
            continue

        content = item_match.group(1).strip()

        # Skip placeholder and count markers
        if content in ("(none)", "(없음)"):
            continue
        if re.match(r"^_\.\.\.\s+and\s+\d+\s+more_$", content):
            continue

        # Strip backtick wrapping if present
        if content.startswith("`") and content.endswith("`"):
            content = content[1:-1]

        entry = results[current_source]
        key = "added_text" if current_category == "added" else "removed_text"
        entry[key] = (entry[key] + "\n" + content).lstrip("\n")

    return results


# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------


@dag(
    dag_id="agentnav_issue_analyzer",
    description=(
        "Analyzes AgentNav docs-drift issues by comparing official docs "
        "against AgentNav parsed documents"
    ),
    schedule=None,  # triggered externally
    start_date=datetime(2026, 3, 21),
    catchup=False,
    max_active_runs=5,
    params={
        "issue_number": Param(
            default=0,
            type="integer",
            description="GitHub issue number",
        ),
        "repo": Param(
            default="baekenough/AgentNav",
            type="string",
            description="GitHub repo",
        ),
    },
    tags=["agentnav", "issue-analysis", "automated"],
    default_args={
        "owner": "agentnav",
        "retries": 1,
        "retry_delay": timedelta(minutes=5),
    },
    doc_md=__doc__,
)
def agentnav_issue_analyzer() -> None:
    """Orchestrate per-source analysis for AgentNav docs-drift issues."""

    # ------------------------------------------------------------------
    # Task 1: Fetch issue details from GitHub API
    # ------------------------------------------------------------------

    @task()
    def fetch_issue_details(**context) -> dict:
        """Fetch issue details from the GitHub API.

        Retrieves the issue identified by ``issue_number`` and ``repo``
        params and returns a lightweight dict containing only the fields
        needed by downstream tasks.

        Returns:
            Dict with ``number``, ``title``, ``body``, ``labels``, and
            ``html_url`` keys.

        Raises:
            AirflowFailException: On permanent HTTP client errors (4xx
                excluding 429).
            RuntimeError: On transient network or server errors so that
                Airflow's retry mechanism can retry the task.
        """
        issue_number = context["params"]["issue_number"]
        repo = context["params"].get("repo", "baekenough/AgentNav")
        token = _resolve_github_token()
        headers = _github_headers(token)

        url = GITHUB_API_ISSUES_URL.format(repo=repo) + f"/{issue_number}"

        try:
            response = requests.get(
                url, headers=headers, timeout=DEFAULT_REQUEST_TIMEOUT
            )
            response.raise_for_status()
        except requests.exceptions.HTTPError as exc:
            status = exc.response.status_code
            if 400 <= status < 500 and status != 429:
                raise AirflowFailException(
                    f"HTTP {status} fetching issue #{issue_number} from {repo}: {exc}"
                ) from exc
            raise RuntimeError(
                f"HTTP {status} fetching issue #{issue_number} from {repo}: {exc}"
            ) from exc
        except requests.exceptions.RequestException as exc:
            raise RuntimeError(
                f"Failed to fetch issue #{issue_number} from {repo}: {exc}"
            ) from exc

        issue = response.json()
        label_names = [label["name"] for label in issue.get("labels", [])]

        result = {
            "number": issue["number"],
            "title": issue.get("title", ""),
            "body": issue.get("body", "") or "",
            "labels": label_names,
            "html_url": issue.get("html_url", ""),
        }

        log.info(
            "Fetched issue #%d: %s (labels: %s)",
            result["number"],
            result["title"],
            label_names,
        )
        return result

    # ------------------------------------------------------------------
    # Task 2: Identify sources and fetch AgentNav documents
    # ------------------------------------------------------------------

    @task()
    def identify_sources(issue_data: dict) -> list[dict]:
        """Parse the issue body to find changed sources and fetch AgentNav docs.

        For each known source that appears as a ``## <source>`` section in the
        issue body, fetches the corresponding AgentNav agents.md document and
        extracts the added/removed change lines.

        Args:
            issue_data: Output from ``fetch_issue_details``.

        Returns:
            List of dicts, one per identified source, each containing:
            ``name``, ``added_text``, ``removed_text``, ``agentnav_doc``.
            Sources that appear in the issue but have no known stream mapping
            are skipped. Returns an empty list if the issue body is blank.
        """
        body = issue_data.get("body", "") or ""
        if not body:
            log.info("Issue body is empty; no sources to identify.")
            return []

        sections = _extract_source_sections(body)
        if not sections:
            log.info(
                "No known source sections (## claude-code / ## codex / ## gemini-cli) "
                "found in issue #%d.",
                issue_data.get("number", 0),
            )
            return []

        sources: list[dict] = []
        for name, change_data in sections.items():
            doc_url = AGENTNAV_DOC_URLS.get(name)
            agentnav_doc = ""

            if doc_url:
                try:
                    resp = requests.get(doc_url, timeout=DEFAULT_REQUEST_TIMEOUT)
                    resp.raise_for_status()
                    agentnav_doc = resp.text
                    log.info(
                        "Fetched AgentNav doc for %s (%d chars).",
                        name,
                        len(agentnav_doc),
                    )
                except requests.exceptions.HTTPError as exc:
                    status = exc.response.status_code
                    log.warning(
                        "HTTP %d fetching AgentNav doc for %s (%s). "
                        "Continuing without document.",
                        status,
                        name,
                        doc_url,
                    )
                except requests.exceptions.RequestException as exc:
                    log.warning(
                        "Network error fetching AgentNav doc for %s: %s. "
                        "Continuing without document.",
                        name,
                        exc,
                    )
            else:
                log.warning("No AGENTNAV_DOC_URLS entry for source: %s", name)

            sources.append({
                "name": name,
                "added_text": change_data.get("added_text", ""),
                "removed_text": change_data.get("removed_text", ""),
                "agentnav_doc": agentnav_doc,
            })

        log.info(
            "Identified %d source(s) with changes: %s",
            len(sources),
            [s["name"] for s in sources],
        )
        return sources

    # ------------------------------------------------------------------
    # Task 3: Publish per-source analysis requests to Redis Streams
    # ------------------------------------------------------------------

    @task()
    def publish_analysis_requests(
        issue_details: dict,
        sources: list[dict],
        **context,
    ) -> str:
        """Publish analysis requests to per-source Redis Streams.

        For each identified source, writes a message to the stream mapped by
        ``STREAM_MAP`` so that the appropriate analyzer container can pick it
        up and perform a deep comparison of the official doc changes against
        the AgentNav parsed document.

        Args:
            issue_details: Output from ``fetch_issue_details``.
            sources: Output from ``identify_sources``.

        Returns:
            Summary string describing how many requests were published.
        """
        if not sources:
            log.info("No sources identified; skipping analysis requests.")
            return "No sources identified; skipping analysis requests."

        import redis as redis_lib

        repo = context["params"].get("repo", "baekenough/AgentNav")
        redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
        r = redis_lib.from_url(redis_url)

        issue_number = issue_details.get("number", 0)
        issue_url = issue_details.get("html_url", "")

        published = 0
        for source in sources:
            name = source.get("name", "")
            stream = STREAM_MAP.get(name)
            if not stream:
                log.warning("No stream mapping for source: %s — skipping.", name)
                continue

            agentnav_doc = source.get("agentnav_doc", "")
            if len(agentnav_doc) > AGENTNAV_DOC_MAX_CHARS:
                agentnav_doc = agentnav_doc[:AGENTNAV_DOC_MAX_CHARS]
                log.info(
                    "Truncated AgentNav doc for %s to %d chars.",
                    name,
                    AGENTNAV_DOC_MAX_CHARS,
                )

            request_data = {
                "source_name": name,
                "issue_number": str(issue_number),
                "issue_url": issue_url,
                "repo": repo,
                "added_lines": source.get("added_text", ""),
                "removed_lines": source.get("removed_text", ""),
                "agentnav_doc": agentnav_doc,
                "requested_at": datetime.now(timezone.utc).isoformat(),
            }

            msg_id = r.xadd(stream, request_data)
            log.info(
                "Published %s analysis request to %s: %s",
                name,
                stream,
                msg_id,
            )
            published += 1

        summary = f"Published {published} analysis request(s) for issue #{issue_number}"
        log.info(summary)
        return summary

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    details = fetch_issue_details()
    sources = identify_sources(details)
    publish_analysis_requests(details, sources)


# Instantiate the DAG
dag_instance = agentnav_issue_analyzer()
