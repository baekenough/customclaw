"""
omc_issue_analyzer DAG.

Analyzes new GitHub issues on configurable repositories by publishing
analysis requests to a Redis Stream. A separate worker process consumes
the stream and performs the actual Claude-powered analysis.

Triggered externally via GitHub Actions webhook → SSH → airflow dags trigger.

Graph:
    fetch_issue_details ─► determine_analysis_depth ─► request_worker_analysis
"""

from __future__ import annotations

import json
import logging
import os
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
GITHUB_API_ISSUES_URL = "https://api.github.com/repos/{repo}/issues"
DEFAULT_REQUEST_TIMEOUT = 30
TARGET_REPO = "baekenough/oh-my-customcode"

LABEL_DEPTH_MAP = {
    "epic": 15, "architecture": 15,
    "bug": 10, "enhancement": 10, "performance": 10,
    "documentation": 5, "chore": 5,
}
DEFAULT_MAX_TURNS = 10

# Repo → Slack channel mapping for analysis notifications.
# When empty or missing, the Go worker falls back to the DB-configured default.
REPO_SLACK_CHANNELS = {
    "baekenough/customclaw": "C0ANBQF9K36",
    "baekenough/oh-my-customcode": "C0AM684CLRH",
}


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


# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------
default_args = {
    "owner": "omc",
    "retries": 1,
    "retry_delay": timedelta(minutes=5),
}


@dag(
    dag_id="omc_issue_analyzer",
    description=(
        "Fetches GitHub issue details from a configurable repository and "
        "publishes an analysis request to a Redis Stream for worker processing."
    ),
    schedule=None,
    start_date=datetime(2026, 3, 18),
    catchup=False,
    max_active_runs=10,
    params={
        "issue_number": Param(
            default=0,
            type="integer",
            description="GitHub issue number to analyze",
        ),
        "repo": Param(
            default="baekenough/oh-my-customcode",
            type="string",
            description="GitHub repository (owner/repo)",
        ),
    },
    tags=["project:omc", "project:customclaw", "issue-analysis", "automated"],
    default_args=default_args,
    doc_md=__doc__,
)
def omc_issue_analyzer() -> None:
    """Orchestrate issue analysis for oh-my-customcode."""

    # ------------------------------------------------------------------
    # Task 1: Fetch issue details from GitHub API
    # ------------------------------------------------------------------

    @task()
    def fetch_issue_details(**context) -> dict:
        """Fetch issue details from the GitHub API."""
        issue_number = context["params"]["issue_number"]
        repo = context["params"].get("repo", TARGET_REPO)
        token = _resolve_github_token()
        headers = _github_headers(token)

        url = GITHUB_API_ISSUES_URL.format(repo=repo) + f"/{issue_number}"

        try:
            response = requests.get(url, headers=headers, timeout=DEFAULT_REQUEST_TIMEOUT)
            response.raise_for_status()
        except requests.exceptions.HTTPError as exc:
            status = exc.response.status_code
            if 400 <= status < 500 and status != 429:
                raise AirflowFailException(
                    f"HTTP {status} fetching issue #{issue_number}: {exc}"
                ) from exc
            raise RuntimeError(f"HTTP {status} fetching issue #{issue_number}: {exc}") from exc
        except requests.exceptions.RequestException as exc:
            raise RuntimeError(f"Failed to fetch issue #{issue_number}: {exc}") from exc

        issue = response.json()
        label_names = [l["name"] for l in issue.get("labels", [])]

        result = {
            "number": issue["number"],
            "title": issue.get("title", ""),
            "body": issue.get("body", "") or "",
            "labels": label_names,
            "html_url": issue.get("html_url", ""),
        }

        log.info("Fetched issue #%d: %s (labels: %s)", result["number"], result["title"], label_names)
        return result

    # ------------------------------------------------------------------
    # Task 2: Determine analysis depth from issue labels
    # ------------------------------------------------------------------

    @task()
    def determine_analysis_depth(issue_data: dict) -> dict:
        """Determine analysis depth based on issue labels."""
        labels = issue_data.get("labels", [])
        max_depth = DEFAULT_MAX_TURNS

        for label in labels:
            depth = LABEL_DEPTH_MAP.get(label, 0)
            if depth > max_depth:
                max_depth = depth

        log.info("Analysis depth for labels %s: max_turns=%d", labels, max_depth)
        return {"depth": "deep" if max_depth >= 15 else "standard", "max_turns": max_depth}

    # ------------------------------------------------------------------
    # Task 3: Publish analysis request to Redis Stream
    # ------------------------------------------------------------------

    @task()
    def request_worker_analysis(issue_details: dict, analysis_config: dict, **context) -> str:
        """Publish analysis request to Redis for worker processing."""
        if not issue_details:
            return "No issue details, skipping."

        import redis as redis_lib

        repo = context["params"].get("repo", TARGET_REPO)
        redis_url = os.environ.get("REDIS_URL", "redis://redis:6379")
        r = redis_lib.from_url(redis_url)

        issue_number = issue_details.get("number", 0)
        repo_name = repo.split("/")[-1]

        request_data = {
            "type": "issue_analysis",
            "issue_number": str(issue_number),
            "issue_title": issue_details.get("title", ""),
            "issue_body": issue_details.get("body", "")[:5000],
            "issue_labels": json.dumps(issue_details.get("labels", [])),
            "repo": repo,
            "repo_path": f"/home/baekenough/workspace/{repo_name}",
            "analysis_depth": analysis_config.get("depth", "standard"),
            "slack_channel": REPO_SLACK_CHANNELS.get(repo, ""),
            "requested_at": datetime.now(timezone.utc).isoformat(),
        }

        msg_id = r.xadd("customclaw:analysis-requests", request_data)
        log.info("Published analysis request for issue #%s: %s", issue_number, msg_id)
        return f"Analysis request published for issue #{issue_number}"

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    details = fetch_issue_details()
    config = determine_analysis_depth(details)
    request_worker_analysis(details, config)


# Instantiate the DAG
dag_instance = omc_issue_analyzer()
