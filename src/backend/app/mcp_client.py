"""
MCP Client - Manages stdio connection to the Go MCP server (always started)
"""
import asyncio
import logging
import os
import sys
from contextlib import asynccontextmanager
from typing import AsyncGenerator

from mcp.client.session import ClientSession
from mcp.client.stdio import StdioServerParameters, stdio_client

logger = logging.getLogger(__name__)

def print_debug(msg):
    """Print with flush for immediate output visibility."""
    print(f"[MCP] {msg}", flush=True)
    sys.stdout.flush()
    logger.info(msg)

# MCP configuration from environment.
# When true (default), the backend calls MCP tools (Confluence, documents, forms,
# skills, processes) alongside the RAG store. When false, MCP search is disabled
# entirely and only the RAG store is queried — the Go MCP subprocess is not spawned.
MCP_ENABLED = os.getenv("MCP_ENABLED", "true").lower() == "true"

def _clean_env_value(value: str | None) -> str | None:
    """Strip whitespace, accidental surrounding quotes, and embedded control
    characters from an env var. Windows users commonly quote .env values that
    contain spaces/backslashes (e.g. MCP_SERVER_PATH="C:\\Program Files\\..."),
    and paths pasted from Windows Explorer/clipboard can carry a stray embedded
    \r or other control character that isn't just leading/trailing whitespace —
    any of these would make the path unresolvable (or silently truncate it)."""
    if value is None:
        return None
    # Drop ASCII control chars (0x00-0x1F, 0x7F) anywhere in the string, not just the ends
    value = "".join(ch for ch in value if ord(ch) >= 0x20 and ch != "\x7f").strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
        value = value[1:-1].strip()
    return value or None

