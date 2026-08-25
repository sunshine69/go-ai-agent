"""
RAG Router - Handles RAG-specific endpoints
"""
from fastapi import APIRouter, HTTPException

from app.rag import rag_store

router = APIRouter()

@router.post("/rag/search")
async def search_rag(req: dict):
    """Search RAG documents using vector similarity
    
    Body:
        query: str - Search query
        limit: int - Max results (optional, default 5)
        category: str - Filter by category (optional)
    """
    try:
        query = req.get("query", "")
        limit = int(req.get("limit", 5))
        category = req.get("category")
        
        results = rag_store.search(
            query=query,
            limit=limit,
            category=category,
        )
        return {"results": results, "total": len(results), "category": category}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"RAG search error: {str(e)}")

@router.get("/rag/categories")
async def get_rag_categories():
    """Get available document categories in RAG store"""
    try:
        categories = rag_store.get_categories()
        return {"categories": categories}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"RAG error: {str(e)}")

@router.get("/rag/stats")
async def get_rag_stats():
    """Get RAG store statistics"""
    try:
        stats = rag_store.get_stats()
        return stats
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"RAG error: {str(e)}")
