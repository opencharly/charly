"""Tornado-ASGI bridge handler for mounting FastMCP's Starlette app inside Jupyter Server."""

import asyncio

from jupyter_server.base.handlers import JupyterHandler


class TornadoASGIHandler(JupyterHandler):
    """Bridges Tornado HTTP requests to a Starlette ASGI application.

    Both Tornado and Starlette run on the same asyncio event loop inside
    Jupyter Server, so we can call the ASGI app directly — no threads,
    no subprocess, no extra ports.

    Supports SSE streaming (text/event-stream) by disabling auto-finish
    and flushing chunks incrementally.
    """

    def initialize(self, asgi_app):
        self.asgi_app = asgi_app

    def check_xsrf_cookie(self):
        # MCP clients are not browsers — XSRF protection must be disabled
        pass

    async def prepare(self):
        # JupyterHandler.prepare() handles authentication (token validation).
        # It may or may not be a coroutine depending on the version.
        result = super().prepare()
        if result is not None:
            await result
        # Prevent Tornado from auto-finishing the response — required for
        # SSE streams where we send multiple chunks before finishing.
        self._auto_finish = False

    async def _handle(self):
        """Translate the Tornado request into ASGI scope/receive/send and call the app."""
        # Build ASGI HTTP scope.
        # path="/" because FastMCP's http_app(path="/") expects root-mounted requests.
        # Tornado's headers.items() comma-joins multi-value headers (HTTP-standard).
        scope = {
            "type": "http",
            "asgi": {"version": "3.0"},
            "http_version": "1.1",
            "method": self.request.method,
            "path": "/",
            "query_string": (self.request.query or "").encode("latin-1"),
            "headers": [
                (k.lower().encode("latin-1"), v.encode("latin-1"))
                for k, v in self.request.headers.items()
            ],
            "server": (self.request.host.split(":")[0], 8888),
        }

        # ASGI receive callable — delivers the request body once, then blocks
        # until the client disconnects (signalled by on_connection_close).
        self._disconnected = asyncio.Event()
        body_sent = False

        async def receive():
            nonlocal body_sent
            if not body_sent:
                body_sent = True
                return {
                    "type": "http.request",
                    "body": self.request.body or b"",
                    "more_body": False,
                }
            # Block until the client disconnects
            await self._disconnected.wait()
            return {"type": "http.disconnect"}

        # ASGI send callable — streams ASGI response events back through Tornado.
        async def send(message):
            if self._finished:
                return
            msg_type = message["type"]
            if msg_type == "http.response.start":
                self.set_status(message["status"])
                for name, value in message.get("headers", []):
                    header_name = name.decode("latin-1")
                    header_value = value.decode("latin-1")
                    # Skip Content-Length — Tornado manages it for streaming responses
                    if header_name.lower() == "content-length":
                        continue
                    self.set_header(header_name, header_value)
            elif msg_type == "http.response.body":
                body = message.get("body", b"")
                if body:
                    self.write(body)
                if message.get("more_body", False):
                    # Flush chunk for SSE streaming
                    await self.flush()
                else:
                    # Final chunk — finish the response
                    self.finish()

        try:
            await self.asgi_app(scope, receive, send)
        except Exception:
            if not self._finished:
                self.set_status(500)
                self.finish("Internal Server Error")

    # Route all HTTP methods through the ASGI bridge
    post = get = delete = _handle

    def on_connection_close(self):
        """Signal the ASGI app that the client disconnected (stops SSE streams)."""
        if hasattr(self, "_disconnected"):
            self._disconnected.set()
