# SuperSonicIQ — Architecture Document

> **Tagline:** Ask Sonic once. Get the right answer. Super fast. Super simple. SuperSonicIQ.

## 1. Vision

SuperSonicIQ is an AI-powered conversational assistant that helps Sonic Healthcare employees quickly find accurate, trusted information across Sonic's existing knowledge sources — Confluence, SharePoint, documents, procedures, forms, and FAQs.

Instead of searching through multiple sources, users simply ask a question in natural language and receive a concise answer with a link back to the **approved source**.

## 2. Problem Statement

At Sonic, information is spread across Confluence, SharePoint, documents, procedures, forms, and different teams. Employees spend significant time searching for the right information and often the fastest option is simply asking someone else.

## 3. Hackathon Scope (POC)

For the hackathon, the scope is deliberately small and achievable:

- Connect **20–50 approved, non-sensitive documents**
- Focus on **3–5 high-value internal use cases**
- Build a simple conversational interface
- Demonstrate AI search/question-and-answer across multiple document sources
- Provide **source-linked answers** so users can validate the information

## 4. System Architecture

> Updated to reflect the actual implementation: **one** consolidated Go MCP server binary (not three separate proxies), plus a **RAG subsystem** (ChromaDB) that runs alongside MCP rather than being a future extension.

```
┌───────────────────────────────────────────────────────────────────────┐
│                        SuperSonicIQ System                            │
├───────────────────────────────────────────────────────────────────────┤
│                                                                       │
│  ┌──────────────┐                                                     │
│  │   Streamlit   │  ← Frontend Chat UI (domain + sub-category picker) │
│  │   Chat UI     │     (user asks question, sees answer)              │
│  └──────┬───────┘                                                     │
│         │ HTTP (FastAPI)                                              │
│         ▼                                                             │
│  ┌──────────────────────────────────────────────────────────────────┐ │
│  │   Backend — FastAPI (routes, ContextBuilder, MCP client, LLM)     ││
│  └──────┬───────────────────────────────────────┬───────────────────┘ │
│         │ MCP Protocol (stdio)                  │ Vector query        │
│         ▼                                        ▼                    │
│  ┌──────────────────────────────────────┐  ┌───────────────────────┐  │
│  │   MCP Server (Go, single binary)     │  │   RAG Store           │  │
│  │  - confluence_search / read_page /   │  │   (ChromaDB, SQLite   │  │
│  │    get_spaces / search_by_label      │  │   backend)            │  │
│  │  - documents_list / search /         │  │  - all-MiniLM-L6-v2   │  │
│  │    get_content                       │  │    embeddings         │  │
│  │  - forms_list / forms_search         │  │  - resources/         │  │
│  │  - skills_list / skills_search       │  │    rag_documents/     │  │
│  │  - process_get_steps / get_owner /   │  │    (PDF/MD indexed    │  │
│  │    process_search                    │  │    via CLI indexer)   │  │
│  │  - text_grep / section / toc / lines │  └───────────────────────┘  │
│  │    / wc / cat / head / tail          │                             │
│  │  - fetch_url / list_directory /      │                             │
│  │    read_file / file_glob_search      │                             │
│  └──────────────────┬───────────────────┘                             │
│                     │                                                 │
├─────────────────────┼─────────────────────────────────────────────────┤
│  CONFLUENCE CLOUD API          LOCAL RESOURCE FOLDER (resources/)     │
│  - Space browsing               documents/confluence/  - page exports │
│  - Page search & read            documents/skills/      - skills JSON │
│  - Label-based search             documents/procedures/ - procedures  │
│                                   documents/forms/       - forms      │
│                                   documents/faq/         - FAQs       │
│                                   rag_documents/<category>/ - unstructured PDF/MD for RAG │
└───────────────────────────────────────────────────────────────────────┘
```

The Go MCP server is one process (`mcp-server/main.go`) that registers all tool groups at startup — it is **not** split into separate Confluence/Skills/Documents proxies. Generic dev-agent tooling that shipped with the original toolkit (shell exec, file write/delete, HTTP, Postgres, Octopus Deploy, SVG, godoc/rustdoc, Playwright) has been removed/trimmed since it was unused attack surface for a read-only knowledge assistant — see [copilot-review.md](../copilot-review.md).

