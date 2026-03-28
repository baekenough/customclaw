"""
Docs Drift Monitor DAG.

Monitors official documentation for Claude Code, Codex, and Gemini CLI,
compares content against a stored baseline, creates a consolidated GitHub
issue when changes are detected, and triggers the agentnav_issue_analyzer
DAG for deep analysis.

Graph:
    fetch_doc_indexes -> detect_changes -> create_issue_if_needed -> trigger_analysis
                                                                  -> update_baselines
"""

from __future__ import annotations

import logging
import os
from datetime import datetime, timedelta, timezone

import requests
from airflow.exceptions import AirflowFailException
from airflow.sdk import Variable, dag, task

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
GITHUB_API_ISSUES_URL = "https://api.github.com/repos/{repo}/issues"

DEFAULT_REQUEST_TIMEOUT = 30  # seconds

DOCS_DRIFT_LABEL = "docs-drift"
ISSUE_LABELS = ["automated", DOCS_DRIFT_LABEL]

DOC_SOURCES = [
    {
        "name": "claude-code",
        "llms_txt_url": "https://code.claude.com/docs/llms.txt",
        "fallback_url": "https://code.claude.com/docs",
        "variable_key": "docs_baseline_claude_code",
    },
    {
        "name": "codex",
        "llms_txt_url": "https://developers.openai.com/codex/llms.txt",
        "fallback_url": "https://developers.openai.com/codex",
        "variable_key": "docs_baseline_codex",
    },
    {
        "name": "gemini-cli",
        "llms_txt_url": "https://geminicli.com/llms.txt",
        "fallback_url": "https://geminicli.com/docs",
        "variable_key": "docs_baseline_gemini_cli",
    },
]

# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------
default_args = {
    "owner": "omc",
    "retries": 1,
    "retry_delay": timedelta(minutes=5),
}


