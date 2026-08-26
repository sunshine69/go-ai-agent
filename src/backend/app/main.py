"""
GenIQ Backend - Main Application
"""

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from dotenv import load_dotenv
import os

# Load .env file from backend directory (MCP_SERVER_PATH, LLM_*, etc.)
# __file__ is at <project>/src/backend/app/main.py
# Go up 2 levels: app → backend
env_path = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), ".env")
# utf-8-sig strips a leading BOM (Notepad's default "UTF-8" save on Windows),
# which otherwise corrupts the first key in the file (e.g. "\ufeffMCP_SERVER_PATH")
# and makes os.getenv() silently miss it.
load_dotenv(env_path, encoding="utf-8-sig")

# Env vars whose value should be masked when dumped to the startup log
_SENSITIVE_MARKERS = ("KEY", "TOKEN", "SECRET", "PASSWORD", "PWD", "CREDENTIAL")
# Only these prefixes/names are dumped — avoids leaking unrelated host env vars (PATH, HOME, etc.)
_DEBUG_ENV_PREFIXES = ("MCP_", "LLM_", "CONFLUENCE_", "RAG_", "SUPABASE_", "DOCUMENTS_", "SKILLS_", "PROC_", "APP_")
_DEBUG_ENV_NAMES = ("HOST", "PORT", "DEBUG")


def _mask_env_value(key: str, value: str) -> str:
    if not value or not any(marker in key.upper() for marker in _SENSITIVE_MARKERS):
        return value
    if len(value) <= 8:
        return "*" * len(value)
    return f"{value[:2]}{'*' * (len(value) - 4)}{value[-2:]}"


def _dump_env_debug() -> None:
    """Print resolved env vars (secrets masked) so Windows .env-loading issues are visible in the startup log.
    Values are printed with repr() — a stray embedded \r/\n or other control/invisible
    character (common when a path is pasted from Windows Explorer) would otherwise move
    the terminal cursor and visually truncate/overwrite the line instead of showing up."""
    print("=" * 60, flush=True)
    print(f"[ENV DEBUG] .env path checked: {env_path}", flush=True)
    print(f"[ENV DEBUG] .env file exists: {os.path.isfile(env_path)}", flush=True)
    for key in sorted(os.environ):
        if key.startswith(_DEBUG_ENV_PREFIXES) or key in _DEBUG_ENV_NAMES:
            print(f"[ENV DEBUG] {key} = {_mask_env_value(key, os.environ[key])!r}", flush=True)
    print("=" * 60, flush=True)


_dump_env_debug()

# Import routers
from app.routers import auth, conversations, messages, documents, forms, skills, processes, confluence, rag, domains, pwa

app = FastAPI(
    title="GenIQ",
    description="AI-Powered Knowledge Assistant",
    version="1.0.0",
)

# CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Include routers
app.include_router(auth.router, prefix="/api/auth", tags=["Authentication"])
app.include_router(conversations.router, prefix="/api/conversations", tags=["Conversations"])
app.include_router(messages.router, prefix="/api/messages", tags=["Messages"])
app.include_router(domains.router, prefix="/api/domains", tags=["Domains"])
app.include_router(documents.router, prefix="/api/documents", tags=["Documents"])
app.include_router(forms.router, prefix="/api/forms", tags=["Forms"])
app.include_router(skills.router, prefix="/api/skills", tags=["Skills"])
app.include_router(processes.router, prefix="/api/processes", tags=["Processes"])
app.include_router(confluence.router, prefix="/api/confluence", tags=["Confluence"])
app.include_router(rag.router, prefix="/api", tags=["RAG"])
app.include_router(pwa.router, prefix="/pwa", tags=["PWA"])

@app.get("/health")
async def health_check():
    from app.mcp_client import get_mcp_manager
    manager = get_mcp_manager()
    ready = manager._ready if manager._initialized else False
    return {"status": "ok", "mcp_ready": ready}

@app.on_event("startup")
async def startup_event():
    """Initialize MCP server on backend startup (unless MCP search is disabled)."""
    from app.mcp_client import get_mcp_manager, MCP_ENABLED
    logger = __import__('logging').getLogger(__name__)

    logger.info("=" * 60, flush=True)
    logger.info("GenIQ Backend Starting Up", flush=True)
    logger.info("=" * 60, flush=True)

    if not MCP_ENABLED:
        logger.warning(
            "MCP search is disabled (MCP_ENABLED=false) — skipping Go MCP server "
            "startup. Only RAG search will be used.",
        )
        return

    try:
        manager = get_mcp_manager()
        await manager.initialize()
        logger.info("MCP server initialized successfully on startup!", flush=True)
        logger.info("=" * 60, flush=True)
    except Exception as e:
        logger.error(f"Failed to initialize MCP server on startup: {e}", exc_info=True)
        # Don't fail the whole backend - allow requests but log the error
        logger.warning("Continuing without MCP server - tools will fail until MCP is started")

@app.on_event("shutdown")
async def shutdown_event():
    """Shutdown MCP server on backend shutdown."""
    from app.mcp_client import get_mcp_manager
    logger = __import__('logging').getLogger(__name__)

    manager = get_mcp_manager()
    if manager._initialized:
        await manager.shutdown()

if __name__ == "__main__":
    import uvicorn
    uvicorn.run("app.main:app", host="0.0.0.0", port=8000, reload=True)
