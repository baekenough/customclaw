"""
omc_pr_analyzer DAG.

Analyzes new GitHub Pull Requests on a configurable repository by publishing
analysis requests to a Redis Stream. A separate worker process consumes
the stream and performs the actual Claude-powered analysis.

The target repository defaults to ``baekenough/oh-my-customcode`` but can be
overridden via the ``repo`` DAG param (e.g. ``baekenough/customclaw``).

Triggered externally via GitHub Actions webhook → SSH → airflow dags trigger.

Graph:
    fetch_pr_details ─► determine_analysis_scope ─► request_worker_analysis
"""

from __future__ import annotations

import json
import logging
import os
import re
from datetime import datetime, timedelta, timezone

import requests
from airflow.sdk import dag, task
from airflow.exceptions import AirflowFailException
from airflow.sdk import Variable
from airflow.models.param import Param

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
GITHUB_API_PULLS_URL = "https://api.github.com/repos/{repo}/pulls"
DEFAULT_REQUEST_TIMEOUT = 30
TARGET_REPO = "baekenough/oh-my-customcode"

# PR size thresholds for scope determination
SMALL_PR_MAX_FILES = 5
SMALL_PR_MAX_LINES = 100
MEDIUM_PR_MAX_FILES = 15
MEDIUM_PR_MAX_LINES = 500

MAX_TURNS_STANDARD = 8
MAX_TURNS_DEEP = 12
MAX_TURNS_LARGE = 18


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


def _extract_linked_issue(pr_title: str, pr_body: str) -> str:
    """Extract the linked issue number from a PR title or body.

    Checks for GitHub closing keywords (fixes, closes, resolves) followed by
    an issue reference, then falls back to a bare ``#NNN`` reference in the
    PR title.

    Args:
        pr_title: The pull request title string.
        pr_body: The pull request body/description string.

    Returns:
        The linked issue number as a string, or an empty string if none found.
    """
    keyword_pattern = re.compile(
        r'(?:fixes|closes|resolves|fix|close|resolve)\s+#(\d+)',
        re.IGNORECASE,
    )

    for text in (pr_body, pr_title):
        if not text:
            continue
        match = keyword_pattern.search(text)
        if match:
            return match.group(1)

    # Fallback: bare #NNN in PR title
    bare_pattern = re.compile(r'#(\d+)')
    if pr_title:
        match = bare_pattern.search(pr_title)
        if match:
            return match.group(1)

    return ""


# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------
default_args = {
    "owner": "omc",
    "retries": 1,
    "retry_delay": timedelta(minutes=5),
}


