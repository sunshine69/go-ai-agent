"""
Context builder for domain-specific queries.

This module builds context for MCP tool calls based on domain selection.
It collects relevant keywords from the domain/sub-category selection
and the user's free-text input, then queries the appropriate MCP tools
with domain-aware search terms.

Usage:
    builder = ContextBuilder(session_state)
    context, sources = await builder.build_context(user_message, domain, sub_category)
"""

import asyncio
import re

from app.domain_config import DOMAINS
from app.rag import rag_store, RAG_ENABLED
from app.mcp_client import MCP_ENABLED


def extract_confluence_refs(tool_name: str, text: str) -> list[dict]:
    """Extract {title, id} references from confluence_search / confluence_read_page tool output.

    Parses the exact text formats produced by mcp-server/confluence-mcp.go —
    search results are '## <title> (ID: <id>)' headings, read_page output is a
    single '# <title>' heading with a separate 'Page ID: <id>' line.
    """
    refs = []
    if tool_name == "confluence_search":
        for m in re.finditer(r'^## (.+?) \(ID: (\d+)\)$', text, re.MULTILINE):
            refs.append({"title": m.group(1).strip(), "id": m.group(2)})
    elif tool_name == "confluence_read_page":
        title_m = re.match(r'# (.+)', text)
        id_m = re.search(r'Page ID: (\d+)', text)
        if title_m and id_m:
            refs.append({"title": title_m.group(1).strip(), "id": id_m.group(1)})
    return refs

