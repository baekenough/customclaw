"""Slack Bolt Container Healthcheck DAG.

Monitors the ``customclaw-slack-bolt-1`` container for a BrokenPipeError
reconnection loop.  The container does not self-exit when this happens, so the
Docker restart policy never fires.  This DAG detects the loop and force-restarts
the container.

Detection heuristic: if ``BrokenPipeError`` appears 3 or more times in the last
20 log lines, the container is considered stuck and is restarted.

Graph:
    check_logs ─► restart_if_looping ─► send_alert
"""

from __future__ import annotations

import logging
import os
import subprocess
from datetime import datetime, timedelta

import requests
from airflow.sdk import Variable, dag, task

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

# Target container to monitor and restart.
SLACK_BOLT_CONTAINER = "customclaw-slack-bolt-1"

# Number of trailing log lines to inspect on each run.
LOG_TAIL_LINES = 20

# How many occurrences of BrokenPipeError in the tail before we restart.
BROKEN_PIPE_THRESHOLD = 3

# Airflow Variable / env-var names (shared with credential_keepalive).
SLACK_TOKEN_VAR = "customclaw_slack_bot_token"
SLACK_ALERT_CHANNEL_VAR = "credential_alert_channel"
DEFAULT_ALERT_CHANNEL = "C0AMBNY135Z"

# Timeout for docker commands (seconds).
DOCKER_LOGS_TIMEOUT = 15
DOCKER_RESTART_TIMEOUT = 30

# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------
default_args = {
    "owner": "customclaw",
    # No retries: each 5-minute tick is a fresh check; retrying a health-check
    # after failure would just re-check the same already-restarted container.
    "retries": 0,
}


