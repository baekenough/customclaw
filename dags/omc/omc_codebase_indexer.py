"""
OMC Codebase Indexer DAG.

Indexes the oh-my-customcode codebase into OpenSearch for RAG-based
full-text code search.

Graph:
    scan_files ─► index_codebase
"""

from __future__ import annotations

import logging
import os
from datetime import datetime, timedelta

from airflow.sdk import dag, task
from airflow.exceptions import AirflowFailException

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
CODEBASE_ROOT = "/home/baekenough/workspace/oh-my-customcode"

INCLUDE_EXTENSIONS = {".ts", ".py", ".md", ".yaml", ".yml", ".json"}

EXCLUDE_DIRS = {"node_modules", ".git", "dist", "coverage"}

EXCLUDE_SUFFIXES = {".test.ts", ".spec.ts"}

OPENSEARCH_INDEX = "omc-codebase"

INDEX_MAPPINGS = {
    "mappings": {
        "properties": {
            "file_path": {"type": "keyword"},
            "content": {"type": "text", "analyzer": "standard"},
            "language": {"type": "keyword"},
            "chunk_index": {"type": "integer"},
            "indexed_at": {"type": "date"},
        }
    }
}

# Chunking parameters
CHUNK_LINES = 100
OVERLAP_LINES = 20

# Bulk index batch size — keep memory usage bounded
BULK_BATCH_SIZE = 200

# ---------------------------------------------------------------------------
# DAG definition
# ---------------------------------------------------------------------------
default_args = {
    "owner": "omc",
    "retries": 1,
    "retry_delay": timedelta(minutes=5),
}


@dag(
    dag_id="omc_codebase_indexer",
    description=(
        "Indexes the oh-my-customcode codebase into OpenSearch for "
        "RAG-based full-text code search."
    ),
    schedule="@daily",
    start_date=datetime(2026, 3, 19),
    catchup=False,
    tags=["project:omc", "indexing", "rag"],
    default_args=default_args,
    doc_md=__doc__,
)
def omc_codebase_indexer() -> None:
    """Orchestrate codebase scanning and OpenSearch indexing."""

    # ------------------------------------------------------------------
    # Task 1: Walk the codebase and collect file paths
    # ------------------------------------------------------------------

    @task()
    def scan_files() -> list[str]:
        """Walk the codebase directory and return matching file paths.

        Includes files with extensions: .ts, .py, .md, .yaml, .yml, .json.
        Excludes paths under: node_modules/, .git/, dist/, coverage/.
        Excludes test/spec files: *.test.ts, *.spec.ts.

        Returns:
            Sorted list of absolute file paths to index.
        """
        if not os.path.isdir(CODEBASE_ROOT):
            raise AirflowFailException(
                f"Codebase root does not exist or is not a directory: {CODEBASE_ROOT}"
            )

        collected: list[str] = []

        for dirpath, dirnames, filenames in os.walk(CODEBASE_ROOT):
            # Prune excluded directories in-place so os.walk skips them.
            dirnames[:] = [
                d for d in dirnames if d not in EXCLUDE_DIRS
            ]

            for filename in filenames:
                # Check excluded suffixes first (e.g. .test.ts, .spec.ts)
                if any(filename.endswith(s) for s in EXCLUDE_SUFFIXES):
                    continue

                # Check included extensions
                _, ext = os.path.splitext(filename)
                if ext not in INCLUDE_EXTENSIONS:
                    continue

                collected.append(os.path.join(dirpath, filename))

        collected.sort()
        log.info(
            "scan_files: found %d files under %s", len(collected), CODEBASE_ROOT
        )
        return collected

    # ------------------------------------------------------------------
    # Task 2: Chunk files and bulk-index into OpenSearch
    # ------------------------------------------------------------------

    @task()
    def index_codebase(files: list[str]) -> dict:
        """Read, chunk, and bulk-index all files into OpenSearch.

        For each file:
        - Reads content (skips unreadable files with a warning).
        - Splits into chunks of ~100 lines with 20-line overlap.
        - Produces documents: {file_path, content, language, chunk_index}.

        Deletes the existing index before recreating it to ensure a clean
        full re-index on every DAG run.

        Args:
            files: List of file paths from ``scan_files``.

        Returns:
            Summary dict: {total_files, total_chunks, indexed, errors}.
        """
        # Import inside task to avoid top-level import cost on every DAG parse.
        from opensearchpy import OpenSearch
        from opensearchpy.helpers import bulk

        opensearch_url = os.environ.get("OPENSEARCH_URL", "http://opensearch:9200")
        indexed_at = datetime.utcnow().isoformat() + "Z"

        client = _build_opensearch_client(opensearch_url)
        _recreate_index(client, OPENSEARCH_INDEX, INDEX_MAPPINGS)

        total_chunks = 0
        total_errors = 0
        batch: list[dict] = []

        def _flush(batch: list[dict]) -> int:
            """Bulk-index a batch; return the number of errors."""
            if not batch:
                return 0
            success, failed = bulk(client, batch, raise_on_error=False)
            if failed:
                log.warning(
                    "Bulk index partial failure: %d succeeded, %d failed",
                    success,
                    len(failed),
                )
            return len(failed)

        for file_path in files:
            try:
                with open(file_path, encoding="utf-8", errors="replace") as fh:
                    content = fh.read()
            except OSError as exc:
                log.warning("Cannot read %s: %s", file_path, exc)
                total_errors += 1
                continue

            language = _detect_language(file_path)
            lines = content.splitlines()
            chunks = _chunk_lines(lines, CHUNK_LINES, OVERLAP_LINES)

            for chunk_index, chunk_lines in enumerate(chunks):
                doc = {
                    "_index": OPENSEARCH_INDEX,
                    "_source": {
                        "file_path": file_path,
                        "content": "\n".join(chunk_lines),
                        "language": language,
                        "chunk_index": chunk_index,
                        "indexed_at": indexed_at,
                    },
                }
                batch.append(doc)
                total_chunks += 1

                if len(batch) >= BULK_BATCH_SIZE:
                    total_errors += _flush(batch)
                    batch = []

        # Flush any remaining documents.
        total_errors += _flush(batch)

        summary = {
            "total_files": len(files),
            "total_chunks": total_chunks,
            "indexed": total_chunks - total_errors,
            "errors": total_errors,
        }
        log.info(
            "index_codebase complete: %d files, %d chunks, %d indexed, %d errors",
            summary["total_files"],
            summary["total_chunks"],
            summary["indexed"],
            summary["errors"],
        )

        if total_errors > 0 and total_chunks == total_errors:
            raise AirflowFailException(
                f"All {total_errors} bulk index operations failed."
            )

        return summary

    # ------------------------------------------------------------------
    # Wire up the task graph
    # ------------------------------------------------------------------
    files = scan_files()
    index_codebase(files)