@dag(
    dag_id="docs_drift_monitor",
    description=(
        "Monitors Claude Code and Codex official documentation for "
        "structural changes and analyzes impact on customclaw"
    ),
    schedule="0 */3 * * *",
    start_date=datetime(2026, 3, 21),
    catchup=False,
    tags=["project:omc", "project:customclaw", "drift-detection", "automated"],
    default_args=default_args,
    doc_md=__doc__,
)
def docs_drift_monitor() -> None:
    """Orchestrate documentation drift monitoring."""

    # ------------------------------------------------------------------
    # Task 1: Fetch documentation indexes
    # ------------------------------------------------------------------

    @task()
    def fetch_doc_indexes() -> list[dict]:
        """Fetch documentation index content from each configured source.

        For each source, attempts to fetch ``llms_txt_url`` first. If that
        fails, falls back to ``fallback_url``. Sources where both fetches
        fail are logged and skipped.

        Returns:
            List of dicts with ``name``, ``content``, and ``url`` keys.
        """
        results: list[dict] = []

        for source in DOC_SOURCES:
            name = source["name"]
            content = None
            used_url = None

            # Try primary llms.txt URL first.
            try:
                resp = _get(source["llms_txt_url"])
                content = resp.text
                used_url = source["llms_txt_url"]
                log.info(
                    "Fetched llms.txt for %s (%d chars).",
                    name,
                    len(content),
                )
            except (RuntimeError, AirflowFailException) as exc:
                log.warning(
                    "Primary fetch failed for %s: %s. Trying fallback.",
                    name,
                    exc,
                )

            # If primary failed, try fallback URL.
            if content is None:
                try:
                    resp = _get(source["fallback_url"])
                    content = resp.text
                    used_url = source["fallback_url"]
                    log.info(
                        "Fetched fallback for %s (%d chars).",
                        name,
                        len(content),
                    )
                except (RuntimeError, AirflowFailException) as exc:
                    log.warning(
                        "Fallback fetch also failed for %s: %s. "
                        "Skipping this source.",
                        name,
                        exc,
                    )
                    continue

            results.append({
                "name": name,
                "content": content,
                "url": used_url,
            })

        log.info(
            "Fetched documentation from %d/%d sources.",
            len(results),
            len(DOC_SOURCES),
        )
        return results

    # ------------------------------------------------------------------
    # Task 2: Detect changes against stored baselines
    # ------------------------------------------------------------------

    @task()
    def detect_changes(fetched_docs: list[dict]) -> list[dict]:
        """Compare fetched documentation against stored baselines.

        For each source, loads the baseline from an Airflow Variable and
        computes added/removed lines focusing on structural elements
        (paths, section headers). Baselines are NOT updated here; that
        happens in ``update_baselines`` after issue creation to ensure
        changes are never silently lost on issue-creation failure.

        Args:
            fetched_docs: Output from ``fetch_doc_indexes``.

        Returns:
            List of dicts with change details. Only sources with actual
            changes are included.
        """
        if not fetched_docs:
            log.info("No fetched docs to compare.")
            return []

        # Build a lookup from DOC_SOURCES for variable keys.
        variable_keys = {
            src["name"]: src["variable_key"] for src in DOC_SOURCES
        }

        changed_sources: list[dict] = []

        for doc in fetched_docs:
            name = doc["name"]
            current_content = doc.get("content", "")
            variable_key = variable_keys.get(name)

            if not variable_key:
                log.warning(
                    "No variable_key mapping for source %s. Skipping.",
                    name,
                )
                continue

            baseline = Variable.get(variable_key, default="")

            if current_content == baseline:
                log.info("No changes detected for %s.", name)
                continue

            # Compute diff focusing on structural lines.
            added, removed = _compute_structural_diff(
                baseline, current_content
            )

            log.info(
                "Changes detected for %s: +%d / -%d structural lines.",
                name,
                len(added),
                len(removed),
            )

            changed_sources.append({
                "name": name,
                "added": added,
                "removed": removed,
                "has_changes": True,
            })

        log.info(
            "Change detection complete: %d/%d sources have changes.",
            len(changed_sources),
            len(fetched_docs),
        )
        return changed_sources

    # ------------------------------------------------------------------
    # Task 3: Create consolidated GitHub issue if needed
    # ------------------------------------------------------------------

    @task()
    def create_issue_if_needed(changed_sources: list[dict]) -> dict | None:
        """Create a consolidated GitHub issue for detected doc changes.

        Checks for existing open issues to avoid duplicates, then creates
        a single consolidated issue covering all sources with changes.

        Args:
            changed_sources: Output from ``detect_changes``.

        Returns:
            Dict with ``issue_number``, ``issue_url``, and ``repo`` keys
            on success, or ``None`` if no issue was created.
        """
        if not changed_sources:
            log.info("No changed sources. Skipping issue creation.")
            return None

        github_token = _resolve_github_token()
        headers = _github_headers(github_token)
        target_repo = Variable.get(
            "agentnav_github_repo", default="baekenough/AgentNav"
        )

        source_names = ", ".join(s["name"] for s in changed_sources)
        today = datetime.now(tz=timezone.utc).strftime("%Y-%m-%d")
        issue_title = (
            f"[Docs Drift] {source_names} documentation changes "
            f"detected — {today}"
        )

        # Dedup: check for existing open issues with docs-drift label.
        if _has_existing_drift_issue(
            target_repo=target_repo,
            headers=headers,
            source_names=source_names,
        ):
            log.info(
                "An open docs-drift issue already exists for %s. "
                "Skipping creation.",
                source_names,
            )
            return None

        issue_body = _build_drift_issue_body(
            impactful=changed_sources,
            source_names=source_names,
        )

        api_url = GITHUB_API_ISSUES_URL.format(repo=target_repo)
        payload = {
            "title": issue_title,
            "body": issue_body,
            "labels": list(ISSUE_LABELS),
        }

        try:
            resp = requests.post(
                api_url,
                json=payload,
                headers=headers,
                timeout=DEFAULT_REQUEST_TIMEOUT,
            )
            resp.raise_for_status()
            issue_data = resp.json()
            issue_number = issue_data.get("number")
            issue_url = issue_data.get("html_url", "")
            log.info(
                "Created docs-drift issue #%s: %s",
                issue_number,
                issue_url,
            )
            return {
                "issue_number": issue_number,
                "issue_url": issue_url,
                "repo": target_repo,
            }
        except requests.exceptions.HTTPError as exc:
            status = exc.response.status_code
            resp_body = exc.response.text[:500]
            log.error(
                "Failed to create docs-drift issue: HTTP %s — %s",
                status,
                resp_body,
            )
        except requests.exceptions.RequestException as exc:
            log.error("Network error creating docs-drift issue: %s", exc)

        return None

    # ------------------------------------------------------------------
    # Task 4: Update baselines after issue creation
    # ------------------------------------------------------------------

    @task()
    def update_baselines(
        changed_sources: list[dict],
        issue_info: dict | None,
        fetched_docs: list[dict],
    ) -> str:
        """Update baselines after issue creation (or if no issue needed).

        Runs after ``create_issue_if_needed`` to ensure that a failure
        during issue creation does not silently discard detected changes.

        Args:
            changed_sources: Output from ``detect_changes``.
            issue_info: Output from ``create_issue_if_needed`` (may be None).
            fetched_docs: Output from ``fetch_doc_indexes``.

        Returns:
            A summary string listing updated sources.
        """
        if not changed_sources:
            return "No changes to update."

        variable_keys = {
            src["name"]: src["variable_key"] for src in DOC_SOURCES
        }
        doc_content = {doc["name"]: doc["content"] for doc in fetched_docs}

        updated: list[str] = []
        for source in changed_sources:
            name = source["name"]
            key = variable_keys.get(name)
            content = doc_content.get(name)
            if key and content:
                Variable.set(key, content)
                updated.append(name)
                log.info("Baseline updated for %s.", name)

        return f"Updated baselines: {', '.join(updated)}"

    # ------------------------------------------------------------------
    # Task 5: Trigger agentnav_issue_analyzer DAG for deep analysis
    # ------------------------------------------------------------------

    @task()
    def trigger_analysis(issue_info: dict | None, **context) -> str:
        """Trigger the agentnav_issue_analyzer DAG for deep analysis.

        Uses the Airflow REST API to trigger the analysis DAG with the
        issue number and repository as parameters.

        Args:
            issue_info: Output from ``create_issue_if_needed``.

        Returns:
            A summary string describing the trigger result.
        """
        if not issue_info:
            log.info("No issue created, skipping analysis trigger.")
            return "No issue created, skipping analysis trigger."

        import httpx

        issue_number = issue_info["issue_number"]
        repo = issue_info.get("repo", "baekenough/AgentNav")

        airflow_url = os.environ.get("AIRFLOW_API_URL", "http://localhost:8080")
        airflow_user = os.environ.get("AIRFLOW_API_USER", "admin")
        airflow_password = os.environ.get("AIRFLOW_API_PASSWORD", "")

        # Get auth token
        try:
            token_resp = httpx.post(
                f"{airflow_url}/auth/token",
                json={"username": airflow_user, "password": airflow_password},
                timeout=10,
            )
            token = token_resp.json()["access_token"]
        except Exception as exc:
            log.error("Failed to get Airflow auth token: %s", exc)
            return f"Auth failed: {exc}"

        # Trigger DAG
        try:
            resp = httpx.post(
                f"{airflow_url}/api/v2/dags/agentnav_issue_analyzer/dagRuns",
                headers={"Authorization": f"Bearer {token}"},
                json={
                    "conf": {
                        "issue_number": issue_number,
                        "repo": repo,
                    }
                },
                timeout=10,
            )
            resp.raise_for_status()
            run_id = resp.json().get("dag_run_id", "unknown")
            log.info(
                "Triggered agentnav_issue_analyzer for issue #%s: %s",
                issue_number,
                run_id,
            )
            return f"Triggered analysis for issue #{issue_number}: {run_id}"
        except Exception as exc:
            log.error("Failed to trigger analysis DAG: %s", exc)
            return f"Trigger failed: {exc}"

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    fetched = fetch_doc_indexes()
    changes = detect_changes(fetched)
    issue_info = create_issue_if_needed(changes)
    trigger_analysis(issue_info)
    update_baselines(changes, issue_info, fetched)  # AFTER issue creation


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