class ContextBuilder:
    """Builds context for domain-specific queries."""

    def __init__(self, session_state):
        self.session_state = session_state

    def get_domain_keywords(self, domain: str) -> list[str]:
        """Get keywords for a domain."""
        if domain in DOMAINS:
            return DOMAINS[domain].get("keywords", [])
        return []

    def get_sub_category_keywords(self, domain: str, sub_category: str) -> list[str]:
        """Get keywords for a domain + sub-category."""
        if domain in DOMAINS:
            subs = DOMAINS[domain].get("sub_categories", {})
            if sub_category in subs:
                return subs[sub_category].get("keywords", [])
        return []

    def extract_keywords_from_message(self, message: str) -> list[str]:
        """Extract meaningful keywords from the user message."""
        import re

        stop_words = {
            "the", "a", "an", "and", "or", "but", "in", "on", "at", "to", "for",
            "of", "with", "by", "from", "is", "are", "was", "were", "be", "been",
            "being", "have", "has", "had", "do", "does", "did", "will", "would",
            "could", "should", "may", "might", "can", "shall", "it", "its", "i",
            "me", "my", "we", "our", "you", "your", "he", "him", "his", "she",
            "her", "they", "them", "their", "what", "which", "who", "whom", "this",
            "that", "these", "those", "show", "give", "tell", "provide", "find",
            "help", "need", "want", "please", "how", "why",
            # Conversational filler — not filtered by the length check below, so must be listed explicitly
            "yeah", "yep", "yup", "okay", "sure", "hey", "hello", "thanks", "thank",
        }

        tokens = re.findall(r"\b[a-z]+(?:\d+[a-z]*)?\b", message.lower())
        keywords = [t for t in tokens if t not in stop_words and len(t) >= 3]

        # Generate truncated versions for prefix matching
        additional = []
        for kw in keywords:
            if len(kw) > 8:
                for suffix in ["ing", "tion", "ment", "ness", "ance", "ence"]:
                    if kw.endswith(suffix):
                        truncated = kw[:-len(suffix)]
                        if len(truncated) >= 3:
                            additional.append(truncated)

        all_keywords = keywords + additional
        all_keywords.sort(key=len, reverse=True)
        return all_keywords[:5]

    def get_search_terms(self, message: str) -> list[str]:
        """Get search terms from message + domain keywords."""
        # Start with domain-specific keywords
        domain = self.session_state.get("selected_domain")
        sub_category = self.session_state.get("selected_sub_category")

        # Message keywords are the most specific to what was actually asked, so they
        # must win the priority order — only build_context[:3] gets used, and a plain
        # length sort let generic domain words (e.g. "operational") crowd out the
        # message's own words (e.g. "template").
        seen = set()
        ordered_terms = []

        def add_term(kw: str):
            kw = kw.lower()
            if kw not in seen:
                seen.add(kw)
                ordered_terms.append(kw)

        # Add message keywords first (highest priority)
        message_keywords = self.extract_keywords_from_message(message)
        for kw in message_keywords:
            add_term(kw)

        # Add sub-category keywords
        if sub_category:
            for kw in self.get_sub_category_keywords(domain or "", sub_category):
                add_term(kw)

        # Add domain keywords last (broadest, lowest priority)
        for kw in self.get_domain_keywords(domain or ""):
            add_term(kw)

        return ordered_terms

    def get_mcp_manager(self):
        """Get MCP manager instance."""
        from app.mcp_client import get_mcp_manager
        return get_mcp_manager()

    async def call_mcp_tool(self, name: str, args: dict | None = None) -> str:
        """Call a single MCP tool and return text result."""
        try:
            manager = self.get_mcp_manager()
            return await manager.call_tool(name, args)
        except Exception as e:
            return f"MCP tool '{name}' error: {str(e)}"

    async def _run_search(self, task_type: str, tool_name: str | None, args: dict | None, query: str):
        """Run either an MCP tool call or a RAG search.

        Args:
            task_type: 'mcp' or 'rag'
            tool_name: MCP tool name (only for 'mcp' tasks)
            args: Tool arguments (only for 'mcp' tasks)
            query: Search query (used for both MCP keywords and RAG)
        """
        if task_type == "mcp":
            try:
                manager = self.get_mcp_manager()
                return await manager.call_tool(tool_name, args)
            except Exception as e:
                return f"MCP tool '{tool_name}' error: {str(e)}"
        else:  # rag
            try:
                return rag_store.search(query, limit=5)
            except Exception as e:
                return f"RAG search error: {str(e)}"

    async def build_context(self, message: str) -> tuple[str, list[str], list[dict]]:
        """Build context from MCP tools based on domain selection.

        Returns:
            tuple of (context_text, sources, confluence_refs)
        """
        domain = self.session_state.get("selected_domain")
        sub_category = self.session_state.get("selected_sub_category")

        context_parts = []
        sources = []
        confluence_refs = []
        seen_confluence_ids = set()

        # Get search terms
        search_terms = self.get_search_terms(message)

        # Determine which MCP tools to call based on domain + sub-category (skipped
        # entirely when MCP search is disabled — RAG-only mode).
        tools_to_call = self._get_tools_for_domain(domain, sub_category) if MCP_ENABLED else []

        # Try up to 3 search terms to maximize hit rate
        # Tools that take a free-text keyword/query and should search for what the user
        # actually asked, rather than the domain's static placeholder keyword.
        KEYWORD_TOOLS = {"confluence_search", "documents_search", "forms_search", "skills_search", "process_search"}

        # Collect all MCP tool calls
        tool_calls = []  # list of (tool_name, call_args, search_term)
        called = set()
        for search_term in search_terms[:3]:
            for tool_name, tool_args in tools_to_call:
                call_args = dict(tool_args)
                if tool_name in KEYWORD_TOOLS:
                    call_args["query" if tool_name == "confluence_search" else "keyword"] = search_term

                cache_key = (tool_name, tuple(sorted(call_args.items())))
                if cache_key in called:
                    continue
                called.add(cache_key)
                tool_calls.append((tool_name, call_args, search_term))

        # Also collect RAG queries if enabled
        rag_queries = []
        if RAG_ENABLED:
            # Vector search is semantic, not literal - single extracted keywords like
            # "pocket" or "blood" often fail to retrieve the right document even though
            # the full question scores well (embeddings need sentence-level context).
            # Search the full message first, then top keywords for extra coverage.
            rag_queries = [message] + [t for t in search_terms[:2] if t.lower() != message.lower()]

        # Build task list: MCP calls + RAG searches — all run concurrently
        mcp_tasks = [(tool_name, args, "mcp", search_term) for tool_name, args, search_term in tool_calls]
        rag_tasks = [(None, None, "rag", rag_query) for rag_query in rag_queries]
        all_tasks = mcp_tasks + rag_tasks

        # Run everything concurrently
        results = await asyncio.gather(
            *[self._run_search(task_type, name, args, query) for name, args, task_type, query in all_tasks],
            return_exceptions=True
        )

        # Process results — MCP results first, then RAG results
        for task, result in zip(all_tasks, results):
            tool_name, args, task_type, query = task
            if isinstance(result, Exception):
                if task_type == "mcp":
                    result = f"MCP tool '{tool_name}' error: {str(result)}"
                else:
                    result = f"RAG search error: {str(result)}"

            if task_type == "mcp" and result and "error" not in result.lower():
                source_name = tool_name.replace("_", " ").title()
                context_parts.append(
                    f"=== {source_name} Search (keyword: '{query}') ===\n{result}"
                )
                if source_name not in sources:
                    sources.append(source_name)

                if tool_name in ("confluence_search", "confluence_read_page"):
                    for ref in extract_confluence_refs(tool_name, result):
                        if ref["id"] not in seen_confluence_ids:
                            seen_confluence_ids.add(ref["id"])
                            confluence_refs.append(ref)

            elif task_type == "rag" and isinstance(result, list):
                seen_chunk_ids = set()
                new_results = [r for r in result if r["chunk_id"] not in seen_chunk_ids]
                if new_results:
                    seen_chunk_ids.update(r["chunk_id"] for r in new_results)
                    rag_text = "\n".join([
                        f"=== {r['document_title']} (page {r.get('page_number', 'N/A')}): {r['content'][:500]}..."
                        for r in new_results
                    ])
                    context_parts.append(f"=== RAG Document Search (query: '{query}') ===\n{rag_text}")
                    for r in new_results:
                        doc_title = r.get("source_file", r.get("document_title", "Unknown"))
                        if doc_title not in sources:
                            sources.append(doc_title)

        context = "\n\n".join(context_parts)
        return context, sources, confluence_refs

    def _get_tools_for_domain(self, domain: str, sub_category: str) -> list[tuple[str, dict]]:
        """Get the list of MCP tools to call for a given domain + sub-category.

        Tool selection is driven by the domain configuration (``domains.yaml`` or the
        built-in fallback) via each sub-category's ``tools`` list, rather than any
        Sonic-specific hardcoded domain matching — so new/renamed domains work with no
        code changes.

        A ``tools`` entry is a ``{"tool": name, "args": {...}}`` dict. Entries may use
        either ``keyword`` or ``query`` as the argument key — the caller below rewrites
        whichever is present to match each MCP tool's expected argument name.

        Falls back to a broad "search everything" set when there is no domain, no
        matching sub-category, or the sub-category defines no explicit tools.
        """
        default_tools = [
            ("confluence_search", {"keyword": "*"}),
            ("documents_search", {"keyword": "*"}),
            ("forms_search", {"keyword": "*"}),
            ("skills_search", {"keyword": "*"}),
            ("process_search", {"keyword": "*"}),
        ]

        if not domain or domain not in DOMAINS:
            return list(default_tools)

        sub_categories = DOMAINS[domain].get("sub_categories", {})
        if not sub_category or sub_category not in sub_categories:
            return list(default_tools)

        sub_tools = sub_categories[sub_category].get("tools") or []
        if not sub_tools:
            # Sub-category exists but defines no explicit tools — fall back to broad search.
            tools = list(default_tools)
        else:
            tools = [
                pair for pair in (self._normalise_tool_args(e) for e in sub_tools)
                if pair is not None
            ]

        # Scope confluence_search calls to this domain's Confluence spaces, if configured.
        space_keys = DOMAINS.get(domain, {}).get("confluence_spaces", [])
        if space_keys:
            space_filter = ",".join(space_keys)
            tools = [
                (name, {**args, "space": space_filter}) if name == "confluence_search" else (name, args)
                for name, args in tools
            ]

        return tools

    @staticmethod
    def _normalise_tool_args(entry) -> tuple[str, dict] | None:
        """Extract a ``(tool_name, args)`` pair from a ``tools`` config entry.

        Accepts either:
        - a ``{"tool": name, "args": {...}}`` dict, or
        - a ``(tool_name, args)`` / ``[tool_name, args]`` list/tuple (the shape produced
          by ``domain_config._coerce_tools``).

        Args are passed through untouched — each may use ``keyword`` or ``query`` as
        its search key. ``build_context`` rewrites whichever key to the dynamic search
        term using the correct name per tool, so no remapping happens here.

        Returns ``None`` if the entry has no valid ``tool`` name.
        """
        if isinstance(entry, dict):
            tool = entry.get("tool")
            if not tool:
                return None
            return str(tool), dict(entry.get("args", {}) or {})
        elif isinstance(entry, (list, tuple)):
            if not entry:
                return None
            tool = entry[0]
            if not tool:
                return None
            args = entry[1] if len(entry) > 1 else {}
            if not isinstance(args, dict):
                args = {}
            return str(tool), dict(args)
        return None

