"""
Messages router - handles message sending and AI responses
Uses MCP tools for context retrieval + LLM for response generation
"""

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel
import litellm
import os
import asyncio

# Context builder for domain-specific queries
from app.context_builder import ContextBuilder

# Domain configuration
from app.domain_config import DOMAINS

router = APIRouter()

class MessageRequest(BaseModel):
    message: str
    # Empty string (or absent) => start a NEW conversation. A real id => append
    # to that existing conversation and maintain cross-turn context.
    conversation_id: str = ""
    # Prior conversation turns (from the frontend) so the LLM retains context.
    # Each item is {role: "user"|"assistant", content: str}. Does NOT include the
    # current message — the backend persists this turn separately.
    history: list[dict] | None = None
    domain: str | None = None        # Domain selection (e.g., "IT", "Science & Technology")
    sub_category: str | None = None   # Sub-category selection (e.g., "onboarding", "forms")

class MessageResponse(BaseModel):
    conversation_id: str | None = None
    answer: str
    sources: list = []
    confluence_links: list = []  # [{"title": str, "url": str}]

def build_system_prompt() -> str:
    """Build system prompt with dual identity support.

    Returns the full system prompt that defines both GenIQ (knowledge mode)
    and Friendly Agent (casual mode) identities. When MCP/RAG context is empty,
    the prompt is augmented to instruct the Friendly Agent to answer freely.
    """
    base_prompt = """You are a helpful AI assistant with TWO identities:

## Identity 1: GenIQ (knowledge mode)
You are an expert knowledge assistant for this organization.
You answer questions about the organization's procedures, forms, skills, processes, and policies.
You are thorough, accurate, and cite sources when referencing knowledge base content.
Use the provided context to give accurate responses, always citing sources.

**How to cite RAG document sources:**
When your answer comes from RAG document snippets (shown with format "=== [Document Title] (page [X]): ..."), you MUST include the document title in your answer. For example: "According to the MRI Brochure, you should..." or "The HR Employee Handbook states...". Cite the document title every time you use information from a document.

**How to cite Confluence sources:**
When your answer comes from Confluence pages (shown with format "- [Page Title](url)"), include a link to the Confluence page. For example: "As described in the Onboarding Guide [[link]]."

**When no context is provided:**
If no document context is shown above your answer, use your general knowledge but still cite what you know.

## Identity 2: Friendly Agent (casual mode)
You are a fun, casual AI assistant.
You answer general questions, tell jokes, chat, and be helpful in everyday ways.
You are witty, friendly, and approachable — like a helpful coworker who's also funny.
You can handle anything outside the knowledge base — weather, recipes, trivia, life advice — with a light, warm tone.

## How to choose which identity
- If the question is about the organization's knowledge base, forms, procedures, or related topics, you are GenIQ.
- For everything else, you are the Friendly Agent.
- Use whichever identity feels most natural — you can seamlessly switch between modes."""

    # If the question is about the organization, augment the prompt to stay in knowledge mode
    system_prompt = base_prompt

    return system_prompt

def detect_no_useful_context(context_text: str, sources_list: list) -> bool:
    """Detect if gathered context is empty or purely error-based.

    When all MCP tools and RAG return nothing, this function returns True
    so the backend skips the context and lets the LLM answer freely.
    """
    if not context_text or not context_text.strip():
        return True
    # Count non-empty lines — if most lines are just error messages, treat as no good context
    lines = [l for l in context_text.split("\n") if l.strip()]
    if len(lines) == 0:
        return True
    # If RAG document snippets are present in the context (indicated by "=== RAG Document Search" headers),
    # don't discard context just because MCP tools had errors
    if "=== RAG Document Search" in context_text:
        return False
    # If sources_list contains document titles (PDF/MD files), RAG results are present
    if sources_list:
        doc_extensions = (".pdf", ".md", ".docx", ".doc")
        for src in sources_list:
            if src.lower().endswith(doc_extensions):
                return False
    error_count = sum(1 for l in lines if "error" in l.lower() and len(l) < 100)
    if error_count >= len(lines) - 1:
        return True
    return False