def _get(
    url: str,
    headers: dict[str, str] | None = None,
    params: dict | None = None,
) -> requests.Response:
    """Perform an HTTP GET with transient/permanent error distinction.

    Transient errors (timeout, connection error, 5xx, 429) raise a regular
    ``RuntimeError`` so that Airflow's built-in retry mechanism can retry the
    task. Permanent client errors (4xx except 429) raise
    ``AirflowFailException`` to skip retries immediately.

    Args:
        url: The URL to fetch.
        headers: Optional HTTP headers.
        params: Optional query parameters.

    Returns:
        The :class:`requests.Response` object on success.

    Raises:
        RuntimeError: For transient errors (timeout, connection, 5xx, 429).
        AirflowFailException: For permanent client errors (4xx except 429).
    """
    try:
        response = requests.get(
            url, headers=headers, params=params,
            timeout=DEFAULT_REQUEST_TIMEOUT,
        )
        response.raise_for_status()
        return response
    except requests.exceptions.Timeout as exc:
        raise RuntimeError(
            f"Request timed out after {DEFAULT_REQUEST_TIMEOUT}s: {url}"
        ) from exc
    except requests.exceptions.ConnectionError as exc:
        raise RuntimeError(
            f"Connection error while fetching: {url}"
        ) from exc
    except requests.exceptions.HTTPError as exc:
        status = exc.response.status_code
        if 400 <= status < 500 and status != 429:
            raise AirflowFailException(
                f"HTTP {status} from {url}: {exc}"
            ) from exc
        raise RuntimeError(
            f"HTTP {status} from {url}: {exc}"
        ) from exc


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
        "GitHub token not found. Set the Airflow Variable "
        "'agentnav_github_token' or the environment variable 'GITHUB_TOKEN'."
    )


