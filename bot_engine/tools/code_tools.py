"""Code search tool using a local LLM CLI subprocess."""

from __future__ import annotations

import logging
import os
import subprocess
import tempfile

from bot_engine.tools.base import BaseTool, ToolDefinition, ToolResult

log = logging.getLogger(__name__)


class SearchCodeTool(BaseTool):
    def definition(self) -> ToolDefinition:
        return ToolDefinition(
            name="search_code",
            description="Search and explore the project codebase to answer questions about code structure, patterns, and implementation details.",
            input_schema={
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "The question or search query about the codebase",
                    },
                },
                "required": ["query"],
            },
        )

    def execute(self, *, repo_path: str = "", max_turns: int = 10, **kwargs) -> ToolResult:
        query = kwargs.get("query", "")

        if not repo_path:
            return ToolResult(content="No project repo path configured", is_error=True)

        if not os.path.isdir(repo_path):
            return ToolResult(content=f"Repo path does not exist: {repo_path}", is_error=True)

        claude_cli = os.environ.get("CLAUDE_CLI_PATH", "claude")
        prompt_file = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False) as f:
                f.write(query)
                prompt_file = f.name

            cmd = (
                f'NO_COLOR=1 {claude_cli} -p "$(cat {prompt_file})" '
                f'--model sonnet --max-turns {max_turns} '
                f'--allowedTools Read,Glob,Grep'
            )
            result = subprocess.run(
                cmd,
                shell=True,
                capture_output=True,
                text=True,
                timeout=max_turns * 60,
                stdin=subprocess.DEVNULL,
                cwd=repo_path,
            )

            if result.returncode != 0:
                stderr = result.stderr[:500] if result.stderr else "Unknown error"
                return ToolResult(content=f"코드 검색 실행 오류: {stderr}", is_error=True)

            output = result.stdout.strip()
            if not output:
                return ToolResult(content="코드 검색 결과가 비어 있습니다", is_error=True)

            # Truncate very long outputs
            if len(output) > 4000:
                output = output[:4000] + "\n\n... (truncated)"

            return ToolResult(content=output)

        except subprocess.TimeoutExpired:
            return ToolResult(
                content=f"Code search timed out after {max_turns * 60}s",
                is_error=True,
            )
        except Exception as e:
            return ToolResult(content=f"Code search failed: {e}", is_error=True)
        finally:
            if prompt_file and os.path.exists(prompt_file):
                os.unlink(prompt_file)