@dag(
    dag_id="slack_bolt_healthcheck",
    description=(
        "Every 5 minutes, inspects the last 20 log lines of customclaw-slack-bolt-1. "
        "If BrokenPipeError appears 3+ times the container is restarted and a Slack "
        "alert is posted to the credential alert channel."
    ),
    schedule="*/5 * * * *",
    start_date=datetime(2026, 3, 25),
    catchup=False,
    tags=["monitoring", "platform"],
    default_args=default_args,
    doc_md=__doc__,
)
def slack_bolt_healthcheck() -> None:
    """Orchestrate the healthcheck and optional restart of the slack-bolt container."""

    # ------------------------------------------------------------------
    # Task 1: Fetch the last N log lines from the container
    # ------------------------------------------------------------------

    @task()
    def check_logs() -> dict:
        """Retrieve the last log lines from the slack-bolt container.

        Runs ``docker logs --tail {LOG_TAIL_LINES}`` against the target
        container.  If docker is unavailable or the command fails for any
        reason the task logs a warning and returns an empty result rather
        than failing the DAG run.

        Returns:
            Dict with keys:

            - ``lines`` (list[str]): Individual log lines (stdout + stderr merged).
            - ``broken_pipe_count`` (int): Occurrences of ``BrokenPipeError`` in
              those lines.
            - ``docker_available`` (bool): False when the docker binary was not
              found.
        """
        cmd = [
            "docker",
            "logs",
            "--tail",
            str(LOG_TAIL_LINES),
            SLACK_BOLT_CONTAINER,
        ]

        try:
            # docker logs writes to stderr by default; capture both streams.
            result = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                timeout=DOCKER_LOGS_TIMEOUT,
            )
        except subprocess.TimeoutExpired:
            log.warning(
                "docker logs timed out after %ds for %s.",
                DOCKER_LOGS_TIMEOUT,
                SLACK_BOLT_CONTAINER,
            )
            return {"lines": [], "broken_pipe_count": 0, "docker_available": True}
        except FileNotFoundError:
            log.warning("docker binary not found — cannot fetch container logs.")
            return {"lines": [], "broken_pipe_count": 0, "docker_available": False}

        if result.returncode != 0:
            log.warning(
                "docker logs exited %d for %s. stderr: %s",
                result.returncode,
                SLACK_BOLT_CONTAINER,
                result.stderr[:300],
            )
            return {"lines": [], "broken_pipe_count": 0, "docker_available": True}

        # docker logs emits to stderr; stdout may be empty depending on the
        # container's logging driver.  Merge both so we don't miss anything.
        combined = (result.stdout + result.stderr).strip()
        lines = combined.splitlines() if combined else []

        broken_pipe_count = sum(
            1 for line in lines if "BrokenPipeError" in line
        )

        log.info(
            "Fetched %d log lines from %s. BrokenPipeError occurrences: %d/%d threshold.",
            len(lines),
            SLACK_BOLT_CONTAINER,
            broken_pipe_count,
            BROKEN_PIPE_THRESHOLD,
        )
        if lines:
            log.debug("Last log line: %s", lines[-1])

        return {
            "lines": lines,
            "broken_pipe_count": broken_pipe_count,
            "docker_available": True,
        }

    # ------------------------------------------------------------------
    # Task 2: Restart the container if the loop is detected
    # ------------------------------------------------------------------

    @task()
    def restart_if_looping(log_result: dict) -> dict:
        """Restart the slack-bolt container if a BrokenPipeError loop is detected.

        A restart is triggered only when ``broken_pipe_count`` meets or exceeds
        ``BROKEN_PIPE_THRESHOLD``.  On any docker command failure the error is
        logged but the task does not raise so the alert task still runs.

        Args:
            log_result: Dict produced by ``check_logs``.

        Returns:
            Dict with keys:

            - ``restarted`` (bool): True if a restart was attempted.
            - ``restart_success`` (bool): True if docker restart returned exit 0.
            - ``broken_pipe_count`` (int): Forwarded from ``log_result``.
            - ``reason`` (str): Human-readable reason for the action taken.
        """
        broken_pipe_count: int = log_result.get("broken_pipe_count", 0)
        docker_available: bool = log_result.get("docker_available", True)

        if not docker_available:
            log.warning("Skipping restart check: docker is unavailable.")
            return {
                "restarted": False,
                "restart_success": False,
                "broken_pipe_count": broken_pipe_count,
                "reason": "docker_unavailable",
            }

        if broken_pipe_count < BROKEN_PIPE_THRESHOLD:
            log.info(
                "Container %s appears healthy (BrokenPipeError: %d/%d).",
                SLACK_BOLT_CONTAINER,
                broken_pipe_count,
                BROKEN_PIPE_THRESHOLD,
            )
            return {
                "restarted": False,
                "restart_success": False,
                "broken_pipe_count": broken_pipe_count,
                "reason": "healthy",
            }

        log.warning(
            "BrokenPipeError loop detected in %s (%d occurrences in last %d lines). "
            "Triggering container restart.",
            SLACK_BOLT_CONTAINER,
            broken_pipe_count,
            LOG_TAIL_LINES,
        )

        try:
            result = subprocess.run(
                ["docker", "restart", SLACK_BOLT_CONTAINER],
                capture_output=True,
                text=True,
                timeout=DOCKER_RESTART_TIMEOUT,
            )
        except subprocess.TimeoutExpired:
            log.error(
                "docker restart timed out after %ds for %s.",
                DOCKER_RESTART_TIMEOUT,
                SLACK_BOLT_CONTAINER,
            )
            return {
                "restarted": True,
                "restart_success": False,
                "broken_pipe_count": broken_pipe_count,
                "reason": "restart_timeout",
            }
        except FileNotFoundError:
            log.error("docker binary not found — cannot restart %s.", SLACK_BOLT_CONTAINER)
            return {
                "restarted": True,
                "restart_success": False,
                "broken_pipe_count": broken_pipe_count,
                "reason": "docker_unavailable",
            }

        if result.returncode != 0:
            log.error(
                "docker restart %s failed (exit %d): %s",
                SLACK_BOLT_CONTAINER,
                result.returncode,
                result.stderr[:300],
            )
            return {
                "restarted": True,
                "restart_success": False,
                "broken_pipe_count": broken_pipe_count,
                "reason": f"restart_failed_exit_{result.returncode}",
            }

        log.info(
            "Successfully restarted %s after detecting BrokenPipeError loop.",
            SLACK_BOLT_CONTAINER,
        )
        return {
            "restarted": True,
            "restart_success": True,
            "broken_pipe_count": broken_pipe_count,
            "reason": "loop_detected_and_restarted",
        }

    # ------------------------------------------------------------------
    # Task 3: Send a Slack alert when a restart was performed
    # ------------------------------------------------------------------

    @task()
    def send_alert(restart_result: dict) -> None:
        """Post a Slack alert when the container was restarted.

        No message is sent during healthy (no-restart) runs to avoid noise.
        If a restart occurred (successfully or not), an alert is posted to
        the credential alert channel.  Alert failure is logged but does not
        fail the DAG.

        Args:
            restart_result: Dict from ``restart_if_looping``.
        """
        restarted: bool = restart_result.get("restarted", False)
        if not restarted:
            log.info("No restart was triggered — skipping alert.")
            return

        restart_success: bool = restart_result.get("restart_success", False)
        broken_pipe_count: int = restart_result.get("broken_pipe_count", 0)
        reason: str = restart_result.get("reason", "unknown")

        if restart_success:
            status_emoji = ":white_check_mark:"
            status_text = "Container restarted successfully."
        else:
            status_emoji = ":x:"
            status_text = f"Restart *failed* (reason: `{reason}`). Manual intervention required."

        text = (
            f":warning: *Slack Bolt Container Healthcheck*\n"
            f"*Container:* `{SLACK_BOLT_CONTAINER}`\n"
            f"*Trigger:* `BrokenPipeError` appeared *{broken_pipe_count}x* "
            f"in the last {LOG_TAIL_LINES} log lines (threshold: {BROKEN_PIPE_THRESHOLD}).\n"
            f"{status_emoji} {status_text}"
        )

        slack_token = _resolve_slack_token()
        if not slack_token:
            log.warning(
                "No Slack token available — cannot send restart alert. "
                "Container restart status: %s",
                "success" if restart_success else "failed",
            )
            return

        try:
            channel = Variable.get(SLACK_ALERT_CHANNEL_VAR, default=DEFAULT_ALERT_CHANNEL)
        except Exception:
            channel = DEFAULT_ALERT_CHANNEL

        _send_slack_message(token=slack_token, channel=channel, text=text)

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    log_result = check_logs()
    restart_result = restart_if_looping(log_result)
    send_alert(restart_result)