# ---------------------------------------------------------------------------
# Module-level pure helper functions
# ---------------------------------------------------------------------------


def _detect_language(file_path: str) -> str:
    """Derive a short language tag from a file's extension.

    Args:
        file_path: Absolute or relative path to the file.

    Returns:
        Language tag string: one of "ts", "py", "md", "yaml", "json",
        or "unknown" for unrecognised extensions.
    """
    ext_map = {
        ".ts": "ts",
        ".py": "py",
        ".md": "md",
        ".yaml": "yaml",
        ".yml": "yaml",
        ".json": "json",
    }
    _, ext = os.path.splitext(file_path)
    return ext_map.get(ext, "unknown")


def _chunk_lines(
    lines: list[str],
    chunk_size: int,
    overlap: int,
) -> list[list[str]]:
    """Split a list of lines into overlapping chunks.

    Each chunk is at most ``chunk_size`` lines. Consecutive chunks share
    ``overlap`` lines so that context is not lost at chunk boundaries.

    An empty ``lines`` list returns a single chunk containing an empty list,
    so callers always receive at least one document per file.

    Args:
        lines: Source lines to chunk.
        chunk_size: Maximum number of lines per chunk.
        overlap: Number of lines shared between adjacent chunks.

    Returns:
        List of line-list chunks.

    Examples:
        >>> _chunk_lines(["a","b","c","d","e"], chunk_size=3, overlap=1)
        [['a', 'b', 'c'], ['c', 'd', 'e']]
    """
    if not lines:
        return [[]]

    step = max(chunk_size - overlap, 1)
    chunks: list[list[str]] = []
    start = 0

    while start < len(lines):
        chunks.append(lines[start : start + chunk_size])
        start += step

    return chunks


def _build_opensearch_client(url: str) -> "OpenSearch":  # noqa: F821
    """Create an OpenSearch client from a URL string.

    Supports ``http://host:port`` and ``https://host:port`` schemes.
    For HTTPS, server TLS verification is skipped (suitable for
    self-signed certs in local/dev deployments).

    Args:
        url: OpenSearch base URL, e.g. "http://opensearch:9200".

    Returns:
        Configured :class:`opensearchpy.OpenSearch` instance.
    """
    from opensearchpy import OpenSearch

    use_ssl = url.startswith("https://")
    host = url.replace("https://", "").replace("http://", "")

    return OpenSearch(
        hosts=[host],
        use_ssl=use_ssl,
        verify_certs=False,
        ssl_show_warn=False,
    )


def _recreate_index(client: "OpenSearch", index_name: str, mappings: dict) -> None:  # noqa: F821
    """Delete and recreate an OpenSearch index.

    Deletes the index if it exists, then creates it fresh with the
    provided mapping configuration. This ensures every DAG run starts
    from a clean slate (full re-index semantics).

    Args:
        client: Configured OpenSearch client.
        index_name: Name of the index to recreate.
        mappings: Full index mapping body dict (passed to create API).

    Raises:
        AirflowFailException: If index creation fails.
    """
    from opensearchpy.exceptions import NotFoundError, RequestError

    if client.indices.exists(index=index_name):
        client.indices.delete(index=index_name)
        log.info("Deleted existing index: %s", index_name)

    try:
        client.indices.create(index=index_name, body=mappings)
        log.info("Created index: %s", index_name)
    except RequestError as exc:
        raise AirflowFailException(
            f"Failed to create OpenSearch index '{index_name}': {exc}"
        ) from exc


# Instantiate the DAG
dag_instance = omc_codebase_indexer()
