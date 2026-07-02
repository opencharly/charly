"""RTC Adapter: accesses jupyter-collaboration CRDT documents for MCP tools.

Design principles (post-2026-05-06 cutover):

* **Auto-attach.** Every path-accepting method transparently joins whichever
  CRDT room exists for that notebook (UI tab, another MCP session, or this
  one), or creates one if none exists. Clients never call ``open_room`` /
  ``close_room`` — those tools were deleted from the MCP surface.
  ``_resolve_notebook_doc`` is the single entry point.

* **Single-room invariant.** All paths are canonicalized to a workspace-
  relative form before reaching ``file_id_manager.index()`` so MCP and the
  JupyterLab UI converge on the SAME ``room_id`` for the SAME logical file.
  Host paths and ``..`` escapes are rejected.

* **In-place ``set_cell``.** Cell mutations operate on the existing
  ``Y.Map`` in place — never delete-then-insert at the Y.Array level —
  preserving cell ``id`` and avoiding the phantom-cell residue we observed
  when delegating to upstream ``YNotebook.set_cell``.

* **Server-side idle cleanup.** A background sweeper periodically flushes
  and removes rooms that have been idle (no clients, no MCP activity) for
  longer than ``MCP_ROOM_IDLE_TIMEOUT_SEC``. Replaces the deleted
  client-side ``room_close`` semantic.

* **file_id_manager hygiene.** A one-shot cleanup on first use deletes
  rows whose path is outside the notebook root (host-path leaks) or whose
  underlying file no longer exists (orphaned cruft).
"""

from __future__ import annotations

import asyncio
import logging
import os
import sqlite3
import time
from typing import Any

log = logging.getLogger(__name__)


class RoomNotOpenError(RuntimeError):
    """Raised when an internal lookup expects an existing room but finds none.

    Not exposed via any MCP tool after the auto-attach cutover — every tool
    auto-creates rooms via ``_resolve_notebook_doc``. Kept for callers that
    explicitly want a "fail if missing" semantic in internal code paths.
    """

    def __init__(self, path: str):
        super().__init__(f"No active CRDT room for '{path}'.")
        self.path = path


class NotebookWatcher:
    """Fan-out observer for a single notebook's CRDT changes.

    YNotebook.observe() only supports one callback at a time, so this class
    provides a single observer that dispatches to multiple asyncio.Event waiters.
    """

    def __init__(self):
        self._events: set[asyncio.Event] = set()

    def add_waiter(self) -> asyncio.Event:
        event = asyncio.Event()
        self._events.add(event)
        return event

    def remove_waiter(self, event: asyncio.Event) -> None:
        self._events.discard(event)

    def notify_all(self) -> None:
        for event in self._events:
            event.set()

    @property
    def has_waiters(self) -> bool:
        return len(self._events) > 0


