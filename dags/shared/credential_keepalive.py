"""Credential Keepalive DAG.

Periodically refreshes LLM CLI OAuth tokens to prevent expiry-driven outages.
Claude OAuth tokens expire after ~8 hours; this DAG runs every hour to keep
them fresh and restarts the go-worker if it was previously stuck in an error
state due to a stale token.

Codex CLI uses ChatGPT OAuth which cannot be auto-refreshed.  When a 401 /
auth failure is detected the DAG initiates a device-auth flow inside the
go-worker container, captures the device code + URL, and sends them to Slack
so the operator can authenticate from any device within 5 minutes.

Graph:
    check_status ─► refresh_claude ─► verify_and_alert
    check_codex  ─► recover_codex
"""

from __future__ import annotations

import json
import logging
import os
import subprocess
import time
from datetime import datetime, timedelta

import requests
from airflow.exceptions import AirflowFailException
from airflow.sdk import Variable, dag, task

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

# Container that runs LLM CLI subprocesses; restarted when token is recovered.
GO_WORKER_CONTAINER = "customclaw-go-worker-1"

# Postgres container used to query credential_status (go-worker has no psql).
POSTGRES_CONTAINER = "customclaw-postgres-1"

# Docker image used for the ephemeral token-refresh container.
# Must have the claude CLI installed at /usr/local/bin/claude (bound from host).
WORKER_IMAGE = "ghcr.io/baekenough/customclaw-slack-bolt:latest"

# Host paths for credential mounts.  The container receives them at the SAME
# absolute paths so that OAuth writes-back land in the right place on the host.
HOST_CLAUDE_DIR = "/home/baekenough/.claude"
HOST_CLAUDE_JSON = "/home/baekenough/.claude.json"
HOST_CLAUDE_BIN = "/home/baekenough/.local/bin/claude"

# Airflow Variable / env-var names.
SLACK_TOKEN_VAR = "customclaw_slack_bot_token"
SLACK_ALERT_CHANNEL_VAR = "credential_alert_channel"
DEFAULT_ALERT_CHANNEL = "C0AMBNY135Z"

# docker run timeout (seconds) — haiku with max-turns 1 should finish quickly.
DOCKER_RUN_TIMEOUT = 120
DOCKER_RESTART_TIMEOUT = 30

# Timeout for the user to complete Codex device-auth (seconds).
CODEX_DEVICE_AUTH_TIMEOUT = 300  # 5 minutes

# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------
default_args = {
    "owner": "customclaw",
    "retries": 2,
    "retry_delay": timedelta(minutes=5),
}


