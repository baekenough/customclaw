"""LLM provider credential health probe.

Runs as a daemon thread, periodically checking all LLM provider credentials
and storing results in PostgreSQL. Sends Slack alerts on failure transitions.
"""

import json
import logging
import os
import subprocess
import threading
import time
from typing import Optional

import psycopg2
import requests

log = logging.getLogger(__name__)

# Environment variable names
_ENV_DATABASE_DSN = "DATABASE_DSN"
_ENV_SLACK_TOKEN = "CUSTOMCLAW_SLACK_BOT_TOKEN"
_ENV_ALERT_CHANNEL = "CREDENTIAL_ALERT_CHANNEL"
_ENV_CLAUDE_CLI_PATH = "CLAUDE_CLI_PATH"
_ENV_CONTAINER_HOME = "CONTAINER_HOME"
_ENV_OPENAI_API_KEY = "OPENAI_API_KEY"
_ENV_GEMINI_API_KEY = "GEMINI_API_KEY"

_DEFAULT_ALERT_CHANNEL = "C0AMBNY135Z"
_DEFAULT_CLAUDE_CLI = "claude"
_DEFAULT_CONTAINER_HOME = "/home/appuser"

# Track previous states to detect ok → error transitions
_previous_states: dict[str, str] = {}
_states_lock = threading.Lock()


# ---------------------------------------------------------------------------
# Provider checks
# ---------------------------------------------------------------------------


def _check_claude() -> tuple[str, Optional[str]]:
    """Check Anthropic Claude CLI credentials.

    Returns:
        (status, error_message) where status is "ok" or "error".
    """
    cli_path = os.environ.get(_ENV_CLAUDE_CLI_PATH, _DEFAULT_CLAUDE_CLI)
    home_dir = os.environ.get(_ENV_CONTAINER_HOME, _DEFAULT_CONTAINER_HOME)

    try:
        result = subprocess.run(
            [
                cli_path,
                "-p", ".",
                "--model", "haiku",
                "--max-turns", "1",
                "--output-format", "json",
            ],
            capture_output=True,
            text=True,
            timeout=30,
            env={**os.environ, "HOME": home_dir},
        )

        output = result.stdout.strip()
        if not output:
            output = result.stderr.strip()

        try:
            data = json.loads(output)
        except json.JSONDecodeError:
            # Non-JSON output — treat process exit code as signal
            if result.returncode != 0:
                return "error", output or "Claude CLI exited with non-zero status"
            return "ok", None

        if not data.get("is_error", False):
            return "ok", None

        # is_error: true — inspect the message
        raw = json.dumps(data)
        if "authentication_error" in raw or "expired" in raw:
            error_msg = data.get("error", {})
            if isinstance(error_msg, dict):
                error_msg = error_msg.get("message", raw)
            return "error", str(error_msg)

        return "error", data.get("error", raw)

    except subprocess.TimeoutExpired:
        return "error", "Claude CLI timed out after 30 seconds"
    except FileNotFoundError:
        return "error", f"Claude CLI not found at path: {cli_path}"
    except Exception as exc:
        return "error", str(exc)


def _check_openai() -> tuple[str, Optional[str]]:
    """Check OpenAI API credentials.

    Returns:
        (status, error_message) where status is "ok", "error", or "unconfigured".
    """
    api_key = os.environ.get(_ENV_OPENAI_API_KEY, "")
    if not api_key:
        return "unconfigured", None

    try:
        response = requests.get(
            "https://api.openai.com/v1/models",
            headers={"Authorization": f"Bearer {api_key}"},
            timeout=10,
        )
        if response.status_code == 200:
            return "ok", None
        if response.status_code == 401:
            try:
                msg = response.json().get("error", {}).get("message", response.text)
            except Exception:
                msg = response.text
            return "error", str(msg)
        return "error", f"Unexpected status {response.status_code}: {response.text[:200]}"

    except requests.Timeout:
        return "error", "OpenAI API timed out after 10 seconds"
    except Exception as exc:
        return "error", str(exc)


def _check_gemini() -> tuple[str, Optional[str]]:
    """Check Google Gemini API credentials.

    Returns:
        (status, error_message) where status is "ok", "error", or "unconfigured".
    """
    api_key = os.environ.get(_ENV_GEMINI_API_KEY, "")
    if not api_key:
        return "unconfigured", None

    try:
        response = requests.get(
            "https://generativelanguage.googleapis.com/v1beta/models",
            params={"key": api_key},
            timeout=10,
        )
        if response.status_code == 200:
            return "ok", None
        if response.status_code in (400, 403):
            try:
                msg = response.json().get("error", {}).get("message", response.text)
            except Exception:
                msg = response.text
            return "error", str(msg)
        return "error", f"Unexpected status {response.status_code}: {response.text[:200]}"

    except requests.Timeout:
        return "error", "Gemini API timed out after 10 seconds"
    except Exception as exc:
        return "error", str(exc)


