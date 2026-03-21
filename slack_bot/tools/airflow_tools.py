"""Airflow DAG tools for Claude API tool_use."""

from __future__ import annotations

import logging
import os

import httpx

from slack_bot.tools.base import BaseTool, ToolDefinition, ToolResult

log = logging.getLogger(__name__)
DEFAULT_TIMEOUT = 30


def _get_airflow_token() -> tuple[str, dict]:
    """Get Airflow API base URL and auth headers using token authentication."""
    base_url = os.environ.get("AIRFLOW_API_URL", "http://airflow:8080/api/v2")
    # Extract base (without /api/v2) for token endpoint
    airflow_base = base_url.replace("/api/v2", "")
    user = os.environ.get("AIRFLOW_API_USER", "admin")
    password = os.environ.get("AIRFLOW_API_PASSWORD", "")

    try:
        resp = httpx.post(
            f"{airflow_base}/auth/token",
            json={"username": user, "password": password},
            timeout=DEFAULT_TIMEOUT,
        )
        resp.raise_for_status()
        token = resp.json()["access_token"]
        return base_url, {"Authorization": f"Bearer {token}"}
    except Exception as e:
        log.warning("Failed to get Airflow token: %s", e)
        return base_url, {}


class GetDagStatusTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="get_dag_status",
            description="Get the latest run status of a specific Airflow DAG.",
            input_schema={
                "type": "object",
                "properties": {
                    "dag_id": {"type": "string", "description": "The DAG ID to check"},
                },
                "required": ["dag_id"],
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        dag_id = kwargs.get("dag_id", "")
        base_url, headers = _get_airflow_token()

        try:
            resp = httpx.get(
                f"{base_url}/dags/{dag_id}/dagRuns",
                params={"limit": 1, "order_by": "-start_date"},
                headers=headers,
                timeout=DEFAULT_TIMEOUT,
            )
            resp.raise_for_status()
            data = resp.json()
            runs = data.get("dag_runs", [])

            if not runs:
                return ToolResult(content=f"DAG '{dag_id}': No runs found.")

            run = runs[0]
            return ToolResult(
                content=(
                    f"DAG '{dag_id}' latest run:\n"
                    f"- State: {run['state']}\n"
                    f"- Start: {run.get('start_date', 'N/A')}\n"
                    f"- End: {run.get('end_date', 'N/A')}\n"
                    f"- Run ID: {run['dag_run_id']}"
                )
            )
        except Exception as e:
            return ToolResult(content=f"Failed to get DAG status: {e}", is_error=True)


class ListDagRunsTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="list_dag_runs",
            description="List recent runs for a specific Airflow DAG.",
            input_schema={
                "type": "object",
                "properties": {
                    "dag_id": {"type": "string", "description": "The DAG ID"},
                    "limit": {"type": "integer", "default": 10},
                },
                "required": ["dag_id"],
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        dag_id = kwargs.get("dag_id", "")
        limit = kwargs.get("limit", 10)
        base_url, headers = _get_airflow_token()

        try:
            resp = httpx.get(
                f"{base_url}/dags/{dag_id}/dagRuns",
                params={"limit": limit, "order_by": "-start_date"},
                headers=headers,
                timeout=DEFAULT_TIMEOUT,
            )
            resp.raise_for_status()
            data = resp.json()
            runs = data.get("dag_runs", [])

            if not runs:
                return ToolResult(content=f"DAG '{dag_id}': No runs found.")

            lines = [f"DAG '{dag_id}' recent runs ({len(runs)}):"]
            for run in runs:
                lines.append(
                    f"- {run['dag_run_id']}: {run['state']} "
                    f"(started: {run.get('start_date', 'N/A')})"
                )

            return ToolResult(content="\n".join(lines))
        except Exception as e:
            return ToolResult(content=f"Failed to list DAG runs: {e}", is_error=True)


class ListDagsTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="list_dags",
            description=(
                "List all available Airflow DAGs with their current status."
                " Use this when the user asks about DAG status without specifying a specific DAG."
            ),
            input_schema={
                "type": "object",
                "properties": {},
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        base_url, headers = _get_airflow_token()

        try:
            resp = httpx.get(
                f"{base_url}/dags",
                params={"limit": 50},
                headers=headers,
                timeout=DEFAULT_TIMEOUT,
            )
            resp.raise_for_status()
            dags_data = resp.json().get("dags", [])

            if not dags_data:
                return ToolResult(content="No DAGs found.")

            lines = [f"Airflow DAGs ({len(dags_data)}):"]
            for dag in dags_data:
                dag_id = dag["dag_id"]
                is_paused = dag.get("is_paused", False)
                status_icon = "\u23f8\ufe0f" if is_paused else "\u25b6\ufe0f"

                try:
                    runs_resp = httpx.get(
                        f"{base_url}/dags/{dag_id}/dagRuns",
                        params={"limit": 1, "order_by": "-start_date"},
                        headers=headers,
                        timeout=DEFAULT_TIMEOUT,
                    )
                    runs_resp.raise_for_status()
                    runs = runs_resp.json().get("dag_runs", [])
                    if runs:
                        run = runs[0]
                        state = run["state"]
                        state_icon = {
                            "success": "\u2705",
                            "failed": "\u274c",
                            "running": "\U0001f504",
                            "queued": "\u23f3",
                        }.get(state, "\u2753")
                        start = run.get("start_date", "N/A")
                        if start and start != "N/A":
                            start = start[:19]  # trim to seconds
                        lines.append(f"\n{status_icon} **{dag_id}**")
                        lines.append(f"  Latest: {state_icon} {state} (started: {start})")
                    else:
                        lines.append(f"\n{status_icon} **{dag_id}**")
                        lines.append("  Latest: No runs yet")
                except Exception:
                    lines.append(f"\n{status_icon} **{dag_id}**")
                    lines.append("  Latest: Unable to fetch run status")

            return ToolResult(content="\n".join(lines))
        except Exception as e:
            return ToolResult(content=f"Failed to list DAGs: {e}", is_error=True)


class TriggerDagTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="trigger_dag",
            description="Trigger a manual run of an Airflow DAG.",
            input_schema={
                "type": "object",
                "properties": {
                    "dag_id": {"type": "string", "description": "The DAG ID to trigger"},
                    "config": {
                        "type": "object",
                        "description": "Optional configuration to pass to the DAG",
                        "default": {},
                    },
                },
                "required": ["dag_id"],
            },
        )

    def execute(self, **kwargs) -> ToolResult:
        dag_id = kwargs.get("dag_id", "")
        config = kwargs.get("config", {})
        base_url, headers = _get_airflow_token()

        try:
            resp = httpx.post(
                f"{base_url}/dags/{dag_id}/dagRuns",
                json={"conf": config},
                headers=headers,
                timeout=DEFAULT_TIMEOUT,
            )
            resp.raise_for_status()
            run = resp.json()
            return ToolResult(
                content=(
                    f"DAG '{dag_id}' triggered successfully.\n"
                    f"- Run ID: {run['dag_run_id']}\n"
                    f"- State: {run['state']}"
                )
            )
        except Exception as e:
            return ToolResult(content=f"Failed to trigger DAG: {e}", is_error=True)