## 5. Design Philosophy — MCP for Structured Data, RAG for Unstructured Documents

### Why MCP over traditional RAG for structured content?

Traditional RAG systems work like this:
1. Index all documents into vector embeddings
2. On query → search vectors → retrieve top-k → pass to LLM
3. Problems: stale embeddings, can't fetch new content, no structured extraction

**Our MCP-first approach for structured sources (Confluence, forms, skills, processes):**
1. **No pre-indexing needed** — models fetch and extract information on-the-fly
2. **Always fresh** — MCP tools query live Confluence APIs, local files as needed
3. **Structured extraction** — MCP text tools (grep, section, toc) extract precise content, not vector fragments
4. **Dynamic tool selection** — the backend's `ContextBuilder` chooses which MCP tools to call based on the selected domain/sub-category and message keywords
5. **Composability** — multiple MCP tools can be chained for complex queries
6. **Smaller context window** — extract only what's needed instead of dumping entire documents

### RAG complements MCP for unstructured documents

SupersonicIQ **does** ship a working RAG subsystem (`src/backend/app/rag.py`), used for PDFs/Markdown that haven't been modeled as structured MCP data (e.g. policy documents under `resources/rag_documents/`). Implementation:

- **Vector store:** ChromaDB (`PersistentClient`, SQLite-backed), collection `rag_documents`, cosine distance.
- **Embeddings:** `sentence-transformers` model `all-MiniLM-L6-v2` (384-dim), configurable via `RAG_EMBEDDING_MODEL`.
- **Chunking/indexing:** offline CLI indexer, `python -m app.cli.rag_indexer --mode [full|incremental|reset|dry-run] [--category policy|procedure|training|reference]`, run from `src/backend/`. Tracks per-file checksums in `_index_state.json` for incremental re-indexing.
- **Config:** `RAG_ENABLED` (default `true`, disables ChromaDB/sentence-transformers imports entirely when `false`), `CHROMA_PATH`, `RAG_CHUNK_SIZE` (1500), `RAG_CHUNK_OVERLAP` (300), `RAG_SEARCH_LIMIT` (5), `RAG_SCORE_THRESHOLD` (0.25 — results below this similarity are dropped).
- **Current content:** only the `policy` category is populated today (`resources/rag_documents/policy/{onboarding_policy,safety_policy}.md`); `procedure`/`training`/`reference` are supported by the indexer but have no documents yet.
- **API:** `GET /api/domains`... `POST /api/rag/search`, `GET /api/rag/categories` (`src/backend/app/routers/rag.py`).

**Retrieval flow differs from the original "MCP-first, RAG-as-fallback" design**: `ContextBuilder.build_context()` (`src/backend/app/context_builder.py`) calls the domain-mapped MCP tools **and unconditionally also queries the RAG store** for the same search terms (when `RAG_ENABLED`), concatenating both into the LLM context — it does not currently gate RAG on "MCP returned nothing." Both subsystems can be independently toggled via `MCP_ENABLED` (default `true`) and `RAG_ENABLED` (default `true`); setting `MCP_ENABLED=false` skips Go MCP subprocess startup and RAG-only mode is used, while `RAG_ENABLED=false` leaves MCP search fully intact.

### MCP + RAG Tool Flow for a Query

```
User: "Which pathology request form do I need for this test?"

1. Backend receives query + selected domain/sub-category
2. ContextBuilder derives search terms (domain keywords + message keywords)
3. For each search term, calls the MCP tools mapped to that domain:
   - e.g. "forms_search" → "pathology_request_form.md"
4. In parallel, queries the RAG store (ChromaDB) with the same search terms
5. Both MCP + RAG results are concatenated into the LLM context
6. LLM composes an answer citing the sources
7. Backend returns answer + source list to the Streamlit UI
```

## 6. Component Breakdown

### 6.1 Streamlit Chat Frontend

- **Tech:** Streamlit + Python
- **Features:**
  - Chat interface with message history
  - Domain + sub-category picker (HR, Pathology, Radiology, Operations) that narrows which MCP tools/keywords get used
  - Source citation display (which document was referenced)
  - Loading indicators for MCP tool execution
- **Location:** `src/frontend/app.py`

