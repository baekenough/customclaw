"""Base tool interface for Claude API tool_use."""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any


@dataclass
class ToolDefinition:
    name: str
    description: str
    input_schema: dict[str, Any]


@dataclass
class ToolResult:
    content: str
    is_error: bool = False


class BaseTool(ABC):
    @abstractmethod
    def definition(self) -> ToolDefinition:
        ...

    @abstractmethod
    def execute(self, **kwargs) -> ToolResult:
        ...


class ToolRegistry:
    def __init__(self) -> None:
        self._tools: dict[str, BaseTool] = {}

    def register(self, tool: BaseTool) -> None:
        defn = tool.definition()
        self._tools[defn.name] = tool

    def get(self, name: str) -> BaseTool | None:
        return self._tools.get(name)

    def get_definitions(self, enabled: list[str] | None = None) -> list[dict]:
        """Return Claude API tool definitions for enabled tools."""
        result = []
        for name, tool in self._tools.items():
            if enabled is None or name in enabled:
                defn = tool.definition()
                result.append({
                    "name": defn.name,
                    "description": defn.description,
                    "input_schema": defn.input_schema,
                })
        return result

    def list_names(self) -> list[str]:
        return list(self._tools.keys())
