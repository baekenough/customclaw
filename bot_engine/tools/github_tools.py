"""GitHub issue tools for Claude API tool_use."""

from __future__ import annotations

import logging
import os

import httpx

from bot_engine.tools.base import BaseTool, ToolDefinition, ToolResult

log = logging.getLogger(__name__)
DEFAULT_TIMEOUT = 30


class CreateIssueTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="create_issue",
            description="Create a new GitHub issue in the project repository.",
            input_schema={
                "type": "object",
                "properties": {
                    "title": {"type": "string", "description": "Issue title"},
                    "body": {"type": "string", "description": "Issue body in Markdown"},
                    "labels": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Labels to apply",
                        "default": [],
                    },
                },
                "required": ["title", "body"],
            },
        )

    def execute(self, *, repo: str = "", token: str = "", **kwargs) -> ToolResult:
        title = kwargs.get("title", "")
        body = kwargs.get("body", "")
        labels = kwargs.get("labels", [])

        if not repo or not token:
            return ToolResult(content="Missing repo or token configuration", is_error=True)

        headers = {
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
        }
        try:
            resp = httpx.post(
                f"https://api.github.com/repos/{repo}/issues",
                json={"title": title, "body": body, "labels": labels},
                headers=headers,
                timeout=DEFAULT_TIMEOUT,
            )
            resp.raise_for_status()
            issue = resp.json()
            return ToolResult(
                content=f"Issue created: #{issue['number']} - {issue['title']}\nURL: {issue['html_url']}"
            )
        except Exception as e:
            return ToolResult(content=f"Failed to create issue: {e}", is_error=True)


class QueryIssuesTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="query_issues",
            description="Search or list GitHub issues in the project repository.",
            input_schema={
                "type": "object",
                "properties": {
                    "state": {
                        "type": "string",
                        "enum": ["open", "closed", "all"],
                        "default": "open",
                    },
                    "labels": {"type": "string", "description": "Comma-separated labels to filter"},
                    "query": {"type": "string", "description": "Search query text"},
                    "limit": {"type": "integer", "default": 10},
                },
            },
        )

    def execute(self, *, repo: str = "", token: str = "", **kwargs) -> ToolResult:
        state = kwargs.get("state", "open")
        labels = kwargs.get("labels", "")
        query = kwargs.get("query", "")
        limit = kwargs.get("limit", 10)

        if not repo or not token:
            return ToolResult(content="Missing repo or token configuration", is_error=True)

        headers = {
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
        }
        try:
            params = {"state": state, "per_page": min(limit, 30)}
            if labels:
                params["labels"] = labels

            resp = httpx.get(
                f"https://api.github.com/repos/{repo}/issues",
                params=params,
                headers=headers,
                timeout=DEFAULT_TIMEOUT,
            )
            resp.raise_for_status()
            issues = resp.json()

            if not issues:
                return ToolResult(content="No issues found matching criteria.")

            lines = []
            for issue in issues[:limit]:
                labels_str = ", ".join(l["name"] for l in issue.get("labels", []))
                lines.append(f"- #{issue['number']}: {issue['title']} [{labels_str}] ({issue['state']})")

            return ToolResult(content="\n".join(lines))
        except Exception as e:
            return ToolResult(content=f"Failed to query issues: {e}", is_error=True)