# ---------------------------------------------------------------------------
# Module-level pure helper functions
# ---------------------------------------------------------------------------


def _resolve_slack_token() -> str | None:
    """Resolve the Slack bot token from Airflow Variable or environment.

    Lookup order:
    1. Airflow Variable ``customclaw_slack_bot_token``
    2. Environment variable ``CUSTOMCLAW_SLACK_BOT_TOKEN``

    Returns:
        Token string if found, ``None`` otherwise.
    """
    # 1. Airflow Variable (preferred)
    try:
        token = Variable.get(SLACK_TOKEN_VAR, default=None)
        if token:
            return token
    except Exception:
        pass

    # 2. Direct environment variable
    return os.environ.get("CUSTOMCLAW_SLACK_BOT_TOKEN") or None


def _send_slack_message(token: str, channel: str, text: str) -> None:
    """Post a message to a Slack channel via the chat.postMessage API.

    Errors are logged but not re-raised so they never fail the DAG.

    Args:
        token: Slack bot OAuth token.
        channel: Slack channel ID.
        text: Message body (supports mrkdwn).
    """
    try:
        resp = requests.post(
            "https://slack.com/api/chat.postMessage",
            headers={
                "Authorization": f"Bearer {token}",
                "Content-Type": "application/json",
            },
            json={"channel": channel, "text": text},
            timeout=15,
        )
        resp.raise_for_status()
        data = resp.json()
        if not data.get("ok"):
            log.error(
                "Slack API returned ok=false: %s", data.get("error", "unknown")
            )
        else:
            log.info("Slack alert sent to channel %s.", channel)
    except requests.exceptions.RequestException as exc:
        log.error("Failed to send Slack alert: %s", exc)


# Instantiate the DAG
dag_instance = slack_bolt_healthcheck()
