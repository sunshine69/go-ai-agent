"""
Forms router - delegates to MCP for forms search
"""

import re
from typing import Any

from fastapi import APIRouter, HTTPException

router = APIRouter()

def parse_forms_text(text: str) -> list[dict[str, Any]]:
    """Parse MCP formatted text into a list of form dicts."""
    forms = []
    # Match patterns like: ## FormName (ID: formID)\nCategory: cat\nPath: path\n\n
    form_pattern = re.compile(
        r'^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\n'
        r'Category:\s+(.+?)\s*\n'
        r'Path:\s+(.+?)(?:\n|$)',
        re.MULTILINE
    )
    
    for match in form_pattern.finditer(text):
        title = match.group(1).strip()
        form_id = match.group(2).strip()
        category = match.group(3).strip()
        path = match.group(4).strip()
        
        forms.append({
            "id": form_id,
            "title": title,
            "category": category,
            "path": path,
        })
    
    return forms

@router.get("")
async def list_forms(q: str | None = None):
    """List/search forms - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        if q:
            result = await manager.call_tool("forms_search", {"keyword": q})
        else:
            result = await manager.call_tool("forms_list")
        
        # Parse MCP text response to JSON
        if isinstance(result, str):
            forms = parse_forms_text(result)
            return forms
        elif isinstance(result, dict) and "content" in result:
            content = result["content"]
            if isinstance(content, list):
                for item in content:
                    if item.get("type") == "text":
                        forms = parse_forms_text(item["text"])
                        return forms
        return []
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")
