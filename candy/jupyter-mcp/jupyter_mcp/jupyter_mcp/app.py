"""Jupyter Server Extension that registers the MCP endpoint at /mcp."""

import asyncio
import logging

from .tornado_asgi import TornadoASGIHandler

log = logging.getLogger(__name__)


class MCPExtensionApp:
    """Lightweight extension that registers /mcp with the Jupyter Server.

    Uses the _load_jupyter_server_extension() pattern. The YDocExtension
    is resolved lazily (at tool call time) to avoid load-order dependencies
    — our extension may load before jupyter_server_ydoc.
    """

    def initialize(self, server_app):
        self.server_app = server_app

        # Build the FastMCP server with tools.
        # The RTCAdapter resolves YDocExtension lazily at call time.
        from .mcp_server import create_mcp_server
        from .rtc_adapter import RTCAdapter

        adapter = RTCAdapter(server_app)
        mcp = create_mcp_server(adapter)

        # Get the ASGI app from FastMCP — path="/" because the Tornado handler
        # rewrites the path to "/" before forwarding to the ASGI app.
        asgi_app = mcp.http_app(path="/")

        # Start the ASGI lifespan (initializes FastMCP's session manager).
        # This is needed because we're calling the ASGI app from Tornado —
        # Starlette's lifespan events don't fire automatically.
        loop = asyncio.get_event_loop()
        loop.create_task(self._start_asgi_lifespan(asgi_app))

        # Register the Tornado handler at /mcp
        handlers = [
            (r"/mcp", TornadoASGIHandler, {"asgi_app": asgi_app}),
        ]
        server_app.web_app.add_handlers(".*$", handlers)
        log.info("jupyter-mcp: registered MCP endpoint at /mcp")

    @staticmethod
    async def _start_asgi_lifespan(asgi_app):
        """Send ASGI lifespan.startup to the app to initialize the session manager."""
        startup_complete = asyncio.Event()
        shutdown_trigger = asyncio.Event()
        sent_startup = False

        async def receive():
            nonlocal sent_startup
            if not sent_startup:
                sent_startup = True
                return {"type": "lifespan.startup"}
            # Block until shutdown is requested (keeps lifespan alive)
            await shutdown_trigger.wait()
            return {"type": "lifespan.shutdown"}

        async def send(message):
            msg_type = message.get("type", "")
            if msg_type == "lifespan.startup.complete":
                log.info("jupyter-mcp: ASGI lifespan started")
                startup_complete.set()
            elif msg_type == "lifespan.startup.failed":
                log.error(
                    "jupyter-mcp: ASGI lifespan startup failed: %s",
                    message.get("message", ""),
                )
                startup_complete.set()

        scope = {"type": "lifespan", "asgi": {"version": "3.0"}}
        try:
            await asgi_app(scope, receive, send)
        except Exception:
            log.exception("jupyter-mcp: ASGI lifespan error")