@dag(
    dag_id="omc_pr_analyzer",
    description=(
        "Fetches GitHub PR details from a configurable repository and publishes "
        "a PR analysis request to Redis Stream."
    ),
    schedule=None,
    start_date=datetime(2026, 3, 20),
    catchup=False,
    max_active_runs=10,
    params={
        "pr_number": Param(
            default=0,
            type="integer",
            description="GitHub PR number to analyze",
        ),
        "repo": Param(
            default="baekenough/oh-my-customcode",
            type="string",
            description="GitHub repository (owner/repo)",
        ),
    },
    tags=["project:omc", "project:customclaw", "pr-analysis", "automated"],
    default_args=default_args,
    doc_md=__doc__,
)
def omc_pr_analyzer() -> None:
    """Orchestrate PR analysis for a configurable GitHub repository."""

    # ------------------------------------------------------------------
    # Task 1: Fetch PR details from GitHub API
    # ------------------------------------------------------------------

    @task()
    def fetch_pr_details(**context) -> dict:
        """Fetch pull request details from the GitHub API."""
        pr_number = context["params"]["pr_number"]
        repo = context["params"].get("repo", TARGET_REPO)
        token = _resolve_github_token()
        headers = _github_headers(token)

        url = GITHUB_API_PULLS_URL.format(repo=repo) + f"/{pr_number}"

        try:
            response = requests.get(url, headers=headers, timeout=DEFAULT_REQUEST_TIMEOUT)
            response.raise_for_status()
        except requests.exceptions.HTTPError as exc:
            status = exc.response.status_code
            if 400 <= status < 500 and status != 429:
                raise AirflowFailException(
                    f"HTTP {status} fetching PR #{pr_number}: {exc}"
                ) from exc
            raise RuntimeError(f"HTTP {status} fetching PR #{pr_number}: {exc}") from exc
        except requests.exceptions.RequestException as exc:
            raise RuntimeError(f"Failed to fetch PR #{pr_number}: {exc}") from exc

        pr = response.json()
        label_names = [lbl["name"] for lbl in pr.get("labels", [])]

        pr_title = pr.get("title", "")
        pr_body = pr.get("body", "") or ""
        linked_issue = _extract_linked_issue(pr_title, pr_body)

        result = {
            "number": pr["number"],
            "title": pr_title,
            "body": pr_body,
            "html_url": pr.get("html_url", ""),
            "head_branch": pr.get("head", {}).get("ref", ""),
            "base_branch": pr.get("base", {}).get("ref", ""),
            "labels": label_names,
            "changed_files": pr.get("changed_files", 0),
            "additions": pr.get("additions", 0),
            "deletions": pr.get("deletions", 0),
            "linked_issue": linked_issue,
        }

        log.info(
            "Fetched PR #%d: %s (branch: %s → %s, files: %d, +%d/-%d, linked issue: %s)",
            result["number"],
            result["title"],
            result["head_branch"],
            result["base_branch"],
            result["changed_files"],
            result["additions"],
            result["deletions"],
            linked_issue or "none",
        )
        return result

    # ------------------------------------------------------------------
    # Task 2: Determine analysis scope from PR size
    # ------------------------------------------------------------------

    @task()
    def determine_analysis_scope(pr_data: dict) -> dict:
        """Determine analysis scope and max turns based on PR size.

        Small  (< 5 files, < 100 lines total): standard depth.
        Medium (5-15 files, 100-500 lines):    deep analysis.
        Large  (> 15 files, > 500 lines):      deep + extra turns.
        """
        changed_files = pr_data.get("changed_files", 0)
        total_lines = pr_data.get("additions", 0) + pr_data.get("deletions", 0)

        if changed_files > MEDIUM_PR_MAX_FILES or total_lines > MEDIUM_PR_MAX_LINES:
            scope = "deep"
            max_turns = MAX_TURNS_LARGE
        elif changed_files > SMALL_PR_MAX_FILES or total_lines > SMALL_PR_MAX_LINES:
            scope = "deep"
            max_turns = MAX_TURNS_DEEP
        else:
            scope = "standard"
            max_turns = MAX_TURNS_STANDARD

        log.info(
            "Analysis scope for PR #%d (%d files, %d lines): scope=%s, max_turns=%d",
            pr_data.get("number", 0),
            changed_files,
            total_lines,
            scope,
            max_turns,
        )
        return {"scope": scope, "max_turns": max_turns}

    # ------------------------------------------------------------------
    # Task 3: Publish analysis request to Redis Stream
    # ------------------------------------------------------------------

    @task()
    def request_worker_analysis(
        pr_details: dict, analysis_config: dict, **context
    ) -> str:
        """Publish PR analysis request to Redis for worker processing."""
        if not pr_details:
            return "No PR details, skipping."

        import redis as redis_lib

        redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
        r = redis_lib.from_url(redis_url)

        pr_number = pr_details.get("number", 0)
        repo = context["params"].get("repo", TARGET_REPO)
        repo_name = repo.split("/")[-1]

        request_data = {
            "type": "pr_analysis",
            "pr_number": str(pr_number),
            "pr_title": pr_details.get("title", ""),
            "pr_body": pr_details.get("body", "")[:5000],
            "pr_labels": json.dumps(pr_details.get("labels", [])),
            "pr_branch": pr_details.get("head_branch", ""),
            "pr_base": pr_details.get("base_branch", ""),
            "linked_issue": pr_details.get("linked_issue", ""),
            "repo": repo,
            "repo_path": f"/home/baekenough/workspace/{repo_name}",
            "analysis_scope": analysis_config.get("scope", "standard"),
            "requested_at": datetime.now(timezone.utc).isoformat(),
        }

        lock_key = f"customclaw:pr-analysis-lock:{pr_number}"
        if not r.set(lock_key, "1", nx=True, ex=900):  # 15 min TTL
            log.info("PR #%s analysis already in progress, skipping duplicate", pr_number)
            return f"PR #{pr_number} analysis already in progress, skipped"

        msg_id = r.xadd("customclaw:analysis-requests", request_data)
        log.info("Published PR analysis request for PR #%s: %s", pr_number, msg_id)
        return f"Analysis request published for PR #{pr_number}"

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    details = fetch_pr_details()
    config = determine_analysis_scope(details)
    request_worker_analysis(details, config)


# Instantiate the DAG
dag_instance = omc_pr_analyzer()