class MCPClientManager:
    """Manages MCP server subprocess lifecycle and provides tool calling.
    Starts MCP server on initialization (not on-demand)."""

    def __init__(self, mcp_server_path: str | None = None, work_dir: str | None = None):
        print_debug("MCPClientManager.__init__() called")

        # Resolve work_dir — the single source of truth
        # 1. Use provided work_dir if given
        # 2. Use MCP_WORK_DIR env var if set and absolute
        # 3. Fall back: go up 3 levels from this file (app/mcp_client.py → src → backend → src → project_root)
        if work_dir:
            self.work_dir = os.path.abspath(work_dir)
        else:
            work_dir_env = _clean_env_value(os.getenv("MCP_WORK_DIR"))
            if work_dir_env and os.path.isabs(work_dir_env):
                self.work_dir = os.path.abspath(work_dir_env)
            else:
                # Go up 3 levels: app/ → src/ → backend/ → project root
                self.work_dir = os.path.normpath(
                    os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..")
                )

        print_debug(f"Work directory: {self.work_dir}")

        # Resolve mcp_server_path — always relative to work_dir
        # 1. Use provided mcp_server_path if given
        # 2. Use MCP_SERVER_PATH env var if set and absolute
        # 3. Fall back: <work_dir>/mcp-server/geniq-mcp-server
        raw_env_value = os.getenv("MCP_SERVER_PATH")
        print_debug(f"MCP_SERVER_PATH raw env value: {raw_env_value!r}")
        if mcp_server_path:
            self.mcp_server_path = mcp_server_path
        else:
            self.mcp_server_path = _clean_env_value(raw_env_value) or "geniq-mcp-server"

        if not os.path.isabs(self.mcp_server_path):
            # Relative path — resolve relative to work_dir
            self.mcp_server_path = os.path.join(self.work_dir, "mcp-server", self.mcp_server_path)

        self.mcp_server_path = os.path.abspath(self.mcp_server_path)

        # On Windows the built binary carries a .exe suffix — auto-append it
        # if the configured path has no extension and doesn't exist as-is,
        # so MCP_SERVER_PATH=geniq-mcp-server keeps working there too.
        if os.name == "nt" and not os.path.exists(self.mcp_server_path):
            root, ext = os.path.splitext(self.mcp_server_path)
            if not ext and os.path.exists(root + ".exe"):
                self.mcp_server_path = root + ".exe"

        print_debug(f"MCP server path resolved: {self.mcp_server_path!r}")
        print_debug(f"MCP server path exists on disk: {os.path.exists(self.mcp_server_path)}")

        # Context managers for stdio_client and ClientSession
        self._streams_context = None
        self._session_context = None
        self._session: ClientSession | None = None
        self._initialized = False
        self._ready = False  # MCP server is ready to accept requests

    async def initialize(self) -> None:
        """Start the MCP server subprocess and initialize the session."""
        if self._initialized:
            print_debug("MCP server already initialized")
            return

        print_debug("Starting MCP server subprocess...")
        print_debug(f"  Command: {self.mcp_server_path}")
        print_debug(f"  Args: -work-dir {self.work_dir}")

        # Pass work_dir as -work-dir flag. No more env vars for document paths —
        # they're all relative to the work_dir the Go server chdirs into.
        params = StdioServerParameters(
            command=self.mcp_server_path,
            args=["-work-dir", self.work_dir],
            env=os.environ,
            cwd=self.work_dir,
        )

        try:
            print_debug("Creating stdio_client...")
            self._streams_context = stdio_client(params)

            print_debug("Entering stdio_client context...")
            streams = await self._streams_context.__aenter__()
            read_stream, write_stream = streams
            print_debug("stdio_client context entered successfully")

            print_debug("Creating ClientSession...")
            self._session_context = ClientSession(read_stream, write_stream)

            print_debug("Entering ClientSession context...")
            await self._session_context.__aenter__()
            self._session = self._session_context
            print_debug("ClientSession context entered successfully")

            print_debug("Sending initialize request to MCP server...")
            await self._session.initialize()
            self._initialized = True
            print_debug("MCP session initialized (handshake complete)")

            # Wait a moment for the MCP server to fully register tools
            print_debug("Waiting 2 seconds for MCP server to register tools...")
            await asyncio.sleep(2)

            # Verify tools are available
            print_debug("Listing available MCP tools...")
            result = await self._session.list_tools()
            tool_names = [tool.name for tool in result.tools]
            print_debug(f"Available MCP tools ({len(result.tools)} total): {tool_names}")

            self._ready = True
            print_debug("MCP server ready and available!")
        except Exception as e:
            print_debug(f"FAILED: {type(e).__name__}: {e}")
            logger.error(f"Failed to start MCP server: {type(e).__name__}: {e}", exc_info=True)
            await self._cleanup()
            raise RuntimeError(f"Failed to start MCP server: {type(e).__name__}: {e}")

    async def _cleanup(self) -> None:
        """Clean up MCP resources."""
        if self._session_context:
            await self._session_context.__aexit__(None, None, None)
            self._session_context = None

        if self._streams_context:
            await self._streams_context.__aexit__(None, None, None)
            self._streams_context = None

        self._session = None
        self._initialized = False
        self._ready = False

    async def get_session(self) -> ClientSession:
        """Get session - must be initialized on startup."""
        if not self._initialized:
            await self.initialize()
        if not self._ready:
            raise RuntimeError("MCP server not ready - not initialized or failed to start")
        return self._session

    async def call_tool(self, tool_name: str, arguments: dict | None = None) -> str:
        """Call a tool on the MCP server."""
        if not self._ready:
            await self.initialize()

        session = await self.get_session()

        logger.debug(f"Calling MCP tool: {tool_name} with args: {arguments}")
        result = await session.call_tool(tool_name, arguments or {})

        # Extract text content from the result
        content = []
        for item in result.content:
            if hasattr(item, "text"):
                content.append(item.text)
            elif hasattr(item, "data"):
                content.append(str(item.data))
            else:
                content.append(str(item))

        result_text = "\n".join(content)
        logger.debug(f"MCP tool {tool_name} result: {result_text[:200]}...")
        return result_text

    async def list_tools(self) -> list[dict]:
        """List all available tools on the MCP server."""
        if not self._ready:
            await self.initialize()

        session = await self.get_session()
        result = await session.list_tools()
        return result.tools

    async def shutdown(self) -> None:
        """Shut down the MCP server subprocess and session."""
        if not self._initialized:
            return
        print_debug("Shutting down MCP session...")
        await self._cleanup()
        print_debug("MCP session shut down")


# Singleton instance
_mcp_manager: MCPClientManager | None = None


def get_mcp_manager() -> MCPClientManager:
    """Get the singleton MCP client manager."""
    global _mcp_manager
    if _mcp_manager is None:
        _mcp_manager = MCPClientManager()
    return _mcp_manager


@asynccontextmanager
async def mcp_session() -> AsyncGenerator[ClientSession, None]:
    """Context manager for MCP session. Initializes on first use."""
    manager = get_mcp_manager()
    session = await manager.get_session()
    yield session
