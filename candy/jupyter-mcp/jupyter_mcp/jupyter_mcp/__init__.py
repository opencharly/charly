"""jupyter-mcp: MCP server extension for JupyterLab with real-time collaboration."""

__version__ = "0.1.0"


def _load_jupyter_server_extension(server_app):
    """Register the MCP extension with Jupyter Server."""
    from .app import MCPExtensionApp

    ext = MCPExtensionApp()
    ext.initialize(server_app)