class RTCAdapter:
    """Bridges MCP tool calls to jupyter-collaboration's CRDT documents."""

    # Idle-room sweeper defaults; overridable via env vars at extension load.
    IDLE_TIMEOUT_DEFAULT_SEC = 600
    SWEEP_INTERVAL_DEFAULT_SEC = 60

    def __init__(self, server_app):
        self.server_app = server_app
        self._ydoc_extension = None  # Resolved lazily to avoid load-order issues
        self._notebook_locks: dict[str, asyncio.Lock] = {}
        self._watchers: dict[str, NotebookWatcher] = {}  # rel-path → watcher

        self.kernel_manager = server_app.kernel_manager
        self.session_manager = server_app.session_manager
        self.contents_manager = server_app.contents_manager

        self.notebook_dir = os.path.realpath(
            getattr(server_app, "root_dir", os.path.expanduser("/workspace"))
        )

        # Lazy-init state for idle sweeper + file_id cleanup.
        self._initialized: bool = False
        self._init_lock: asyncio.Lock | None = None
        self._idle_sweeper_task: asyncio.Task | None = None
        # room_id → time.monotonic() of last MCP activity. Rooms not in the
        # map default to "now" on first observation, so a fresh room isn't
        # immediately swept.
        self._room_last_active: dict[str, float] = {}
        # Separate lock for room CREATION inside _resolve_notebook_doc.
        # Distinct from _notebook_locks (which serializes cell-level
        # mutations) because mutation methods hold the latter when they
        # call _resolve_notebook_doc — sharing one lock would deadlock.
        self._creation_locks: dict[str, asyncio.Lock] = {}

        try:
            self.idle_timeout_sec = int(
                os.environ.get(
                    "MCP_ROOM_IDLE_TIMEOUT_SEC", self.IDLE_TIMEOUT_DEFAULT_SEC
                )
            )
        except (TypeError, ValueError):
            self.idle_timeout_sec = self.IDLE_TIMEOUT_DEFAULT_SEC
        try:
            self.sweep_interval_sec = int(
                os.environ.get(
                    "MCP_ROOM_SWEEP_INTERVAL_SEC", self.SWEEP_INTERVAL_DEFAULT_SEC
                )
            )
        except (TypeError, ValueError):
            self.sweep_interval_sec = self.SWEEP_INTERVAL_DEFAULT_SEC

    # ── Path canonicalization (single-room invariant) ────────────────

    def _canonical_notebook_path(self, path: str) -> str:
        """Normalize a client-supplied path to the canonical workspace-
        relative form used as the file_id key.

        The JupyterLab contents-manager already normalizes UI-side paths
        the same way, so MCP + UI converge on the SAME file_id → SAME
        room_id for the SAME logical file. Rejects paths that escape the
        notebook root.

        Returns: path RELATIVE to ``self.notebook_dir`` (e.g.
        ``"foo.ipynb"``, ``"sub/bar.ipynb"``). Empty string is reserved
        for the workspace root and rejected.
        """
        if not isinstance(path, str) or not path:
            raise ValueError("notebook path must be a non-empty string")
        if os.path.isabs(path):
            candidate = path
        else:
            candidate = os.path.join(self.notebook_dir, path)
        # normpath collapses ``..`` segments before realpath resolves
        # symlinks; both are required for consistent canonicalization.
        candidate = os.path.realpath(os.path.normpath(candidate))
        root = self.notebook_dir
        if candidate == root or not candidate.startswith(root + os.sep):
            raise ValueError(
                f"path {path!r} resolves outside notebook root {root!r}"
            )
        return os.path.relpath(candidate, root)

    def _lock_for(self, path: str) -> asyncio.Lock:
        if path not in self._notebook_locks:
            self._notebook_locks[path] = asyncio.Lock()
        return self._notebook_locks[path]

    @property
    def ydoc_extension(self):
        """Lazily resolve the YDocExtension — it may load after us."""
        if self._ydoc_extension is None:
            ext_pkg = self.server_app.extension_manager.extensions.get(
                "jupyter_server_ydoc"
            )
            if ext_pkg is not None:
                for point in ext_pkg.extension_points.values():
                    if point.app is not None:
                        self._ydoc_extension = point.app
                        break
            if self._ydoc_extension is None:
                log.warning(
                    "jupyter_server_ydoc extension not available — "
                    "CRDT-based notebook tools will not work."
                )
        return self._ydoc_extension

    async def _ensure_initialized(self) -> None:
        """Lazy one-shot init: file_id_manager cleanup + idle-room sweeper.

        Idempotent and asyncio-safe via ``self._init_lock``. Called from the
        top of every public method that needs the room machinery, so the
        sweeper starts as soon as the first MCP request arrives.
        """
        if self._initialized:
            return
        if self._init_lock is None:
            self._init_lock = asyncio.Lock()
        async with self._init_lock:
            if self._initialized:
                return
            try:
                self._cleanup_file_id_manager()
            except Exception as e:  # never block startup on cleanup
                log.warning("file_id_manager cleanup failed: %s", e)
            try:
                if self._idle_sweeper_task is None:
                    self._idle_sweeper_task = asyncio.create_task(
                        self._idle_room_sweeper()
                    )
            except RuntimeError:
                # No running loop yet — sweeper will start lazily on next call.
                self._idle_sweeper_task = None
            self._initialized = True

    # ── file_id_manager cleanup (Task 7) ─────────────────────────────

    def _file_id_manager_db_path(self) -> str | None:
        """Best-effort lookup of the SQLite path used by file_id_manager.

        jupyter_server_fileid stores entries in
        ``<jupyter_data>/file_id_manager.db`` by default. We accept the
        upstream-default location only — anything custom is left untouched.
        """
        # Try the common location: <data-dir>/file_id_manager.db.
        try:
            from jupyter_core.paths import jupyter_data_dir
            candidate = os.path.join(jupyter_data_dir(), "file_id_manager.db")
            if os.path.exists(candidate):
                return candidate
        except Exception:
            pass
        return None

    def _cleanup_file_id_manager(self) -> dict[str, int]:
        """Delete file_id rows whose path is unreachable.

        Removes:
        * rows whose path is outside the notebook root (host-path leaks),
        * rows whose underlying file no longer exists AND whose room is
          not currently active in the in-memory ywebsocket_server.

        Idempotent. Logs a single summary line. Safe to run on every adapter
        startup; pruning rows can't race with active rooms because we exclude
        anything in ``server.rooms``.
        """
        result = {"host_path_leaks": 0, "orphaned_files": 0, "kept": 0}
        db_path = self._file_id_manager_db_path()
        if db_path is None:
            log.debug("file_id_manager DB not found; skipping cleanup")
            return result

        # Compute the set of room_ids currently active so we don't yank a
        # file_id out from under a live room.
        active_file_ids: set[str] = set()
        ext = self.ydoc_extension
        if ext is not None:
            try:
                server = ext.ywebsocket_server
                for room_id in list(getattr(server, "rooms", {}).keys()):
                    parts = room_id.split(":")
                    if len(parts) >= 3 and parts[1] == "notebook":
                        active_file_ids.add(parts[2])
            except Exception:
                pass

        root = self.notebook_dir
        try:
            conn = sqlite3.connect(db_path)
            try:
                rows = list(conn.execute("SELECT id, path FROM Files"))
                for fid, path in rows:
                    is_outside = not (
                        path == root or path.startswith(root + os.sep)
                    )
                    if is_outside:
                        conn.execute("DELETE FROM Files WHERE id=?", (fid,))
                        result["host_path_leaks"] += 1
                        continue
                    if not os.path.exists(path) and fid not in active_file_ids:
                        conn.execute("DELETE FROM Files WHERE id=?", (fid,))
                        result["orphaned_files"] += 1
                        continue
                    result["kept"] += 1
                conn.commit()
            finally:
                conn.close()
        except Exception as e:
            log.warning("file_id_manager cleanup failed: %s", e)
            return result

        log.info(
            "file_id_manager cleanup: removed %d host-path leaks, "
            "%d orphaned-file rows, kept %d rows",
            result["host_path_leaks"],
            result["orphaned_files"],
            result["kept"],
        )
        return result

    # ── Idle-room sweeper (Task 8) ───────────────────────────────────

    def _touch_room(self, room_id: str | None) -> None:
        """Mark ``room_id`` as active (last MCP activity = now)."""
        if room_id is not None:
            self._room_last_active[room_id] = time.monotonic()

    async def _idle_room_sweeper(self) -> None:
        """Periodically flush+close rooms with zero connected clients that
        have been idle for > ``self.idle_timeout_sec``.

        Activity is tracked via ``self._room_last_active``: every CRDT
        mutation through this adapter bumps the timestamp. A room with
        connected WebSocket clients is never reaped (its presence in the
        ``server.rooms`` registry indicates JupyterLab UI tabs may be
        watching).
        """
        try:
            while True:
                await asyncio.sleep(self.sweep_interval_sec)
                try:
                    await self._sweep_idle_rooms_once()
                except Exception as e:
                    log.exception("idle-room sweeper iteration failed: %s", e)
        except asyncio.CancelledError:
            log.debug("idle-room sweeper cancelled")
            raise

    async def _sweep_idle_rooms_once(self) -> int:
        """Single pass of the sweeper. Returns the number of rooms reaped.

        Public-ish for the eval test scenario that simulates a fast sweep.
        """
        ext = self.ydoc_extension
        if ext is None:
            return 0
        server = ext.ywebsocket_server
        now = time.monotonic()
        reaped = 0
        for room_id in list(getattr(server, "rooms", {}).keys()):
            room = server.rooms.get(room_id)
            if room is None:
                continue
            clients = getattr(room, "_clients", None) or []
            if clients:
                # Room has WS clients (UI tabs) — refresh activity timestamp
                # and keep alive.
                self._room_last_active[room_id] = now
                continue
            last = self._room_last_active.get(room_id, now)
            if now - last < self.idle_timeout_sec:
                continue
            log.info(
                "idle-room sweeper: flushing+closing room %s "
                "(idle %.1fs, no clients)",
                room_id,
                now - last,
            )
            try:
                save_task = room._save_to_disc()
                if save_task is not None:
                    await save_task
            except Exception as e:
                log.warning(
                    "idle-flush failed for %s: %s", room_id, e
                )
            try:
                await server.delete_room(name=room_id)
            except ValueError:
                pass  # benign upstream race
            self._room_last_active.pop(room_id, None)
            reaped += 1
        return reaped

    # ── Filesystem operations (do not touch CRDT rooms) ──────────────

    async def list_notebooks(self) -> list[dict[str, str]]:
        """List all .ipynb files accessible via the contents manager."""
        await self._ensure_initialized()
        model = self.contents_manager.get("", content=True, type="directory")
        if asyncio.iscoroutine(model):
            model = await model
        notebooks: list[dict[str, str]] = []
        await self._collect_notebooks(model, notebooks)
        return notebooks

    async def _collect_notebooks(self, model: dict, result: list[dict]) -> None:
        if model["type"] == "notebook":
            result.append({"path": model["path"], "name": model["name"]})
        elif model["type"] == "directory":
            content = model.get("content")
            if content is None:
                sub = self.contents_manager.get(
                    model["path"], content=True, type="directory"
                )
                if asyncio.iscoroutine(sub):
                    sub = await sub
                content = sub.get("content") if sub else None
            if content:
                for item in content:
                    await self._collect_notebooks(item, result)

    async def create_notebook(self, path: str) -> dict[str, str]:
        """Create a new empty notebook on disk. Does NOT open a CRDT room.

        ``path`` is canonicalized (rejects host paths and escapes).
        """
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        import nbformat

        nb = nbformat.v4.new_notebook()
        model = {"type": "notebook", "content": nb, "format": "json"}
        result = self.contents_manager.save(model, canonical)
        if asyncio.iscoroutine(result):
            result = await result
        return {"path": result["path"], "name": result["name"]}

    # ── Notebook-level operations (auto-attach to room) ──────────────

    async def get_notebook(self, path: str) -> dict[str, Any]:
        """Get full notebook content from the live CRDT document.

        Auto-attaches to any existing room or creates one.
        """
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        doc, room_id = await self._resolve_notebook_doc(canonical)
        self._touch_room(room_id)
        return doc.get()

    # ── Cell operations (auto-attach to room) ────────────────────────

    async def get_cell(self, path: str, index: int) -> dict[str, Any]:
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        doc, room_id = await self._resolve_notebook_doc(canonical)
        self._touch_room(room_id)
        if index < 0 or index >= doc.cell_number:
            raise IndexError(
                f"Cell index {index} out of range "
                f"(notebook has {doc.cell_number} cells)"
            )
        return doc.get_cell(index)

    async def set_cell(self, path: str, index: int, value: dict[str, Any]) -> None:
        """Update a cell in place — preserving its ``id`` and avoiding the
        phantom-cell residue produced by upstream ``YNotebook.set_cell``.

        Upstream ``set_cell(index, value)`` calls ``create_ycell(value)``
        (which mints a fresh UUID when ``"id"`` is absent from ``value``)
        and then ``set_ycell(index, ycell)`` which is
        ``self._ycells[index] = ycell`` — a ``pycrdt.Array.__setitem__``
        that decomposes into delete-then-insert at the CRDT level. Under
        any concurrent state this leaves residue (extra cells, lost cells).

        This method instead mutates the existing ``Y.Map``'s fields in place
        inside a single transaction, leaving the cell's identity and its
        position in the underlying ``Y.Array`` structurally untouched.

        Cell-type changes (markdown ↔ code) DO require a full Y.Map swap;
        we force-preserve the cell ``id`` and post-condition verify that
        the index/id alignment held.
        """
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        async with self._lock_for(canonical):
            doc, room_id = await self._resolve_notebook_doc(canonical)
            self._touch_room(room_id)
            if index < 0 or index >= doc.cell_number:
                raise IndexError(
                    f"Cell index {index} out of range "
                    f"(notebook has {doc.cell_number} cells)"
                )

            ycell = doc.ycells[index]
            old_id = ycell.get("id")
            old_type = ycell.get("cell_type")
            new_type = value.get("cell_type") or old_type

            new_source = value.get("source", "")
            if isinstance(new_source, list):
                new_source = "".join(new_source)

            ydoc = getattr(doc, "ydoc", None) or getattr(doc, "_ydoc", None)
            if ydoc is None:
                # Fallback: no transaction wrapper available — best-effort
                # in-place mutation without atomicity guarantee.
                self._inplace_mutate_cell(
                    doc, index, ycell, old_id, old_type, new_type,
                    value, new_source,
                )
            else:
                # pycrdt's Doc.transaction is a sync context manager that
                # batches CRDT ops into a single update.
                with ydoc.transaction():
                    self._inplace_mutate_cell(
                        doc, index, ycell, old_id, old_type, new_type,
                        value, new_source,
                    )

            # Post-condition: cell still at the requested index, id stable
            # for same-type updates. If a type change forced a Y.Map swap,
            # verify the new map carries the preserved id.
            try:
                actual = doc.ycells[index]
                actual_id = actual.get("id")
            except Exception:
                actual_id = None
            if actual_id != old_id:
                raise RuntimeError(
                    f"set_cell post-condition failed: id drift "
                    f"{old_id!r} → {actual_id!r} at index {index}"
                )

    @staticmethod
    def _inplace_mutate_cell(
        doc, index, ycell, old_id, old_type, new_type, value, new_source,
    ):
        """Apply the cell mutation. Caller wraps in a CRDT transaction."""
        if new_type and new_type != old_type:
            # Type-change branch: must replace the Y.Map. Force-preserve id.
            replacement = dict(value)
            replacement["id"] = old_id
            replacement["cell_type"] = new_type
            replacement["source"] = new_source
            ycell_new = doc.create_ycell(replacement)
            doc.ycells[index] = ycell_new
            return

        # Same-type branch: mutate fields in place. The Y.Map and its
        # position in _ycells stay structurally untouched.
        ysource = ycell["source"]
        if hasattr(ysource, "__delitem__") and hasattr(ysource, "__iadd__"):
            # Y.Text mutation
            del ysource[:]
            ysource += new_source
        else:
            # Plain attribute (rare); fall back to Y.Map.set
            ycell["source"] = new_source

        if "metadata" in value:
            md = ycell["metadata"]
            new_md = value.get("metadata") or {}
            if hasattr(md, "keys") and hasattr(md, "__delitem__"):
                for k in list(md.keys()):
                    del md[k]
                for k, v in new_md.items():
                    md[k] = v
            else:
                ycell["metadata"] = new_md

        if old_type == "code":
            if "outputs" in value:
                youts = ycell.get("outputs")
                if youts is not None and hasattr(youts, "__delitem__"):
                    while len(youts) > 0:
                        del youts[0]
                    for out in value["outputs"]:
                        youts.append(out)
                else:
                    ycell["outputs"] = value["outputs"]
            if "execution_count" in value:
                ycell["execution_count"] = value["execution_count"]

    async def insert_cell(
        self, path: str, index: int, source: str, cell_type: str = "code"
    ) -> None:
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        async with self._lock_for(canonical):
            doc, room_id = await self._resolve_notebook_doc(canonical)
            self._touch_room(room_id)
            cell_value = self._make_cell(source, cell_type)
            ycell = doc.create_ycell(cell_value)
            doc.ycells.insert(index, ycell)

    async def append_cell(
        self, path: str, source: str, cell_type: str = "code"
    ) -> None:
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        async with self._lock_for(canonical):
            doc, room_id = await self._resolve_notebook_doc(canonical)
            self._touch_room(room_id)
            cell_value = self._make_cell(source, cell_type)
            doc.append_cell(cell_value)

    async def delete_cell(self, path: str, index: int) -> None:
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        async with self._lock_for(canonical):
            doc, room_id = await self._resolve_notebook_doc(canonical)
            self._touch_room(room_id)
            if index < 0 or index >= doc.cell_number:
                raise IndexError(
                    f"Cell index {index} out of range "
                    f"(notebook has {doc.cell_number} cells)"
                )
            doc.ycells.pop(index)

    # ── Cell execution ───────────────────────────────────────────────

    async def execute_cell(self, path: str, index: int) -> list[dict[str, Any]]:
        """Execute a cell via the Jupyter kernel and return outputs.

        Outputs are written back via the in-place ``set_cell`` path so they
        persist to disk on the next room save.
        """
        from nbformat.v4 import output_from_msg

        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        cell = await self.get_cell(canonical, index)
        source = cell.get("source", "")
        if not source.strip():
            return []

        kernel_id = await self._ensure_kernel(canonical)
        km = self.kernel_manager.get_kernel(kernel_id)
        client = km.client()
        client.start_channels()

        try:
            await asyncio.wait_for(client.wait_for_ready(), timeout=30)

            msg_id = client.execute(source)
            outputs: list[dict[str, Any]] = []
            iopub_msgs: list[dict[str, Any]] = []
            execution_count: int | None = None

            async def _read_shell_reply() -> None:
                nonlocal execution_count
                try:
                    while True:
                        reply = await asyncio.wait_for(
                            asyncio.ensure_future(client.get_shell_msg()),
                            timeout=120,
                        )
                        if reply["parent_header"].get("msg_id") != msg_id:
                            continue
                        if reply["msg_type"] == "execute_reply":
                            execution_count = reply["content"].get("execution_count")
                            return
                except asyncio.TimeoutError:
                    return

            shell_task = asyncio.ensure_future(_read_shell_reply())

            try:
                while True:
                    try:
                        msg = await asyncio.wait_for(
                            asyncio.ensure_future(client.get_iopub_msg()),
                            timeout=120,
                        )
                    except asyncio.TimeoutError:
                        outputs.append(
                            {
                                "type": "error",
                                "content": {
                                    "ename": "Timeout",
                                    "evalue": "Cell execution timed out",
                                },
                            }
                        )
                        break

                    if msg["parent_header"].get("msg_id") != msg_id:
                        continue

                    msg_type = msg["msg_type"]
                    if msg_type in ("stream", "display_data", "execute_result", "error"):
                        outputs.append({"type": msg_type, "content": msg["content"]})
                        iopub_msgs.append(msg)
                        if msg_type == "execute_result" and execution_count is None:
                            execution_count = msg["content"].get("execution_count")
                    elif (
                        msg_type == "status"
                        and msg["content"]["execution_state"] == "idle"
                    ):
                        break
            finally:
                try:
                    await asyncio.wait_for(shell_task, timeout=5)
                except (asyncio.TimeoutError, asyncio.CancelledError):
                    shell_task.cancel()

            try:
                nbf_outputs = [output_from_msg(m) for m in iopub_msgs]
            except Exception:
                nbf_outputs = []

            try:
                latest = await self.get_cell(canonical, index)
                if latest.get("cell_type") == "code":
                    latest["outputs"] = nbf_outputs
                    if execution_count is not None:
                        latest["execution_count"] = execution_count
                    await self.set_cell(canonical, index, latest)
            except IndexError:
                pass

            return outputs
        finally:
            client.stop_channels()

    # ── Awareness / diagnostics (read-only) ──────────────────────────

    async def list_notebook_users(self, path: str) -> list[dict[str, str]]:
        """List awareness users currently connected to ONE notebook's room.

        Returns the awareness state of every collaborator on ``path``. If
        no room exists yet for ``path``, returns an empty list (does NOT
        auto-create — this is a read-only diagnostic).
        """
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        if self.ydoc_extension is None:
            return []
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        if file_id_manager is None:
            return []
        try:
            file_id = file_id_manager.index(canonical)
        except Exception:
            return []
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )
        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )
        room = server.rooms.get(room_id)
        if room is None:
            return []
        users: list[dict[str, str]] = []
        try:
            ydoc = getattr(room, "ydoc", None) or getattr(room, "_document", None)
            awareness = (
                getattr(ydoc, "awareness", None) if ydoc is not None else None
            )
            if awareness is not None:
                for client_id, state in awareness.states.items():
                    user = state.get("user", {}) if isinstance(state, dict) else {}
                    name = user.get("name") if isinstance(user, dict) else None
                    users.append({"id": str(client_id), "name": name or str(client_id)})
        except Exception:
            pass
        return users

    async def list_rooms(self) -> list[dict[str, Any]]:
        """List every active CRDT room with full metadata. Read-only.

        Returns one entry per room with: room_id, path (reverse-resolved),
        file_id, users (awareness), user_count, has_kernel.
        """
        await self._ensure_initialized()
        if self.ydoc_extension is None:
            return []
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        result: list[dict[str, Any]] = []
        for room_id in list(getattr(server, "rooms", {}).keys()):
            entry = await self._room_metadata(room_id, file_id_manager)
            result.append(entry)
        return result

    # ── Change watching ──────────────────────────────────────────────

    async def watch_notebook(
        self, path: str, timeout: float = 30.0
    ) -> dict[str, Any]:
        """Block until a CRDT change occurs on the notebook, or timeout.

        Auto-attaches to any existing room or creates one.
        """
        await self._ensure_initialized()
        canonical = self._canonical_notebook_path(path)
        doc, room_id = await self._resolve_notebook_doc(canonical)
        self._touch_room(room_id)
        watcher = self._ensure_watcher(canonical, doc)
        event = watcher.add_waiter()
        try:
            await asyncio.wait_for(event.wait(), timeout=timeout)
            return {"changed": True, "cell_count": doc.cell_number}
        except asyncio.TimeoutError:
            return {"changed": False}
        finally:
            watcher.remove_waiter(event)

    def _ensure_watcher(self, path: str, doc) -> NotebookWatcher:
        if path not in self._watchers:
            watcher = NotebookWatcher()
            self._watchers[path] = watcher

            def on_change(event_name, changes):
                watcher.notify_all()

            doc.observe(on_change)
            log.debug("Attached CRDT observer for %s", path)
        return self._watchers[path]

    def _detach_watcher(self, path: str) -> None:
        if path in self._watchers:
            del self._watchers[path]
            log.debug("Detached CRDT observer for %s", path)

    # ── Auto-attach: the single resolve point ────────────────────────

    async def _resolve_notebook_doc(self, canonical: str):
        """Return (doc, room_id) for ``canonical`` (already-canonical path).

        Auto-attaches to the existing room if one exists (UI tab, another
        MCP session, this one) — otherwise creates one. The single-room
        invariant is enforced by the deterministic ``room_id =
        json:notebook:<file_id_manager.index(canonical)>`` mapping.
        """
        if self.ydoc_extension is None:
            raise RuntimeError(
                "jupyter_server_ydoc extension not available — "
                "cannot create CRDT room."
            )
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        if file_id_manager is None:
            raise RuntimeError("file_id_manager not available")
        file_id = file_id_manager.index(canonical)
        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )

        # Fast path: room already exists, fetch its document.
        doc = await self._get_notebook_doc(canonical)
        if doc is not None:
            return doc, room_id

        # Slow path: create the room. Use a SEPARATE creation lock —
        # NOT _notebook_locks — because cell-mutation methods
        # (set_cell, insert_cell, delete_cell, append_cell) hold
        # _notebook_locks[canonical] when they call us. Sharing one
        # lock would deadlock on the cold-room path.
        if canonical not in self._creation_locks:
            self._creation_locks[canonical] = asyncio.Lock()
        async with self._creation_locks[canonical]:
            doc = await self._get_notebook_doc(canonical)
            if doc is not None:
                return doc, room_id
            await self._create_room(canonical)
            await self._push_mcp_awareness(canonical)
            doc = await self._get_notebook_doc(canonical)
            if doc is None:
                raise RuntimeError(
                    f"failed to create CRDT room for {canonical!r}"
                )
            return doc, room_id

    async def _get_notebook_doc(self, path: str):
        """Get the live YNotebook CRDT document, or None if no room exists."""
        if self.ydoc_extension is None:
            return None
        try:
            doc = await self.ydoc_extension.get_document(
                path=path,
                content_type="notebook",
                file_format="json",
                copy=False,
            )
            return doc
        except Exception as e:
            log.debug("Could not get CRDT document for %s: %s", path, e)
            return None

    async def _create_room(self, path: str) -> None:
        """Create a CRDT DocumentRoom for a notebook, mirroring
        ``YDocWebSocketHandler.prepare()`` from jupyter_server_ydoc.

        The deterministic ``room_id`` (derived from
        ``file_id_manager.index(path)``) ensures MCP-driven and UI-driven
        opens converge on the same ``server.rooms[room_id]`` entry.
        """
        from jupyter_server_ydoc.rooms import DocumentRoom
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )

        ext = self.ydoc_extension
        server = ext.ywebsocket_server

        if not server.started.is_set():
            asyncio.ensure_future(server.start())
            await server.started.wait()

        file_id_manager = self.server_app.web_app.settings["file_id_manager"]
        file_id = file_id_manager.index(path)

        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )

        if server.room_exists(room_id):
            return

        # Sweep stale orphans for this path before creating: guards against
        # rare cases where file_id rotates (rename + recreate).
        await self._sweep_stale_rooms_for_path(path)

        def _exception_logger(exception, logger):
            logger.error(
                "Document Room Exception (room_id=%s): ", room_id, exc_info=exception
            )
            return True

        file_loader = ext.file_loaders[file_id]
        ystore_class = ext.ystore_class
        updates_file_path = f".notebook:{file_id}.y"
        ystore = ystore_class(path=updates_file_path, log=log)

        room = DocumentRoom(
            room_id,
            file_format="json",
            file_type="notebook",
            file=file_loader,
            logger=ext.serverapp.event_logger,
            ystore=ystore,
            log=log,
            save_delay=ext.document_save_delay,
            exception_handler=_exception_logger,
        )

        await server.start_room(room)
        await room.initialize()
        server.add_room(room_id, room)
        # Initialize last-active so the sweeper doesn't immediately reap a
        # freshly-created MCP-driven room.
        self._room_last_active[room_id] = time.monotonic()
        log.info("Created CRDT room: %s", room_id)

    # ── Internal helpers ─────────────────────────────────────────────

    def _room_id_to_path(self, room_id: str, file_id_manager) -> str | None:
        """Reverse-map ``json:notebook:<file_id>`` → notebook path, or None."""
        if file_id_manager is None:
            return None
        parts = room_id.split(":")
        if len(parts) < 3 or parts[1] != "notebook":
            return None
        try:
            return file_id_manager.get_path(parts[2])
        except Exception:
            return None

    async def _room_metadata(
        self, room_id: str, file_id_manager
    ) -> dict[str, Any]:
        """Build the rich-metadata entry for one room (used by list_rooms)."""
        server = self.ydoc_extension.ywebsocket_server
        room = server.rooms.get(room_id)

        parts = room_id.split(":")
        file_id = parts[2] if len(parts) >= 3 else None
        path = self._room_id_to_path(room_id, file_id_manager)

        users: list[str] = []
        if room is not None:
            try:
                ydoc = getattr(room, "ydoc", None) or getattr(room, "_document", None)
                awareness = (
                    getattr(ydoc, "awareness", None) if ydoc is not None else None
                )
                if awareness is not None:
                    states = awareness.states
                    for client_id, state in states.items():
                        user = state.get("user", {}) if isinstance(state, dict) else {}
                        name = user.get("name") if isinstance(user, dict) else None
                        users.append(name or str(client_id))
            except Exception:
                pass

        has_kernel = False
        if path is not None:
            try:
                exists = self.session_manager.session_exists(path=path)
                if asyncio.iscoroutine(exists):
                    exists = await exists
                has_kernel = bool(exists)
            except Exception:
                pass

        return {
            "room_id": room_id,
            "path": path,
            "file_id": file_id,
            "users": users,
            "user_count": len(users),
            "has_kernel": has_kernel,
        }

    async def _sweep_stale_rooms_for_path(self, path: str) -> None:
        """Close rooms whose reverse-mapped path equals ``path`` but whose
        ``room_id`` differs from the current ``file_id_manager.index(path)``.

        Guards against orphans left behind when a file is renamed or
        recreated and ``file_id_manager`` rotates the file_id. Defensive —
        with canonicalization in place, this should never have to fire.
        """
        if self.ydoc_extension is None:
            return
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        if file_id_manager is None:
            return
        try:
            current_file_id = file_id_manager.index(path)
        except Exception:
            return
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )

        current_room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", current_file_id)
        )

        stale_ids: list[str] = []
        for rid in list(server.rooms.keys()):
            if rid == current_room_id:
                continue
            mapped_path = self._room_id_to_path(rid, file_id_manager)
            if mapped_path == path:
                stale_ids.append(rid)

        for stale_id in stale_ids:
            log.warning(
                "Sweeping stale room %s for path %s before opening current room %s",
                stale_id,
                path,
                current_room_id,
            )
            try:
                room = server.rooms.get(stale_id)
                if room is not None:
                    try:
                        save_task = room._save_to_disc()
                        if save_task is not None:
                            await save_task
                    except Exception:
                        pass
                try:
                    await server.delete_room(name=stale_id)
                except ValueError:
                    pass
                self._room_last_active.pop(stale_id, None)
            except Exception as e:
                log.warning("Failed to sweep stale room %s: %s", stale_id, e)

    async def _push_mcp_awareness(self, path: str) -> None:
        """Push an awareness presence entry identifying the MCP client.

        Surfaces the MCP as a collaborator in JupyterLab's standard
        collaboration sidebar so a human user opening the notebook in
        the UI sees the join.
        """
        try:
            doc = await self._get_notebook_doc(path)
            if doc is None:
                return
            ydoc = getattr(doc, "_ydoc", None) or getattr(doc, "ydoc", None)
            awareness = getattr(ydoc, "awareness", None) if ydoc is not None else None
            if awareness is None:
                return
            awareness.set_local_state(
                {
                    "user": {
                        "name": "Claude (MCP)",
                        "color": "#7C3AED",
                    }
                }
            )
        except Exception as e:
            log.debug("Could not push MCP awareness for %s: %s", path, e)

    async def _ensure_kernel(self, path: str) -> str:
        exists = self.session_manager.session_exists(path=path)
        if asyncio.iscoroutine(exists):
            exists = await exists
        if exists:
            session = self.session_manager.get_session(path=path)
            if asyncio.iscoroutine(session):
                session = await session
            return session["kernel"]["id"]
        kernel_id = self.kernel_manager.start_kernel(path=path)
        if asyncio.iscoroutine(kernel_id):
            kernel_id = await kernel_id
        result = self.session_manager.create_session(
            path=path, type="notebook", kernel_id=kernel_id
        )
        if asyncio.iscoroutine(result):
            await result
        return kernel_id

    @staticmethod
    def _make_cell(source: str, cell_type: str = "code") -> dict[str, Any]:
        cell: dict[str, Any] = {
            "cell_type": cell_type,
            "source": source,
            "metadata": {},
        }
        if cell_type == "code":
            cell["outputs"] = []
            cell["execution_count"] = None
        return cell