def _compute_structural_diff(
    baseline: str, current: str
) -> tuple[list[str], list[str]]:
    """Compute structural diff between baseline and current content.

    Focuses on lines that represent document structure: paths (starting
    with ``/``), section headers (starting with ``#``), and other
    meaningful content lines.

    Args:
        baseline: The previously stored content.
        current: The newly fetched content.

    Returns:
        Tuple of (added_lines, removed_lines).
    """
    baseline_lines = set(baseline.splitlines())
    current_lines = set(current.splitlines())

    raw_added = current_lines - baseline_lines
    raw_removed = baseline_lines - current_lines

    added = _filter_structural_lines(raw_added)
    removed = _filter_structural_lines(raw_removed)

    return sorted(added), sorted(removed)


def _filter_structural_lines(lines: set[str]) -> list[str]:
    """Filter lines to retain only structural documentation elements.

    Retains lines starting with ``/``, ``#``, or ``##`` after stripping
    whitespace. Aggressively filters HTML, CSS, JavaScript, and other
    markup noise that appears when fetching fallback HTML pages.

    Args:
        lines: Set of raw diff lines.

    Returns:
        List of structural lines.
    """
    import re

    # Patterns that indicate HTML/CSS/JS/JSON noise
    noise_patterns = re.compile(
        r"(?:"
        r"<[a-z/!]"          # HTML tags
        r"|^\s*[)};\]]"      # closing brackets/braces
        r"|class=\""          # CSS class attributes
        r"|data-"            # data attributes
        r"|style=\""         # inline styles
        r"|src=\""           # image/script sources
        r"|href=\""          # links in HTML attributes
        r"|\.(?:js|css|svg|png|jpg|woff)" # asset file extensions
        r"|xmlns"            # XML namespaces
        r"|viewport"         # meta viewport
        r"|backdrop-blur"    # Tailwind/CSS utilities
        r"|@media"           # CSS media queries
        r"|function\s*\("    # JS functions
        r"|window\."         # JS window object
        r"|document\."       # JS document object
        r"|^\s*#[\w-]+\s*[{,:]"   # CSS ID selectors (#category-select {)
        r"|^\s*\.[\w-]+\s*[{,:]"  # CSS class selectors (.container {)
        r"|\"[\w-]+\":"      # JSON keys ("key":)
        r"|^\s*\*\s*\{"      # CSS universal selector (* {)
        r"|^\s*@"            # CSS at-rules (@import, @keyframes, etc.)
        r"|banner|dropdown|clickout|modal|tooltip|popover"  # UI component noise
        r"|^\s*&\."              # SCSS/CSS nested selectors (&.danger {)
        r"|content-container"    # specific CSS ID noise
        r"|localization-select"  # specific CSS noise
        r"|\{\s*$"              # lines ending with opening brace (CSS rules)
        r"|;\s*$"               # lines ending with semicolon (CSS declarations)
        r")",
        re.IGNORECASE,
    )

    # Pattern for valid markdown headers: #{1,6} followed by space and word char
    markdown_header_re = re.compile(r"^#{1,6}\s+\w")

    structural: list[str] = []
    for line in lines:
        stripped = line.strip()
        if not stripped or len(stripped) < 3:
            continue
        # Skip lines that are clearly HTML/CSS/JS noise
        if noise_patterns.search(stripped):
            continue
        # Skip very long lines (likely minified code or HTML attributes)
        if len(stripped) > 300:
            continue
        # Keep structural documentation lines
        if stripped.startswith("/"):
            structural.append(stripped)
        elif stripped.startswith("#") and markdown_header_re.match(stripped):
            structural.append(stripped)
        # Keep llms.txt-style page entries: "- [Title](url): description"
        elif stripped.startswith("- "):
            structural.append(stripped)
        # Keep markdown list items with links that represent doc pages
        elif re.match(r"^[-*]\s+\[", stripped):
            structural.append(stripped)
    return structural


