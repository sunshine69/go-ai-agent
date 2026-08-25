"""
Documents router - delegates to MCP for document search/retrieval
"""

import json
import re
from typing import Any

from fastapi import APIRouter, HTTPException

router = APIRouter()

def parse_documents_text(text: str) -> list[dict[str, Any]]:
    """Parse MCP formatted text into a list of document dicts."""
    documents = []
    # Match patterns like: ## title (ID: doc_id)\nCategory: cat\nPath: path\n
    doc_pattern = re.compile(
        r'^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\n'
        r'Category:\s+(.+?)\s*\n'
        r'Path:\s+(.+?)(?:\n|$)',
        re.MULTILINE
    )
    
    for match in doc_pattern.finditer(text):
        title = match.group(1).strip()
        doc_id = match.group(2).strip()
        category = match.group(3).strip()
        path = match.group(4).strip()
        
        # Extract description if present
        desc_match = re.search(r'Description:\s+(.+?)\n', text[match.end()-1:])
        description = ""
        if desc_match:
            description = desc_match.group(1).strip()
        
        # Extract tags if present
        tags_match = re.search(r'Tags:\s+(.+?)\n', text[match.end()-1:])
        tags = []
        if tags_match:
            tags = [t.strip() for t in tags_match.group(1).strip().split(",")]
        
        documents.append({
            "id": doc_id,
            "title": title,
            "category": category,
            "path": path,
            "description": description,
            "tags": tags,
        })
    
    return documents

def parse_collections_text(text: str) -> list[dict[str, Any]]:
    """Parse MCP collections text into a list of collection dicts with documents."""
    documents = []
    # Match patterns like: ### collection_name\n- Documents: N\n- Path: path\n\n
    # Then followed by individual docs
    coll_pattern = re.compile(
        r'^###\s+(.+?)\s*\n'
        r'- Documents:\s+(\d+)\s*\n'
        r'- Path:\s+(.+?)\s*\n',
        re.MULTILINE
    )
    
    for match in coll_pattern.finditer(text):
        coll_name = match.group(1).strip()
        coll_path = match.group(3).strip()
        
        # Get docs from this section
        end_match = re.search(r'\n\n###', text[match.end():])
        section = text[match.end():match.end()+1000] if end_match else text[match.end():]
        
        # Parse docs within this collection section
        doc_pattern = re.compile(
            r'^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\n'
            r'Category:\s+(.+?)\s*\n'
            r'Path:\s+(.+?)(?:\n|$)',
            re.MULTILINE
        )
        
        for doc_match in doc_pattern.finditer(section):
            doc_id = doc_match.group(2).strip()
            # If doc_id doesn't contain collection prefix, prepend it
            if coll_name not in doc_id:
                doc_id = f"{coll_name}-{doc_id}"
            
            title = doc_match.group(1).strip()
            category = coll_name  # Use collection name as category
            path = doc_match.group(4).strip()
            
            documents.append({
                "id": doc_id,
                "title": title,
                "category": category,
                "path": path,
                "description": "",
                "tags": [],
            })
    
    return documents

@router.get("")
async def list_documents(collection: str | None = None):
    """List documents - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        if collection:
            result = await manager.call_tool("documents_list", {"collection": collection})
        else:
            result = await manager.call_tool("documents_list")
        
        # Parse MCP text response to JSON
        if isinstance(result, str):
            docs = parse_documents_text(result)
            if not docs:
                docs = parse_collections_text(result)
            return docs
        elif isinstance(result, dict) and "content" in result:
            content = result["content"]
            if isinstance(content, list):
                for item in content:
                    if item.get("type") == "text":
                        docs = parse_documents_text(item["text"])
                        if not docs:
                            docs = parse_collections_text(item["text"])
                        return docs
        return []
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")

@router.get("/{category}")
async def get_documents_in_category(category: str):
    """Get documents in category - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        result = await manager.call_tool("documents_list", {"collection": category})
        
        # Parse MCP text response to JSON
        if isinstance(result, str):
            docs = parse_documents_text(result)
            return docs
        elif isinstance(result, dict) and "content" in result:
            content = result["content"]
            if isinstance(content, list):
                for item in content:
                    if item.get("type") == "text":
                        docs = parse_documents_text(item["text"])
                        return docs
        return []
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")

@router.get("/{doc_id}/content")
async def get_document_content(doc_id: str):
    """Get document content - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        result = await manager.call_tool("documents_get_content", {"document_id": doc_id})
        return result
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")
