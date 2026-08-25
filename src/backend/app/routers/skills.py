"""
Skills router - delegates to MCP for skills search
"""

import re
from typing import Any

from fastapi import APIRouter, HTTPException

router = APIRouter()

def parse_skills_text(text: str) -> list[dict[str, Any]]:
    """Parse MCP formatted text into a list of skill dicts.
    
    Handles two formats:
    1. Individual skills (from search or specific category): ## SkillName (ID: skillID) ...
    2. Category headers (from list without category): ### CategoryName ...
    """
    # Check if this is a category-only listing (has ### headers but no ## skills)
    category_pattern = re.compile(
        r'^###\s+(.+?)\s*\n'
        r'- Category ID:\s+(.+?)\s*\n'
        r'- Skills:\s+(\d+)\s*\n',
        re.MULTILINE
    )
    
    cat_matches = list(category_pattern.finditer(text))
    
    if cat_matches and not re.search(r'^##\s+', text, re.MULTILINE):
        # This is a category-only listing (no individual skill details)
        categories = []
        for match in cat_matches:
            cat_name = match.group(1).strip()
            cat_id = match.group(2).strip()
            skill_count = int(match.group(3).strip())
            
            # Get skills from this section
            end_match = re.search(r'\n\n###', text[match.end():])
            section = text[match.end():match.end()+1000] if end_match else text[match.end():]
            
            # Try to parse individual skills in this section
            skill_pattern = re.compile(
                r'^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\n'
                r'- Level:\s+(.+?)\s*\n'
                r'- Description:\s+(.+?)\s*\n'
                r'- Training:\s+(.+?)\s*\n'
                r'- Assessor:\s+(.+?)\s*\n'
                r'- Validity:\s+(\d+)\s*months\s*\n',
                re.MULTILINE
            )
            
            nested_skills = []
            for skill_match in skill_pattern.finditer(section):
                nested_skills.append({
                    "name": skill_match.group(1).strip(),
                    "skill_id": skill_match.group(2).strip(),
                })
            
            categories.append({
                "name": cat_name,
                "category_id": cat_id,
                "skills_count": skill_count,
                "skills": nested_skills,
            })
        
        return categories
    
    # Otherwise, parse individual skills
    skills = []
    skill_pattern = re.compile(
        r'^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\n'
        r'- Level:\s+(.+?)\s*\n'
        r'- Description:\s+(.+?)\s*\n'
        r'- Training:\s+(.+?)\s*\n'
        r'- Assessor:\s+(.+?)\s*\n'
        r'- Validity:\s+(\d+)\s*months\s*\n',
        re.MULTILINE
    )
    
    for match in skill_pattern.finditer(text):
        skills.append({
            "name": match.group(1).strip(),
            "skill_id": match.group(2).strip(),
            "level": match.group(3).strip(),
            "description": match.group(4).strip(),
            "training_required": match.group(5).strip(),
            "assessor": match.group(6).strip(),
            "validity_months": int(match.group(7)),
        })
    
    return skills

@router.get("")
async def list_skills(category: str | None = None):
    """List skills - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        if category:
            result = await manager.call_tool("skills_list", {"category": category})
        else:
            result = await manager.call_tool("skills_list")
        
        # Parse MCP text response to JSON
        if isinstance(result, str):
            skills = parse_skills_text(result)
            return skills
        elif isinstance(result, dict) and "content" in result:
            content = result["content"]
            if isinstance(content, list):
                for item in content:
                    if item.get("type") == "text":
                        skills = parse_skills_text(item["text"])
                        return skills
        return []
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")

@router.get("/search")
async def search_skills(keyword: str):
    """Search skills - delegates to MCP"""
    from app.mcp_client import get_mcp_manager
    try:
        manager = get_mcp_manager()
        result = await manager.call_tool("skills_search", {"keyword": keyword})
        
        # Parse MCP text response to JSON
        if isinstance(result, str):
            skills = parse_skills_text(result)
            return skills
        elif isinstance(result, dict) and "content" in result:
            content = result["content"]
            if isinstance(content, list):
                for item in content:
                    if item.get("type") == "text":
                        skills = parse_skills_text(item["text"])
                        return skills
        return []
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")
