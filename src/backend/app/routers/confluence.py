"""
Confluence router - delegates to MCP for Confluence search/retrieval
"""

from fastapi import APIRouter, HTTPException

router = APIRouter()

@router.get("/search")
async def search_confluence(q: str):
    """Search Confluence - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        result = await manager.call_tool("confluence_search", {"keyword": q})
        return {"results": result}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")