async def get_llm_answer(context: str, user_message: str, system_prompt: str, history: list[dict] | None = None) -> str:
    """Get answer from LLM with MCP context. Uses LiteLLM to support local OpenAI-compatible servers.

    history is a list of prior {role, content} turns (NOT including the current
    message). It is injected as genuine conversation context so the model retains
    memory across turns within a conversation. Each item is validated to only
    contain the allowed keys and a string content value.
    """
    model = os.getenv("LLM_MODEL", "gpt-4o")
    api_key = os.getenv("LLM_API_KEY", "sk-placeholder")
    base_url = os.getenv("LLM_BASE_URL", None)  # local server URL (e.g., http://192.168.20.23/v1)
    temperature = float(os.getenv("LLM_TEMPERATURE", "0.1"))

    messages = [
        {"role": "system", "content": system_prompt},
    ]

    # Inject validated prior turns as real conversational context.
    if history:
        for turn in history:
            role = turn.get("role")
            content = turn.get("content")
            if role not in ("user", "assistant") or not isinstance(content, str) or not content.strip():
                continue
            messages.append({"role": role, "content": content})

    if context:
        messages.append({
            "role": "user",
            "content": f"Here is additional context from the knowledge base:\n\n{context}\n\nPlease answer this question:\n\n{user_message}"
        })
    else:
        messages.append({
            "role": "user",
            "content": user_message
        })

    try:
        model_to_use = f"openai/{model}"

        response = litellm.completion(
            model=model_to_use,
            api_key=api_key,
            base_url=base_url,
            messages=messages,
            temperature=temperature,
        )
        return response.choices[0].message.content
    except Exception as e:
        return f"Sorry, I encountered an error: {str(e)}"

@router.get("/tools")
async def list_available_tools():
    """List available MCP tools"""
    try:
        from app.mcp_client import get_mcp_manager
        manager = get_mcp_manager()
        tools = await manager.list_tools()
        return {"tools": tools}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"MCP error: {str(e)}")

@router.post("")
async def send_message(req: MessageRequest):
    from app.routers.conversations import (
        append_message,
        create_conversation,
        conversation,
    )

    # --- Resolve the conversation -------------------------------------------
    # Empty/absent conversation_id => start a NEW conversation. Otherwise reuse
    # the existing one (unknown ids fall back to a fresh conversation) and build
    # cross-turn context from its persisted history.
    _cid = req.conversation_id or ""

    if _cid:
        _existing = conversation(_cid)
        _conv_id = _existing["id"] if _existing else ""
    else:
        _conv_id = ""

    if not _conv_id:
        _conv_id = create_conversation()["id"]

    # --- Build prior turns for the LLM (cross-turn context) -----------------
    # Replay every persisted turn EXCEPT the current one (sent in the context
    # block below). Gives the model real memory across turns without duplicating
    # the latest user message.
    history = []
    _prior = conversation(_conv_id)
    _user_key = f"__current_user__:{req.message.strip()}"
    for turn in _prior["messages"]:
        if turn["role"] != "user":
            history.append({"role": turn["role"], "content": turn["content"]})
        else:
            if turn.get("key") != _user_key:
                history.append({"role": turn["role"], "content": turn["content"]})

    # Build system prompt with dual identity support
    system_prompt = build_system_prompt()

    # Build context from MCP tools — when context is empty or purely errors, we
    # skip MCP/RAG and let the LLM answer freely based on its own knowledge.
    builder = ContextBuilder({
        "selected_domain": req.domain,
        "selected_sub_category": req.sub_category,
    })
    context, sources, confluence_refs = await builder.build_context(req.message)

    # Skip MCP context when no useful results were found (avoids robotic "nothing found" replies)
    if detect_no_useful_context(context, sources):
        # When context is empty, let the LLM answer freely. No need to augment prompt further.
        pass

    # Turn Confluence page refs into clickable links using the same base URL the MCP server uses
    confluence_base_url = os.getenv("CONFLUENCE_BASE_URL", "").rstrip("/")
    confluence_links = [
        {"title": ref["title"], "url": f"{confluence_base_url}/pages/viewpage.action?pageId={ref['id']}"}
        for ref in confluence_refs
        if confluence_base_url
    ]

    # Give the LLM the exact URLs so it can cite them inline instead of just listing them separately
    if confluence_links:
        links_block = "\n".join(f"- [{l['title']}]({l['url']})" for l in confluence_links)
        context = f"{context}\n\n=== Available Confluence Links (cite using these exact URLs) ===\n{links_block}"

    # Get answer from LLM with context (including conversation history if any)
    answer = await get_llm_answer(context, req.message, system_prompt, history)

    # --- Persist the current turn -------------------------------------------
    # User turn is tagged with a sentinel key so the backend can recognise and
    # exclude it from the next turn's history replay. Assistant turn is plain.
    append_message(_conv_id, "user", req.message, key=f"__current_user__:{req.message.strip()}")
    if answer:
        append_message(_conv_id, "assistant", answer)

    return MessageResponse(
        conversation_id=_conv_id,
        answer=answer,
        sources=sources if sources else ["Direct Answer"],
        confluence_links=confluence_links,
    )