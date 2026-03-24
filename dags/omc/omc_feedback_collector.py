"""
omc_feedback_collector DAG.

Receives user feedback submitted via `/omcustom:feedback` and creates a
GitHub issue on baekenough/oh-my-customcode. Supports anonymous submissions —
no user identification required.

Triggered via: Airflow REST API or CLI
  airflow dags trigger omc_feedback_collector --conf '{"title": "...", "body": "...", "feedback_type": "bug"}'

Graph:
    validate_feedback ─► create_github_issue
"""

from __future__ import annotations

import logging
import os
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
DEFAULT_REQUEST_TIMEOUT = 30
TARGET_REPO = "baekenough/oh-my-customcode"
MAX_BODY_LENGTH = 10_000

FEEDBACK_TYPE_PREFIX = {
    "bug": "Bug Report",
    "feature": "Feature Request",
    "improvement": "Improvement",
    "question": "Question",
    "general": "Feedback",
}

FEEDBACK_TYPE_LABELS = {
    "bug": ["feedback", "bug"],
    "feature": ["feedback", "enhancement"],
    "improvement": ["feedback", "enhancement"],
    "question": ["feedback", "question"],
    "general": ["feedback"],
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
    dag_id="omc_feedback_collector",
    description=(
        "Collects user feedback submitted via /omcustom:feedback and creates "
        "a GitHub issue on baekenough/oh-my-customcode."
    ),
    schedule=None,
    start_date=datetime(2026, 3, 20),
    catchup=False,
    max_active_runs=20,
    params={
        "title": Param(
            default="",
            type="string",
            description="Feedback title/summary",
        ),
        "body": Param(
            default="",
            type="string",
            description="Detailed feedback content",
        ),
        "feedback_type": Param(
            default="general",
            type="string",
            enum=["bug", "feature", "improvement", "question", "general"],
            description="Type of feedback",
        ),
        "anonymous": Param(
            default=True,
            type="boolean",
            description="Whether to submit anonymously",
        ),
        "submitter": Param(
            default="",
            type="string",
            description="Optional submitter identifier (ignored if anonymous)",
        ),
        "project_context": Param(
            default="",
            type="string",
            description="Optional project context (e.g., omcustom version, project name)",
        ),
    },
    tags=["project:omc", "feedback", "community"],
    default_args=default_args,
    doc_md=__doc__,
)
def omc_feedback_collector() -> None:
    """Orchestrate feedback collection for oh-my-customcode."""

    # ------------------------------------------------------------------
    # Task 1: Validate and sanitize feedback params
    # ------------------------------------------------------------------

    @task()
    def validate_feedback(**context) -> dict:
        """Validate and sanitize feedback inputs.

        Raises:
            AirflowFailException: If title is empty.

        Returns:
            Dict of validated and sanitized feedback fields.
        """
        params = context["params"]

        title = (params.get("title") or "").strip()
        if not title:
            raise AirflowFailException(
                "Feedback title must not be empty. "
                "Provide a non-empty 'title' param when triggering this DAG."
            )

        body = (params.get("body") or "").strip()
        if len(body) > MAX_BODY_LENGTH:
            log.warning(
                "Feedback body truncated from %d to %d characters.",
                len(body),
                MAX_BODY_LENGTH,
            )
            body = body[:MAX_BODY_LENGTH] + "\n\n_(truncated — body exceeded 10 000 characters)_"

        feedback_type = params.get("feedback_type", "general")
        anonymous = params.get("anonymous", True)
        submitter = (params.get("submitter") or "").strip()
        project_context = (params.get("project_context") or "").strip()

        result = {
            "title": title,
            "body": body,
            "feedback_type": feedback_type,
            "anonymous": anonymous,
            "submitter": submitter if not anonymous else "",
            "project_context": project_context,
        }

        log.info(
            "Validated feedback: type=%s, anonymous=%s, title=%r",
            feedback_type,
            anonymous,
            title,
        )
        return result

    # ------------------------------------------------------------------
    # Task 2: Create GitHub issue with feedback content
    # ------------------------------------------------------------------

    @task()
    def create_github_issue(feedback: dict) -> dict:
        """Create a GitHub issue for the submitted feedback.

        Args:
            feedback: Validated feedback dict from validate_feedback.

        Returns:
            Dict with ``issue_number`` and ``issue_url`` of the created issue.

        Raises:
            AirflowFailException: On 4xx HTTP errors (except 429).
            RuntimeError: On 5xx or network errors (triggers Airflow retry).
        """
        token = _resolve_github_token()
        headers = _github_headers(token)

        feedback_type = feedback.get("feedback_type", "general")
        title_prefix = FEEDBACK_TYPE_PREFIX.get(feedback_type, "Feedback")
        issue_title = f"[{title_prefix}] {feedback['title']}"

        submitter_display = feedback.get("submitter") or "Anonymous"
        project_context_display = feedback.get("project_context") or "N/A"
        submitted_at = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")

        issue_body = (
            f"## Feedback\n\n"
            f"{feedback['body']}\n\n"
            f"## Context\n\n"
            f"- **Type**: {feedback_type}\n"
            f"- **Submitted**: {submitted_at}\n"
            f"- **Submitter**: {submitter_display}\n"
            f"- **Project Context**: {project_context_display}\n\n"
            f"---\n"
            f"_Submitted via `/omcustom:feedback` — omc_feedback_collector_"
        )

        labels = FEEDBACK_TYPE_LABELS.get(feedback_type, ["feedback"])

        url = GITHUB_API_ISSUES_URL.format(repo=TARGET_REPO)
        payload = {
            "title": issue_title,
            "body": issue_body,
            "labels": labels,
        }

        try:
            response = requests.post(
                url,
                headers=headers,
                json=payload,
                timeout=DEFAULT_REQUEST_TIMEOUT,
            )
            response.raise_for_status()
        except requests.exceptions.HTTPError as exc:
            status = exc.response.status_code
            if 400 <= status < 500 and status != 429:
                raise AirflowFailException(
                    f"HTTP {status} creating GitHub issue: {exc}"
                ) from exc
            raise RuntimeError(f"HTTP {status} creating GitHub issue: {exc}") from exc
        except requests.exceptions.RequestException as exc:
            raise RuntimeError(f"Failed to create GitHub issue: {exc}") from exc

        issue = response.json()
        issue_number = issue["number"]
        issue_url = issue["html_url"]

        log.info(
            "Created GitHub issue #%d for feedback type=%s: %s",
            issue_number,
            feedback_type,
            issue_url,
        )
        return {"issue_number": issue_number, "issue_url": issue_url}

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    feedback = validate_feedback()
    create_github_issue(feedback)


# Instantiate the DAG
dag_instance = omc_feedback_collector()