def _has_existing_drift_issue(
    *,
    target_repo: str,
    headers: dict[str, str],
    source_names: str,
) -> bool:
    """Check whether an open docs-drift issue already exists.

    Searches open issues with the ``docs-drift`` label and checks if
    any title contains the same source names.

    Args:
        target_repo: GitHub repository in ``owner/repo`` format.
        headers: GitHub API headers.
        source_names: Comma-separated source names to match.

    Returns:
        True if a matching open issue exists.
    """
    list_url = GITHUB_API_ISSUES_URL.format(repo=target_repo)

    try:
        resp = requests.get(
            list_url,
            params={
                "state": "open",
                "labels": DOCS_DRIFT_LABEL,
                "per_page": 30,
                "sort": "created",
                "direction": "desc",
            },
            headers=headers,
            timeout=DEFAULT_REQUEST_TIMEOUT,
        )
        resp.raise_for_status()
        existing_issues = resp.json()

        for issue in existing_issues:
            title = issue.get("title", "")
            if source_names in title:
                return True

    except requests.exceptions.RequestException as exc:
        log.warning(
            "Failed to check existing drift issues: %s. "
            "Proceeding without dedup — may create duplicates.",
            exc,
        )

    return False


def _build_drift_issue_body(
    *,
    impactful: list[dict],
    source_names: str,
) -> str:
    """Build the Markdown body for a docs-drift GitHub issue.

    Creates a consolidated report covering all impacted sources with
    their detected changes and impact analyses.

    Args:
        impactful: List of analysis result dicts with impact.
        source_names: Comma-separated names for the header.

    Returns:
        Formatted Markdown string for the issue body.
    """
    utc_now = datetime.now(tz=timezone.utc).strftime("%Y-%m-%d %H:%M UTC")

    sections: list[str] = [
        "# Documentation Drift Report",
        "",
        f"**Detected at:** {utc_now}",
        f"**Sources with changes:** {source_names}",
        "",
    ]

    # Truncate individual lines; defined once, used inside the loop.
    def _truncate_line(line: str, max_len: int = 120) -> str:
        return line[:max_len] + "..." if len(line) > max_len else line

    for result in impactful:
        name = result.get("name", "unknown")
        added = result.get("added", [])
        removed = result.get("removed", [])
        analysis = result.get("analysis", "")

        total_changes = len(added) + len(removed)
        if total_changes <= 5:
            severity = "minor"
        elif total_changes <= 20:
            severity = "moderate"
        else:
            severity = "major"

        added_text = (
            "\n".join(f"  - `{_truncate_line(line)}`" for line in added[:15])
            if added
            else "  - (none)"
        )
        if len(added) > 15:
            added_text += f"\n  - _... and {len(added) - 15} more_"

        removed_text = (
            "\n".join(f"  - `{_truncate_line(line)}`" for line in removed[:15])
            if removed
            else "  - (none)"
        )
        if len(removed) > 15:
            removed_text += f"\n  - _... and {len(removed) - 15} more_"

        sections += [
            f"## {name} — severity: {severity}",
            "",
            "### Changes Detected",
            f"- **Added:**\n{added_text}",
            f"- **Removed:**\n{removed_text}",
            "",
            "### Impact Analysis",
            analysis or "_No analysis available._",
            "",
            "---",
            "",
        ]

    sections += [
        "## Action Items",
        "",
        "- [ ] Review changes and update affected agents/skills",
        "- [ ] Update guides if documentation references changed",
        "- [ ] Test compatibility with updated documentation",
        "",
        "---",
        "",
        "_This issue was created automatically by the "
        "`docs_drift_monitor` Airflow DAG._",
    ]

    body = "\n".join(sections)
    if len(body) > 10000:
        body = body[:9800] + "\n\n---\n\n_Issue body truncated (exceeded 10,000 characters)._"
    return body


# Instantiate the DAG
dag_instance = docs_drift_monitor()