# ---------------------------------------------------------------------------
# Database storage
# ---------------------------------------------------------------------------


def _save_status(provider: str, status: str, error: Optional[str]) -> None:
    """Upsert credential status into the credential_status table.

    Args:
        provider: Provider identifier (e.g. "claude", "openai", "gemini").
        status: Current status string ("ok", "error", "unconfigured").
        error: Error message if status is "error", otherwise None.
    """
    dsn = os.environ.get(_ENV_DATABASE_DSN)
    if not dsn:
        log.warning("DATABASE_DSN not set; skipping credential status persistence")
        return

    try:
        with psycopg2.connect(dsn) as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    INSERT INTO credential_status (provider, status, error, checked_at)
                    VALUES (%s, %s, %s, NOW())
                    ON CONFLICT (provider) DO UPDATE
                        SET status     = EXCLUDED.status,
                            error      = EXCLUDED.error,
                            checked_at = EXCLUDED.checked_at
                    """,
                    (provider, status, error),
                )
            conn.commit()
    except Exception as exc:
        log.error("Failed to save credential status for %s: %s", provider, exc)


# ---------------------------------------------------------------------------
# Slack alerting
# ---------------------------------------------------------------------------


def _send_slack_alert(provider: str, error: Optional[str]) -> None:
    """Send a Slack alert for a credential failure.

    Args:
        provider: The provider that failed.
        error: The error message to include.
    """
    token = os.environ.get(_ENV_SLACK_TOKEN)
    if not token:
        log.warning("CUSTOMCLAW_SLACK_BOT_TOKEN not set; skipping Slack alert")
        return

    channel = os.environ.get(_ENV_ALERT_CHANNEL, _DEFAULT_ALERT_CHANNEL)
    text = (
        f"\u26a0\ufe0f *LLM Credential Alert*\n"
        f"{provider} \uc778\uc99d \uc2e4\ud328: {error}\n"
        f"\uc11c\ubc84\uc5d0\uc11c credential \uac31\uc2e0\uc774 \ud544\uc694\ud569\ub2c8\ub2e4."
    )

    try:
        response = requests.post(
            "https://slack.com/api/chat.postMessage",
            headers={
                "Authorization": f"Bearer {token}",
                "Content-Type": "application/json",
            },
            json={"channel": channel, "text": text},
            timeout=10,
        )
        data = response.json()
        if not data.get("ok"):
            log.error("Slack alert failed: %s", data.get("error", response.text))
    except Exception as exc:
        log.error("Failed to send Slack alert: %s", exc)


def _maybe_alert(provider: str, status: str, error: Optional[str]) -> None:
    """Send a Slack alert only when transitioning into an error state.

    Alerts fire on:
    - First check result is "error" (no previous state)
    - Transition from any non-error state → "error"

    Args:
        provider: Provider identifier.
        status: New status after the check.
        error: Error message if status is "error".
    """
    with _states_lock:
        previous = _previous_states.get(provider)
        _previous_states[provider] = status

    if status == "error" and previous != "error":
        _send_slack_alert(provider, error)


# ---------------------------------------------------------------------------
# Main probe loop
# ---------------------------------------------------------------------------


_PROVIDERS: list[tuple[str, object]] = [
    ("claude", _check_claude),
    ("openai", _check_openai),
    ("gemini", _check_gemini),
]


def _run_probe() -> None:
    """Execute one full round of credential checks across all providers."""
    for provider, check_fn in _PROVIDERS:
        try:
            status, error = check_fn()
            log.info("Credential probe [%s]: %s%s", provider, status, f" — {error}" if error else "")
            _save_status(provider, status, error)
            _maybe_alert(provider, status, error)
        except Exception as exc:
            log.error("Unexpected error in credential probe for %s: %s", provider, exc)


def start_credential_probe(interval_seconds: int = 1800) -> threading.Thread:
    """Start the credential probe as a daemon thread.

    Runs all provider checks immediately on startup, then repeats at the
    specified interval.

    Args:
        interval_seconds: Seconds between probe runs. Default: 1800 (30 minutes).

    Returns:
        The running daemon thread.
    """

    def _loop() -> None:
        log.info("Credential probe starting (interval: %ds)", interval_seconds)
        while True:
            try:
                _run_probe()
            except Exception as exc:
                log.error("Credential probe loop error: %s", exc)
            time.sleep(interval_seconds)

    thread = threading.Thread(target=_loop, name="credential-probe", daemon=True)
    thread.start()
    return thread
