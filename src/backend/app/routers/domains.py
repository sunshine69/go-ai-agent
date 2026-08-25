"""
Domains router - provides domain and sub-category configurations
"""

from fastapi import APIRouter

# Domain configuration
from app.domain_config import DOMAINS

router = APIRouter()

@router.get("")
async def list_domains():
    """List all available domains with their icons and sub-categories."""
    domains = []
    for domain_key, domain_info in DOMAINS.items():
        domains.append({
            "key": domain_key,
            "display_name": domain_info["display_name"],
            "icon": domain_info["icon"],
            "sub_categories": {
                sub_key: {
                    "display_name": sub_info["display_name"],
                    "icon": sub_info["icon"],
                }
                for sub_key, sub_info in domain_info.get("sub_categories", {}).items()
            },
        })
    return {"domains": domains}
