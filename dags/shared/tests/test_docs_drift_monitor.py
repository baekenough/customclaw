"""Tests for docs_drift_monitor helper functions."""
from __future__ import annotations

import sys
import os

# Ensure the dags directory is importable without an Airflow environment.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", ".."))

import pytest

# Stub out Airflow dependencies before importing the module so that the tests
# can run without a full Airflow installation.
from unittest.mock import MagicMock

airflow_stub = MagicMock()
sys.modules.setdefault("airflow", airflow_stub)
sys.modules.setdefault("airflow.exceptions", airflow_stub.exceptions)
sys.modules.setdefault("airflow.sdk", airflow_stub.sdk)

from dags.shared.docs_drift_monitor import (  # noqa: E402
    _compute_structural_diff,
    _filter_structural_lines,
)


# ---------------------------------------------------------------------------
# _filter_structural_lines
# ---------------------------------------------------------------------------


class TestFilterStructuralLines:
    """Unit tests for _filter_structural_lines."""

    def test_keeps_path_lines(self):
        """Lines starting with '/' (doc paths) are retained."""
        lines = {"/docs/overview", "/api/reference/tools"}
        result = _filter_structural_lines(lines)
        assert "/docs/overview" in result
        assert "/api/reference/tools" in result

    def test_keeps_markdown_headers(self):
        """Markdown section headers (## Section) are retained."""
        lines = {"## Getting Started", "# Overview", "### Advanced Usage"}
        result = _filter_structural_lines(lines)
        assert "## Getting Started" in result
        assert "# Overview" in result
        assert "### Advanced Usage" in result

    def test_keeps_llms_txt_entries(self):
        """llms.txt page entries starting with '- ' are retained."""
        lines = {
            "- [Claude Code Overview](https://code.claude.com/docs): intro",
            "- Claude Code: https://code.claude.com",
        }
        result = _filter_structural_lines(lines)
        assert any("Claude Code Overview" in r for r in result)
        assert any("Claude Code:" in r for r in result)

    def test_rejects_html_noise(self):
        """HTML tags, CSS classes, and JS noise are filtered out."""
        noisy_lines = {
            "<div class=\"container\">",
            "class=\"nav-menu\"",
            "function onClick(event) {",
            "<script src=\"app.js\">",
            "window.location.href = '/home';",
            "  background-color: #fff;",
        }
        result = _filter_structural_lines(noisy_lines)
        assert result == []

    def test_rejects_short_lines(self):
        """Lines with fewer than 3 characters are discarded."""
        lines = {"ab", "x", "  ", ""}
        result = _filter_structural_lines(lines)
        assert result == []

    def test_rejects_plain_text_paragraphs(self):
        """Plain text longer than 5 chars is NOT kept (old catch-all removed)."""
        lines = {
            "Welcome to the documentation portal.",
            "This is a paragraph about tool usage.",
            "Updated the retry logic for better performance.",
        }
        result = _filter_structural_lines(lines)
        # None of these match path, header, or llms.txt list-entry patterns.
        assert result == []

    def test_rejects_very_long_lines(self):
        """Lines exceeding 300 characters are discarded (likely minified code)."""
        long_line = "- " + "a" * 301
        result = _filter_structural_lines({long_line})
        assert result == []

    def test_empty_input(self):
        """An empty set returns an empty list."""
        assert _filter_structural_lines(set()) == []


# ---------------------------------------------------------------------------
# _compute_structural_diff
# ---------------------------------------------------------------------------


class TestComputeStructuralDiff:
    """Unit tests for _compute_structural_diff."""

    def test_detects_added_and_removed(self):
        """Correct added/removed lists for a simple structural change."""
        baseline = "/docs/old-page\n## Old Section\n- [Old](https://example.com/old): desc"
        current = "/docs/new-page\n## New Section\n- [New](https://example.com/new): desc"

        added, removed = _compute_structural_diff(baseline, current)

        assert "/docs/new-page" in added
        assert "## New Section" in added
        assert "/docs/old-page" in removed
        assert "## Old Section" in removed

    def test_identical_content_returns_empty(self):
        """No diff is produced when baseline and current are identical."""
        content = "/docs/overview\n## Section\n- [Page](https://example.com): info"
        added, removed = _compute_structural_diff(content, content)
        assert added == []
        assert removed == []

    def test_added_only(self):
        """Only added lines appear when current has new structural entries."""
        baseline = "/docs/intro"
        current = "/docs/intro\n/docs/advanced"

        added, removed = _compute_structural_diff(baseline, current)

        assert "/docs/advanced" in added
        assert removed == []

    def test_removed_only(self):
        """Only removed lines appear when current drops a structural entry."""
        baseline = "/docs/intro\n/docs/deprecated"
        current = "/docs/intro"

        added, removed = _compute_structural_diff(baseline, current)

        assert removed == ["/docs/deprecated"]
        assert added == []
