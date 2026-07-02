"""FastMCP server definition with tools for notebook manipulation via CRDT.

Tool naming: every tool uses ``<noun>_<verb>`` form. Two nouns partition the
client-facing catalog:

  * ``notebook_*`` — filesystem AND notebook-scoped CRDT operations
  * ``cell_*``     — in-memory cell mutations

Plus one read-only diagnostic, ``room_list``.

**No client-side room management.** The server manages CRDT rooms invisibly.
Every notebook_* and cell_* tool auto-attaches to whichever room exists for
the path (UI tab, another MCP session, this one), or creates one if none
exists. Single room per notebook is an invariant. Idle rooms are flushed
and closed by a server-side sweeper after ``MCP_ROOM_IDLE_TIMEOUT_SEC``.

The room_open / room_close / room_close_all / room_pick tools that existed
in earlier versions were removed in the 2026-05-06 cutover — clients no
longer need to reason about CRDT rooms.
"""

from __future__ import annotations

import os
from typing import Any

from fastmcp import FastMCP

from .rtc_adapter import RTCAdapter


def create_mcp_server(adapter: RTCAdapter) -> FastMCP:
    """Create and configure the FastMCP server with all tools."""
    mcp = FastMCP(
        os.environ.get("MCP_SERVER_NAME", "jupyter"),
        instructions=(
            "JupyterLab MCP server with real-time collaboration. Tools are "
            "noun-shaped: notebook_* (filesystem + room-attached notebook "
            "ops), cell_* (in-memory cell mutations), room_list (read-only "
            "diagnostic). Rooms are managed by the server: every "
            "notebook_*/cell_* call auto-attaches to whichever CRDT room is "
            "already open for that notebook (JupyterLab UI tab, another MCP "
            "session, or your own), or creates a fresh room if none exists. "
            "Single room per notebook is an invariant — MCP and the UI "
            "always share one Y.Doc per logical file. Idle rooms are "
            "flushed and closed automatically."
        ),
    )

    # ── notebook_* — filesystem + notebook-scoped CRDT operations ────

    @mcp.tool()
    async def notebook_list() -> list[dict[str, str]]:
        """List all notebooks accessible in the workspace.

        Filesystem-only. Does not touch CRDT rooms.

        Returns a list of dicts with 'path' and 'name' for each notebook.
        """
        return await adapter.list_notebooks()

    @mcp.tool()
    async def notebook_create(path: str) -> dict[str, str]:
        """Create a new empty notebook on disk.

        ``path`` is canonicalized (rejects host paths and ``..`` escapes).
        Filesystem-only — the room is created on the next notebook_*/cell_*
        call against this path.

        Args:
            path: Path for the new notebook (e.g. "experiments/new.ipynb")

        Returns:
            Dict with 'path' and 'name' of the created notebook.
        """
        return await adapter.create_notebook(path)

    @mcp.tool()
    async def notebook_get(path: str) -> dict[str, Any] | None:
        """Get full notebook content (cells, metadata, kernel info).

        Auto-attaches to the existing room for ``path``, or creates one
        if none exists. Result reflects the live CRDT state, including
        unsaved edits from other clients.

        Args:
            path: Notebook path relative to the workspace root
        """
        return await adapter.get_notebook(path)

    @mcp.tool()
    async def notebook_watch(path: str, timeout: int = 30) -> dict[str, Any]:
        """Watch for changes to a notebook. Blocks until any cell is changed
        by another client (UI tab, another MCP session, kernel output) or
        until timeout.

        Auto-attaches to the existing room or creates one. Multiple watchers
        on the same notebook each receive their own notification.

        Args:
            path: Notebook path relative to workspace root
            timeout: Seconds to wait before returning (default: 30, max: 300)

        Returns:
            ``{"changed": true, "cell_count": N}`` if a change was detected,
            ``{"changed": false}`` if the timeout expired with no changes.
        """
        clamped_timeout = min(max(timeout, 1), 300)
        return await adapter.watch_notebook(path, timeout=clamped_timeout)

    @mcp.tool()
    async def notebook_list_users(path: str) -> list[dict[str, str]]:
        """List awareness users currently connected to a notebook's CRDT room.

        Read-only diagnostic. If no room exists yet for ``path``, returns
        an empty list (does NOT auto-create — observation only).

        Args:
            path: Notebook path relative to workspace root

        Returns:
            List of dicts with 'id' and 'name' for each connected user.
        """
        return await adapter.list_notebook_users(path)

    # ── cell_* — in-memory cell mutations (auto-attach) ──────────────

    @mcp.tool()
    async def cell_get(path: str, index: int) -> dict[str, Any]:
        """Get a specific cell's content from a notebook.

        Auto-attaches to the existing room or creates one.

        Args:
            path: Notebook path relative to workspace root
            index: Zero-based cell index

        Returns:
            Cell dict with 'source', 'cell_type', 'metadata', 'id', and
            'outputs'/'execution_count' for code cells.
        """
        return await adapter.get_cell(path, index)

    @mcp.tool()
    async def cell_update(
        path: str,
        index: int,
        source: str,
        cell_type: str | None = None,
    ) -> str:
        """Update a cell's content in place. The change syncs live to all
        collaborators (UI tabs, other MCP sessions).

        The cell's stable ``id`` is preserved across the update — the same
        cell, with the same identity in the CRDT log, just with new source.
        This is critical: pre-2026-05-06 versions of this tool would mint a
        fresh UUID on every update, producing silent cell duplication when
        the room had any concurrent state.

        Args:
            path: Notebook path
            index: Zero-based cell index
            source: New cell source code/text
            cell_type: Optional new cell type ("code", "markdown", "raw").
                       If not provided, keeps the existing type.
        """
        existing = await adapter.get_cell(path, index)
        cell_value = {
            "id": existing["id"],
            "cell_type": cell_type or existing["cell_type"],
            "source": source,
            "metadata": existing.get("metadata", {}),
        }
        if cell_value["cell_type"] == "code":
            # Preserve existing outputs/execution_count unless caller wants
            # to clear them. We do NOT auto-clear: a bare cell_update should
            # only change source, not invalidate prior execution state.
            if "outputs" in existing:
                cell_value["outputs"] = existing["outputs"]
            if "execution_count" in existing:
                cell_value["execution_count"] = existing["execution_count"]
        await adapter.set_cell(path, index, cell_value)
        return f"Cell {index} updated"

    @mcp.tool()
    async def cell_insert(
        path: str,
        index: int,
        source: str,
        cell_type: str = "code",
    ) -> str:
        """Insert a new cell at the given position. Syncs live to all
        collaborators.

        Auto-attaches to the existing room or creates one.

        Args:
            path: Notebook path
            index: Position to insert at (0 = beginning, -1 or cell_count = end)
            source: Cell source code/text
            cell_type: Cell type — "code" (default), "markdown", or "raw"
        """
        await adapter.insert_cell(path, index, source, cell_type)
        return f"Cell inserted at index {index}"

    @mcp.tool()
    async def cell_delete(path: str, index: int) -> str:
        """Delete a cell from a notebook. Syncs live to all collaborators.

        Auto-attaches to the existing room or creates one.

        Args:
            path: Notebook path
            index: Zero-based index of the cell to delete
        """
        await adapter.delete_cell(path, index)
        return f"Cell {index} deleted"

    @mcp.tool()
    async def cell_execute(path: str, index: int) -> list[dict[str, Any]]:
        """Execute a cell and return its outputs.

        Auto-attaches to the existing room or creates one. Starts a kernel
        if one isn't already running for this notebook. Outputs are
        persisted back to the cell via in-place CRDT mutation so they are
        visible to UI clients and survive the next disk save.

        Args:
            path: Notebook path
            index: Zero-based index of the cell to execute

        Returns:
            List of output dicts, each with 'type' (stream, display_data,
            execute_result, error) and 'content'.
        """
        return await adapter.execute_cell(path, index)

    # ── room_list — read-only diagnostic ─────────────────────────────

    @mcp.tool()
    async def room_list() -> list[dict[str, Any]]:
        """List every active CRDT room with full metadata.

        Read-only diagnostic. Reports rooms in the server's registry —
        UI-created and MCP-created alike — with each room's notebook path
        (reverse-resolved via the file id manager), the underlying file_id,
        the list of currently-attached awareness users, the user count,
        and whether a Jupyter kernel session is bound to the path.

        Use this to verify the single-room-per-notebook invariant. There
        should never be two rooms with the same path.

        Returns:
            List of dicts with keys: room_id, path, file_id, users,
            user_count, has_kernel.
        """
        return await adapter.list_rooms()

    return mcp