@dag(
    dag_id="credential_keepalive",
    description=(
        "Proactively refreshes LLM CLI OAuth tokens every hour and "
        "restarts the go-worker if it was stuck in a credential-error state."
    ),
    schedule="0 * * * *",
    start_date=datetime(2026, 3, 25),
    catchup=False,
    tags=["project:customclaw", "credential-management", "automated"],
    default_args=default_args,
    doc_md=__doc__,
)
def credential_keepalive() -> None:
    """Orchestrate periodic credential refresh for LLM CLI tools."""

    # ------------------------------------------------------------------
    # Task 1: Check current credential status from go-worker
    # ------------------------------------------------------------------

    @task()
    def check_status() -> dict:
        """Query credential status from the postgres container.

        Runs ``psql`` inside the postgres container to read the
        ``credential_status`` table.  Falls back to an empty dict if the query
        fails so that the pipeline still attempts a proactive refresh.

        Returns:
            Dict mapping provider name to status string, e.g.
            ``{"claude": "error", "openai": "ok", "gemini": "unconfigured"}``.
        """
        query = (
            "SELECT provider, status FROM credential_status"
            " ORDER BY checked_at DESC;"
        )
        cmd = [
            "docker",
            "exec",
            POSTGRES_CONTAINER,
            "psql",
            "-U", "customclaw",
            "-d", "customclaw",
            "-t", "-A", "-F", "|",
            "-c", query,
        ]

        try:
            result = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                timeout=30,
            )
        except subprocess.TimeoutExpired:
            log.warning("Timed out querying credential_status from go-worker.")
            return {}
        except FileNotFoundError:
            log.warning("docker binary not found — cannot check credential status.")
            return {}

        if result.returncode != 0:
            log.warning(
                "psql query failed (exit %d): %s",
                result.returncode,
                result.stderr[:300],
            )
            return {}

        statuses: dict[str, str] = {}
        for line in result.stdout.strip().splitlines():
            line = line.strip()
            if "|" not in line:
                continue
            provider, status = line.split("|", 1)
            statuses[provider.strip()] = status.strip()

        log.info("Credential status from postgres: %s", statuses)
        return statuses

    # ------------------------------------------------------------------
    # Task 2: Refresh the Claude OAuth token
    # ------------------------------------------------------------------

    @task()
    def refresh_claude(statuses: dict) -> dict:
        """Proactively refresh the Claude OAuth token via a temporary container.

        The token refresh is performed regardless of the current status to keep
        the token fresh and ahead of the ~8-hour expiry window.  A throwaway
        container is created with the host credential directories bind-mounted
        at the same absolute paths, so any token write-back lands correctly on
        the host filesystem.

        Args:
            statuses: Credential status dict from ``check_status``.

        Returns:
            Dict with keys:
            - ``success`` (bool): Whether the refresh invocation succeeded.
            - ``was_error`` (bool): Whether Claude was in error state before.
            - ``output`` (str): Truncated stdout from the CLI invocation.
        """
        was_error = statuses.get("claude", "") == "error"
        log.info(
            "Refreshing Claude token. Previous status: %s",
            statuses.get("claude", "unknown"),
        )

        docker_cmd = [
            "docker",
            "run",
            "--rm",
            # Bind host credential directory at the same absolute path so
            # OAuth write-back reaches the correct location on the host.
            "-v",
            f"{HOST_CLAUDE_DIR}:{HOST_CLAUDE_DIR}",
            "-v",
            f"{HOST_CLAUDE_JSON}:{HOST_CLAUDE_JSON}",
            # Claude CLI binary — read-only, mounted from host.
            "-v",
            f"{HOST_CLAUDE_BIN}:/usr/local/bin/claude:ro",
            "-e",
            "HOME=/home/baekenough",
            "-e",
            "NO_COLOR=1",
            "--network",
            "host",
            WORKER_IMAGE,
            "claude",
            "-p",
            ".",
            "--model",
            "haiku",
            "--max-turns",
            "1",
            "--output-format",
            "json",
        ]

        try:
            result = subprocess.run(
                docker_cmd,
                capture_output=True,
                text=True,
                timeout=DOCKER_RUN_TIMEOUT,
                stdin=subprocess.DEVNULL,
            )
        except subprocess.TimeoutExpired:
            log.error(
                "docker run timed out after %ds during Claude token refresh.",
                DOCKER_RUN_TIMEOUT,
            )
            return {"success": False, "was_error": was_error, "output": "timeout"}
        except FileNotFoundError:
            log.error("docker binary not found — cannot refresh Claude token.")
            return {"success": False, "was_error": was_error, "output": "no-docker"}

        stdout = result.stdout.strip()
        stderr = result.stderr.strip()

        if stderr:
            log.info("docker run stderr: %s", stderr[:500])

        if result.returncode != 0:
            log.error(
                "Claude token refresh failed (exit %d). stderr: %s",
                result.returncode,
                stderr[:500],
            )
            return {
                "success": False,
                "was_error": was_error,
                "output": stderr[:300],
            }

        # The CLI returns JSON with an `is_error` field when --output-format json.
        is_error = _parse_cli_is_error(stdout)
        if is_error:
            log.error("Claude CLI returned is_error=true: %s", stdout[:300])
            return {
                "success": False,
                "was_error": was_error,
                "output": stdout[:300],
            }

        log.info("Claude token refresh succeeded.")

        # If the go-worker was stuck in error state, restart it now that the
        # token is fresh so it picks up the new credentials.
        if was_error:
            log.info(
                "Claude was previously in error state — restarting %s.",
                GO_WORKER_CONTAINER,
            )
            _restart_worker()

        return {"success": True, "was_error": was_error, "output": stdout[:200]}

    # ------------------------------------------------------------------
    # Task 3: Verify outcome and send Slack alert on failure
    # ------------------------------------------------------------------

    @task()
    def verify_and_alert(refresh_result: dict) -> None:
        """Send a Slack alert if the token refresh failed.

        No alert is sent on success.  On failure the alert includes the
        truncated error output to aid manual debugging.

        Args:
            refresh_result: Dict from ``refresh_claude`` with keys
                ``success``, ``was_error``, ``output``.
        """
        success = refresh_result.get("success", False)
        output = refresh_result.get("output", "")

        if success:
            log.info("Credential keepalive completed successfully.")
            return

        log.error(
            "Claude token refresh failed after all retries. Output: %s", output
        )

        slack_token = _resolve_slack_token()
        if not slack_token:
            log.warning(
                "No Slack token available — skipping alert notification."
            )
            # Raise to surface the failure in Airflow UI even without Slack.
            raise AirflowFailException(
                f"Claude token refresh failed and no Slack token configured. "
                f"Last output: {output[:200]}"
            )

        try:
            channel = Variable.get(SLACK_ALERT_CHANNEL_VAR, default=DEFAULT_ALERT_CHANNEL)
        except Exception:
            channel = DEFAULT_ALERT_CHANNEL
        _send_slack_alert(
            token=slack_token,
            channel=channel,
            output=output,
        )

        # Still fail the task so Airflow marks it red and sends email if configured.
        raise AirflowFailException(
            f"Claude token refresh failed. Alert sent to {channel}. "
            f"Output: {output[:200]}"
        )

    # ------------------------------------------------------------------
    # Task 4: Check Codex CLI authentication
    # ------------------------------------------------------------------

    @task()
    def check_codex() -> dict:
        """Test Codex CLI authentication by making a minimal API call.

        Runs a trivial ``codex exec`` inside the go-worker container.  A 401 /
        Unauthorized response or non-zero exit code indicates that the ChatGPT
        OAuth token has expired.

        Returns:
            Dict with keys:
            - ``healthy`` (bool): Whether the auth check passed.
            - ``error`` (str): Short error description when unhealthy.
        """
        cmd = [
            "docker", "exec",
            "-e", "HOME=/home/appuser",
            "-e", "NO_COLOR=1",
            GO_WORKER_CONTAINER,
            "codex", "exec", "hi",
            "-m", "gpt-5.4",
            "--dangerously-bypass-approvals-and-sandbox",
        ]
        try:
            result = subprocess.run(
                cmd, capture_output=True, text=True, timeout=60,
                stdin=subprocess.DEVNULL,
            )
        except subprocess.TimeoutExpired:
            log.warning("Codex auth check timed out.")
            return {"healthy": False, "error": "timeout"}
        except FileNotFoundError:
            return {"healthy": False, "error": "docker not found"}

        combined = result.stdout + result.stderr
        if result.returncode != 0 or "401" in combined or "Unauthorized" in combined:
            error_msg = result.stderr.strip()[:300] or result.stdout.strip()[:300]
            log.error("Codex auth check failed: %s", error_msg)
            return {"healthy": False, "error": error_msg}

        log.info("Codex auth check passed.")
        log.info("Codex token warmup successful — access token refreshed.")
        return {"healthy": True}

    # ------------------------------------------------------------------
    # Task 5: Recover Codex auth via device-auth flow
    # ------------------------------------------------------------------

    @task()
    def recover_codex(codex_result: dict) -> None:
        """If Codex auth failed, initiate device-auth flow and send code to Slack.

        Starts ``codex login --device-auth`` inside the go-worker container,
        reads the device code + URL from its output, and posts them to Slack so
        the operator can authenticate within the 5-minute window.  Waits for the
        process to complete before returning.

        Args:
            codex_result: Dict from ``check_codex`` with keys ``healthy`` and
                ``error``.
        """
        if codex_result.get("healthy", True):
            log.info("Codex auth is healthy — no recovery needed.")
            return

        log.info("Codex auth failed — initiating device-auth recovery flow.")

        slack_token = _resolve_slack_token()
        try:
            channel = Variable.get(SLACK_ALERT_CHANNEL_VAR, default=DEFAULT_ALERT_CHANNEL)
        except Exception:
            channel = DEFAULT_ALERT_CHANNEL

        # Start codex login --device-auth in the go-worker container.
        cmd = [
            "docker", "exec",
            "-e", "HOME=/home/appuser",
            GO_WORKER_CONTAINER,
            "codex", "login", "--device-auth",
        ]

        try:
            proc = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                stdin=subprocess.DEVNULL,
            )
        except FileNotFoundError:
            log.error("docker binary not found — cannot initiate device-auth.")
            raise AirflowFailException("docker not found for Codex device-auth")

        # Read output lines to find device code and URL.
        # Typical output: "Go to https://login.live.com/... and enter code XXXX-XXXX"
        device_info_lines: list[str] = []
        start_time = time.time()
        full_output: list[str] = []

        while time.time() - start_time < CODEX_DEVICE_AUTH_TIMEOUT:
            if proc.poll() is not None:
                # Process finished — drain remaining output.
                remaining = proc.stdout.read()
                if remaining:
                    full_output.append(remaining)
                break

            try:
                line = proc.stdout.readline()
                if line:
                    full_output.append(line)
                    line_stripped = line.strip()
                    log.info("codex login output: %s", line_stripped)
                    # Capture lines that carry the URL or device code.
                    if any(
                        kw in line_stripped.lower()
                        for kw in ["http", "code", "enter", "visit", "go to", "url"]
                    ):
                        device_info_lines.append(line_stripped)
            except Exception:
                time.sleep(1)
                continue

        output_text = "".join(full_output).strip()

        if proc.poll() is None:
            # Still running after timeout — kill it.
            proc.kill()
            proc.wait()
            log.error(
                "Codex device-auth timed out after %ds.", CODEX_DEVICE_AUTH_TIMEOUT
            )

            # Still send whatever device info we captured so the operator can
            # complete auth manually.
            if device_info_lines and slack_token:
                device_msg = "\n".join(device_info_lines)
                text = (
                    ":key: *Codex Device Auth Required*\n"
                    "돌쇠(Discord) 봇의 OpenAI 인증이 만료되었습니다.\n"
                    "아래 URL에서 코드를 입력해주세요:\n"
                    f"```{device_msg}```\n"
                    f"_(인증 대기 {CODEX_DEVICE_AUTH_TIMEOUT}초 초과 — 수동으로 완료 필요)_\n"
                    "```ssh ubuntu24_home_server-ext\n"
                    "docker exec -it -e HOME=/home/appuser customclaw-go-worker-1 "
                    "codex login --device-auth```"
                )
                _send_slack_alert(token=slack_token, channel=channel, output=text)
            raise AirflowFailException(
                f"Codex device-auth timed out. "
                f"Device info: {' | '.join(device_info_lines)}"
            )

        # Process completed within the timeout window.
        if proc.returncode == 0:
            log.info("Codex device-auth completed successfully!")
            if slack_token:
                _send_slack_alert(
                    token=slack_token,
                    channel=channel,
                    output=(
                        ":white_check_mark: *Codex 인증 복구 완료*\n"
                        "돌쇠 봇이 정상 동작합니다."
                    ),
                )
        else:
            log.error(
                "Codex device-auth failed (exit %d): %s",
                proc.returncode,
                output_text[:300],
            )
            if slack_token and device_info_lines:
                device_msg = "\n".join(device_info_lines)
                text = (
                    ":rotating_light: *Codex OAuth 인증 만료*\n"
                    "돌쇠(Discord) 봇이 응답하지 않습니다.\n"
                    "아래 URL에서 인증해주세요:\n"
                    f"```{device_msg}```"
                )
                _send_slack_alert(token=slack_token, channel=channel, output=text)
            elif slack_token:
                _send_slack_alert(
                    token=slack_token,
                    channel=channel,
                    output=(
                        ":rotating_light: *Codex OAuth 인증 만료*\n"
                        "돌쇠 봇이 응답하지 않습니다. 수동 로그인 필요:\n"
                        "```ssh ubuntu24_home_server-ext\n"
                        "docker exec -it -e HOME=/home/appuser "
                        "customclaw-go-worker-1 codex login --device-auth```"
                    ),
                )
            raise AirflowFailException(
                f"Codex device-auth failed: {output_text[:200]}"
            )

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    statuses = check_status()
    refresh_result = refresh_claude(statuses)
    verify_and_alert(refresh_result)

    codex_result = check_codex()
    recover_codex(codex_result)


