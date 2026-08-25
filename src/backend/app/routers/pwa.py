"""
SupersonicIQ Backend - PWA endpoints (manifest.json, service worker, icons, index.html)
"""

from fastapi import APIRouter
from fastapi.responses import HTMLResponse, Response
import os

router = APIRouter(tags=["PWA"])

# Get the frontend directory path
PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
FRONTEND_DIR = os.path.join(PROJECT_ROOT, "frontend")

def _file_path(filename: str) -> str:
    return os.path.join(FRONTEND_DIR, filename)

@router.get("/", response_class=HTMLResponse)
async def serve_index():
    """Serve the index.html page for PWA installation."""
    with open(_file_path("index.html"), "r") as f:
        content = f.read()
    return content

@router.get("/manifest.json")
async def get_manifest():
    """Serve the web app manifest for PWA installation."""
    with open(_file_path("manifest.json"), "r") as f:
        content = f.read()
    return Response(content=content, media_type="application/manifest+json")

@router.get("/sw.js")
async def get_service_worker():
    """Serve the service worker for PWA functionality."""
    with open(_file_path("sw.js"), "r") as f:
        content = f.read()
    return Response(content=content, media_type="application/javascript")

@router.get("/supersonicIQ-192.png")
async def get_icon_192():
    """Serve the 192x192 icon for PWA."""
    with open(_file_path("supersonicIQ-192.png"), "rb") as f:
        content = f.read()
    return Response(content=content, media_type="image/png")

@router.get("/supersonicIQ-512.png")
async def get_icon_512():
    """Serve the 512x512 icon for PWA."""
    with open(_file_path("supersonicIQ-512.png"), "rb") as f:
        content = f.read()
    return Response(content=content, media_type="image/png")