### 6.2 FastAPI Backend

- **Tech:** FastAPI (Python) + MCP Python SDK + LiteLLM
- **Features:**
  - REST API endpoints under `/api/*` (auth, conversations, messages, domains, documents, forms, skills, processes, confluence, rag)
  - `ContextBuilder` (`app/context_builder.py`) builds domain-aware search terms and queries MCP + RAG
  - `MCPClientManager` (`app/mcp_client.py`) manages the stdio subprocess lifecycle for the Go MCP server
  - `get_llm_answer` (`app/routers/messages.py`) calls the configured LLM via LiteLLM (OpenAI-compatible, incl. local servers)
- **Location:** `src/backend/app/main.py`
- **Known gaps:** auth tokens are unsigned/unverified and conversations/users are in-memory only (not persisted) — see [copilot-review.md](../copilot-review.md).

### 6.3 Go MCP Server

- **Tech:** Go + `mark3labs/mcp-go`, single binary (`mcp-server/main.go`), stdio transport by default (streamable HTTP also supported via `-t streamable`)
- **Tools (all loaded by default — no more opt-in `-tools` flag):**
  - `fetch_url` — Fetches URLs and converts to markdown
  - `list_directory`, `read_file`, `file_glob_search` — read-only filesystem browsing
  - `text_grep`, `text_section`, `text_toc`, `text_lines`, `text_wc`, `text_cat`, `text_head`, `text_tail` — structured text extraction (`text-tool.go`)
  - `confluence_search`, `confluence_read_page`, `confluence_get_spaces`, `confluence_search_by_label` (`confluence-mcp.go`)
  - `documents_list`, `documents_search`, `documents_get_content`, `forms_list`, `forms_search` (`documents-mcp.go`)
  - `skills_list`, `skills_search`, `process_get_steps`, `process_get_owner`, `process_search` (`skills-mcp.go`)
- **Removed:** `run_terminal_command`, `exec_command`, `remove_file_or_directory`, `create_new_file`, `find_replace_in_file`, `http_request`, plus the opt-in Postgres/Octopus Deploy/SVG/godoc/rustdoc/Playwright toolkits — none were used by the backend and they were unnecessary attack surface for a read-only assistant.
- **Location:** `mcp-server/` (top-level Go module, all tool files in one package — not split into subfolders)

### 6.4 Domain-Aware Query Routing

- **Tech:** Python dict config (`app/domain_config.py`) + `ContextBuilder` (`app/context_builder.py`)
- **Domains:** HR, Pathology, Radiology, Operations, each with sub-categories (e.g. onboarding, forms, policies, contacts) mapping to keyword lists and a fixed set of MCP tool calls (see `ContextBuilder._get_tools_for_domain`)
- **Behavior:** no domain selected → global keyword search across all MCP tools + RAG; domain selected → domain keywords added; domain + sub-category selected → sub-category keywords also added

### 6.5 RAG Subsystem

- **Tech:** ChromaDB (SQLite-backed) + `sentence-transformers` (`all-MiniLM-L6-v2`)
- **Location:** `src/backend/app/rag.py` (store), `src/backend/app/routers/rag.py` (API), `src/backend/app/cli/rag_indexer.py` (offline indexer)
- **Data:** `resources/rag_documents/<category>/*.{md,pdf}` — see §5 for details

## 7. Data Flow

```
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│  User    │────▶│ Streamlit│────▶│ FastAPI  │────▶│ Go MCP   │────▶│ MCP      │
│  Question│     │  Chat UI │     │  Server  │     │ Server   │     │ Confluence│
└──────────┘     └──────────┘     └──────────┘     └──────────┘     └──────────┘
                                                                    │
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐     ▼
│  Answer  │◀────│ Streamlit│◀────│ FastAPI  │◀────│ Go MCP   │     Local Files
│  Display │     │  Chat UI │     │  Server  │     │ Server   │     (text tools)
└──────────┘     └──────────┘     └──────────┘     └──────────┘     └──────────┘
```

## 8. Concurrency Model

- Each Streamlit chat session = one MCP client instance
- MCP client communicates with Go MCP server over stdio
- FastAPI handles multiple concurrent sessions via asyncio
- Go MCP server handles concurrent tool calls internally

## 9. Security Considerations