# ---------------------------------------------------------------------------
# Module-level pure helper functions
# ---------------------------------------------------------------------------


def _parse_cli_is_error(stdout: str) -> bool:
    """Return True if the Claude CLI JSON output contains ``is_error: true``.

    The CLI may return either a single JSON object or a JSONL stream.  We
    check only the first valid JSON object to avoid false positives from
    intermediate progress lines.

    Args:
        stdout: Raw stdout string from the claude CLI invocation.

    Returns:
        True if ``is_error`` is truthy in the parsed output.
    """
    if not stdout:
        return False
    for line in stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            data = json.loads(line)
            if isinstance(data, dict):
                return bool(data.get("is_error", False))
        except json.JSONDecodeError:
            continue
    return False


def _restart_worker() -> None:
    """Restart the go-worker container.

    Logs a warning on failure but does not raise — a failed restart should not
    prevent the verify_and_alert task from running.
    """
    try:
        result = subprocess.run(
            ["docker", "restart", GO_WORKER_CONTAINER],
            capture_output=True,
            text=True,
            timeout=DOCKER_RESTART_TIMEOUT,
        )
        if result.returncode != 0:
            log.warning(
                "docker restart %s failed (exit %d): %s",
                GO_WORKER_CONTAINER,
                result.returncode,
                result.stderr[:300],
            )
        else:
            log.info("Successfully restarted %s.", GO_WORKER_CONTAINER)
    except subprocess.TimeoutExpired:
        log.warning(
            "docker restart timed out after %ds.", DOCKER_RESTART_TIMEOUT
        )
    except FileNotFoundError:
        log.warning("docker binary not found — cannot restart %s.", GO_WORKER_CONTAINER)


