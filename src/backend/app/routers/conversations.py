"""
Conversations router + conversation store.

Maintains conversation history so the LLM can remember prior turns (cross-turn
context) and so the frontend can let the user scroll/select/delete conversations.

Storage is in-memory with an optional JSON-file overlay (CONVERSATIONS_STORE_PATH)
so history survives a backend restart. The file is read once at import and written
on every mutation; a threading lock guards concurrent requests under uvicorn's
async workers.

Function names imported by messages.py:
    conversation(id)        -> conv dict | None          (GET by id / lookup)
    conversation_id()       -> new conversation (create)
    append_message(...)     -> append a turn & persist
    create_conversation()   -> alias of conversation_id()
    list_conversations()    -> list of {id,title,created_at,updated_at}
    delete_conversation(id) -> bool
    clear_all()             -> count cleared

Routes:
    GET    /                     -> list conversations (public metadata only)
    POST   /                     -> create a conversation
    GET    /{conv_id}            -> full conversation including messages
    DELETE /{conv_id}            -> delete a single conversation
    DELETE /                     -> delete ALL conversations (clear all)
"""

import json
import os
import threading
from datetime import datetime, timezone

from fastapi import APIRouter, HTTPException

router = APIRouter()

# Store file lives next to src/backend/. Default: <project_root>/conversations.json
_DEFAULT_STORE_PATH = os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
    "conversations.json",
)
STORE_PATH = os.getenv("CONVERSATIONS_STORE_PATH", _DEFAULT_STORE_PATH)


# --------------------------------------------------------------------------- #
# Store (in-memory + optional JSON persistence)
# --------------------------------------------------------------------------- #
conversations_db: dict[str, dict] = {}
_lock = threading.Lock()


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _load() -> None:
    """Populate the in-memory store from the JSON file on startup."""
    global conversations_db
    if os.path.exists(STORE_PATH):
        try:
            with open(STORE_PATH, "r", encoding="utf-8") as f:
                loaded = json.load(f)
            if isinstance(loaded, dict):
                conversations_db = loaded
        except Exception:
            conversations_db = {}
    else:
        conversations_db = {}


def _save() -> None:
    """Persist the store to disk. Never raises — persistence is best-effort."""
    try:
        with open(STORE_PATH, "w", encoding="utf-8") as f:
            json.dump(conversations_db, f, indent=2)
    except Exception:
        pass


def conversation_id(title: str = "New Conversation") -> dict:
    """Create a new conversation and persist it. Returns the created conv."""
    with _lock:
        conv_id = f"CONV-{len(conversations_db) + 1:04d}"
        now = _now()
        conv = {
            "id": conv_id,
            "title": title,
            "created_at": now,
            "updated_at": now,
            "messages": [],
        }
        conversations_db[conv_id] = conv
        _save()
        return conv


# Alias so messages.py can call conversation_id() with no args.
create_conversation = conversation_id


def conversation(conv_id: str) -> dict | None:
    """Return the conv dict for id, or None if it doesn't exist."""
    return conversations_db.get(conv_id)


def list_conversations() -> list[dict]:
    """Public view: id/title/updated/created only (no full message history)."""
    with _lock:
        items = [
            {
                "id": c["id"],
                "title": c.get("title", "Untitled"),
                "created_at": c.get("created_at"),
                "updated_at": c.get("updated_at"),
            }
            for c in conversations_db.values()
        ]
        items.sort(key=lambda c: c.get("updated_at") or "", reverse=True)
        return items


def append_message(conv_id: str, role: str, content: str, key: str | None = None) -> dict | None:
    """Append a turn to a conversation and persist. Returns the conv or None."""
    with _lock:
        conv = conversations_db.get(conv_id)
        if not conv:
            return None
        entry = {"role": role, "content": content}
        if key is not None:
            entry["key"] = key
        conv["messages"].append(entry)
        # Seed a readable title from the first user message
        if role == "user" and conv.get("title") == "New Conversation":
            preview = " ".join(content.split())
            conv["title"] = (preview[:60] + "…") if len(preview) > 60 else preview or "Untitled"
        conv["updated_at"] = _now()
        _save()
        return conv


def delete_conversation(conv_id: str) -> bool:
    with _lock:
        if conv_id in conversations_db:
            del conversations_db[conv_id]
            _save()
            return True
        return False


def clear_all() -> int:
    with _lock:
        count = len(conversations_db)
        conversations_db.clear()
        _save()
        return count


# Populate the store on import (before the first request can use it).
_load()


# --------------------------------------------------------------------------- #
# Routes
# --------------------------------------------------------------------------- #
@router.get("")
async def list_conversations_endpoint():
    return list_conversations()


@router.post("")
async def create_conversation_endpoint(title: str = "New Conversation"):
    return conversation_id(title)


@router.get("/{conv_id}")
async def get_conversation_endpoint(conv_id: str):
    conv = conversation(conv_id)
    if conv is None:
        raise HTTPException(status_code=404, detail="Conversation not found")
    return conv


@router.delete("/{conv_id}")
async def delete_conversation_endpoint(conv_id: str):
    ok = delete_conversation(conv_id)
    if not ok:
        raise HTTPException(status_code=404, detail="Conversation not found")
    return {"message": "Conversation deleted"}


@router.delete("")
async def clear_conversations_endpoint():
    count = clear_all()
    return {"message": "Conversations cleared", "deleted": count}