- MCP server has built-in path whitelisting (`ALLOWED_PATH_PTN`/`BLOCKED_PATH_PTN`) and now exposes only read-only tools by default (see §6.3) — the shell-exec/file-write/delete tools were removed as unnecessary attack surface.
- Confluence API uses token-based auth (configured via env var)
- Local file access restricted by Go MCP server path patterns
- No PII stored in chat history
- All answers include source citations for audit trail
- **Not yet production-ready:** the `/api/auth` router issues unsigned demo tokens that are never verified on subsequent requests, so all other endpoints are effectively unauthenticated; CORS is `allow_origins=["*"]` with credentials enabled; conversations/users are stored in-memory only. Full findings in [copilot-review.md](../copilot-review.md).

## 10. Hackathon Timeline

### Day 1: Foundation
- **Morning:** Set up project structure, create sample documents, build Confluence MCP proxy
- **Afternoon:** Build skills & process MCP proxy, build documents & forms MCP proxy

### Day 2: Integration & Polish
- **Morning:** Build FastAPI backend, integrate MCP clients
- **Afternoon:** Build Streamlit UI, polish, test end-to-end

## 11. Sample Use Cases (Hackathon POC)

1. **Pathology Request Forms**
   - "Which form for a full blood count?" → Returns form name + source link
   - "Where is the latest blood collection procedure?" → Returns procedure with source

2. **Onboarding**
   - "How do I onboard a new site?" → Returns step-by-step process + source
   - "Who owns the lab services process?" → Returns owner info + source

3. **Procedures**
   - "Where is the procedure for X?" → Returns procedure with source link
   - "How do I complete request Y?" → Returns step-by-step with source

## 12. Sample Confluence Space/Content Mapping

For the hackathon POC, we will connect to a small set of approved Confluence spaces:

- **Lab Operations Space** — Pathology forms, collection procedures
- **People Space** — Onboarding, skills directory
- **Quality Space** — Quality procedures, SOPs
- **IT Support Space** — FAQ documents, IT processes

## 13. Sample Skills & Process Data

The skills and process MCP will reference local JSON files mapping:
- Skill categories → descriptions
- Process IDs → step-by-step instructions
- Process owners → contact info

## 14. Local Resources (Hackathon POC)

For the hackathon, we simulate Confluence/SharePoint with local files:

```
resources/
├── documents/
│   ├── confluence/        — Saved Confluence page exports
│   ├── skills/            — Skills directory (JSON)
│   ├── procedures/        — Procedure documents
│   ├── forms/             — Request form templates
│   └── faq/               — FAQ documents
└── rag_documents/         — Unstructured PDF/MD indexed into ChromaDB (see §5, §6.5)
    ├── policy/            — populated today
    ├── procedure/         — supported, not yet populated
    ├── training/          — supported, not yet populated
    └── reference/         — supported, not yet populated
```

## 15. Environment Variables

```bash
# Confluence API
CONFLUENCE_BASE_URL=https://your-org.atlassian.net/wiki
CONFLUENCE_API_TOKEN=your_token_here
CONFLUENCE_USERNAME=your_email@sonichealthcare.com.au

# MCP Server
MCP_SERVER_PORT=8080
MCP_SERVER_TRANSPORT=stdio  # or streamable

# LLM Configuration
OPENAI_API_KEY=your_key_here
LLM_MODEL=gpt-4o
LLM_TEMPERATURE=0.3

# RAG
RAG_ENABLED=true
MCP_ENABLED=true
CHROMA_PATH=./rag_chroma_db
RAG_EMBEDDING_MODEL=all-MiniLM-L6-v2
RAG_CHUNK_SIZE=1500
RAG_CHUNK_OVERLAP=300
RAG_SEARCH_LIMIT=5
RAG_SCORE_THRESHOLD=0.25
```

## 16. Future Extensibility

- SharePoint MCP proxy (using Microsoft Graph API)
- Search MCP proxy (Elasticsearch/Weaviate)
- Database MCP proxy (PostgreSQL for structured data)
- Email MCP proxy (Outlook API)
- Calendar MCP proxy (Outlook API)
- File MCP proxy (SharePoint local file sync)
- Multi-tenant support
- Conversation history
- Feedback/evaluation loop