def _resolve_slack_token() -> str | None:
    """Resolve the Slack bot token from Airflow Variable or environment.

    Lookup order:
    1. Airflow Variable ``customclaw_slack_bot_token``
    2. Environment variable ``CUSTOMCLAW_SLACK_BOT_TOKEN``
    3. Fallback: run ``docker exec`` on the go-worker to retrieve from its env

    Returns:
        Token string if found, ``None`` otherwise.
    """
    # 1. Airflow Variable (preferred — set once via Airflow UI or CLI)
    try:
        token = Variable.get(SLACK_TOKEN_VAR, default=None)
        if token:
            return token
    except Exception:
        pass

    # 2. Direct environment variable
    token = os.environ.get("CUSTOMCLAW_SLACK_BOT_TOKEN")
    if token:
        return token

    # 3. Ask the go-worker for its environment (it has the token from .env)
    try:
        result = subprocess.run(
            [
                "docker",
                "exec",
                GO_WORKER_CONTAINER,
                "sh",
                "-c",
                "echo $CUSTOMCLAW_SLACK_BOT_TOKEN",
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0:
            token = result.stdout.strip()
            if token:
                log.info("Resolved Slack token from go-worker environment.")
                return token
    except (subprocess.TimeoutExpired, FileNotFoundError):
        pass

    return None


def _send_slack_alert(token: str, channel: str, output: str) -> None:
    """Post a Slack alert message about a credential refresh failure.

    Args:
        token: Slack bot OAuth token.
        channel: Slack channel ID to post to.
        output: Truncated CLI output or error description.
    """
    text = (
        ":rotating_light: *Credential Keepalive Failed*\n"
        f"The `credential_keepalive` DAG could not refresh the Claude OAuth token.\n"
        f"*Last output:* ```{output[:300]}```\n"
        "Manual intervention may be required: run `claude` on the host to re-authenticate."
    )

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
dag_instance = credential_keepalive()
