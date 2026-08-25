"""
Processes router - handles process browsing and search
"""

from fastapi import APIRouter, HTTPException
import os
import json

router = APIRouter()

def get_processes_path() -> str:
    """Get the path to processes.json relative to work_dir."""
    work_dir = os.getenv("MCP_WORK_DIR")
    if work_dir and os.path.isabs(work_dir):
        return os.path.join(work_dir, "resources", "documents", "skills", "processes.json")
    
    # Go up 3 levels: app/ → src/ → backend/ → project root
    return os.path.normpath(
        os.path.join(
            os.path.dirname(os.path.abspath(__file__)),
            "..", "..", "..",
            "resources", "documents", "skills", "processes.json"
        )
    )

@router.get("")
async def list_processes():
    proc_path = get_processes_path()
    if not os.path.exists(proc_path):
        return []
    
    with open(proc_path, "r") as f:
        data = json.load(f)
    
    # Handle both formats: old wrapper format {"processes": [...]} and new flat format [...]
    if isinstance(data, list):
        return data
    return data.get("processes", [])

@router.get("/search")
async def search_processes(keyword: str):
    proc_path = get_processes_path()
    if not os.path.exists(proc_path):
        return []
    
    with open(proc_path, "r") as f:
        data = json.load(f)
    
    # Handle both formats: old wrapper format {"processes": [...]} and new flat format [...]
    processes = data if isinstance(data, list) else data.get("processes", [])
    
    results = []
    keyword_lower = keyword.lower()
    
    for process in processes:
        if keyword_lower in process.get("name", "").lower() or \
           keyword_lower in process.get("description", "").lower():
            results.append(process)
    
    return results

@router.get("/{process_id}")
async def get_process(process_id: str):
    proc_path = get_processes_path()
    if not os.path.exists(proc_path):
        raise HTTPException(status_code=404, detail="Processes data not found")
    
    with open(proc_path, "r") as f:
        data = json.load(f)
    
    # Handle both formats: old wrapper format {"processes": [...]} and new flat format [...]
    processes = data if isinstance(data, list) else data.get("processes", [])
    
    for process in processes:
        if process.get("process_id") == process_id:
            return process
    
    raise HTTPException(status_code=404, detail="Process not found")

@router.get("/{process_id}/owner")
async def get_process_owner(process_id: str):
    proc_path = get_processes_path()
    if not os.path.exists(proc_path):
        raise HTTPException(status_code=404, detail="Processes data not found")
    
    with open(proc_path, "r") as f:
        data = json.load(f)
    
    # Handle both formats: old wrapper format {"processes": [...]} and new flat format [...]
    processes = data if isinstance(data, list) else data.get("processes", [])
    
    for process in processes:
        if process.get("process_id") == process_id:
            return {
                "process_id": process_id,
                "owner": process.get("owner", ""),
                "owner_title": process.get("owner_title", ""),
                "owner_email": process.get("owner_email", ""),
            }
    
    raise HTTPException(status_code=404, detail="Process not found")
