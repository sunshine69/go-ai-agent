# Backend Port Plan — Python FastAPI → Go

> **Scope:** Port *only* the knowledge backend. The Wails app (Go) and the Go MCP server already exist; they are not touched by this plan.
>
> **Goal:** Replace `src/backend/app/` (Python/FastAPI) with a Go module that speaks the **exact same HTTP contract** the Wails frontend already calls. No endpoint shape changes, no API refactors mid-port.
>
> **Out of scope (NOT this plan):** streamlit, the Wails frontend, the MCP server, RAG embedding model research (flagged as the one open risk).

---

## 1. What already exists (the contract we must match)

The Wails frontend calls the backend over HTTP. These are the contracts — frozen, must not change:

| Endpoint | Method | Request body | Response shape |
|---|---|---|---|
| `/api/domains` | GET | — | `{ "domains": [{key, display_name, icon, sub_categories:{key:{display_name, icon}}}] }` |
| `/api/messages` | POST | `{message, conversation_id?, domain?, sub_category?}` | `{conversation_id, answer, sources[], confluence_links:[{title,url}]}` |
| `/api/conversations` | GET | — | `[{id, title, created_at, updated_at, messages:[]}]` |
| `/api/conversations` | POST | `{title?}` | `{id, title, created_at, updated_at, messages:[]}` |
| `/api/conversations/{id}` | GET | — | single conv object |
| `/api/conversations/{id}` | DELETE | — | `{message:"Conversation deleted"}` |
| `/api/documents/*`, `/api/forms/*`, `/api/skills/*`, `/api/processes/*` | GET/POST | varies | catalog/list shapes (keep as-is) |
| `/api/confluence/search?q=` | GET | `q: string` | `confluence_search` tool text (or list) |
| `/api/rag/search` | POST | `{query, limit?, category?}` | `{query, results:[...], duration_ms}` |
| `/api/rag/categories` | GET | — | `{"categories":[...]}` |
| `/api/auth/register`, `/api/auth/login`, `/api/auth/me` | POST/GET | varies | `{user_id, username, email}` / `{access_token, token_type}` |

All these currently sit behind `src/backend/app/main.py` which mounts `app.routers.*`. The Go backend must expose the **same path tree under `/api/*`**.

---

## 2. Target architecture

```
┌────────────┐   HTTP (same /api/* paths)   ┌──────────────────────────┐
│ Wails app  │ ───────────────────────────▶ │  Go backend (this plan)   │
│ (Go)       │                                │  net/http + mux            │
└────────────┘                                └──────────┬───────────────┘
                                                         │ stdio / streamable HTTP
                                                         ▼
                                                ┌──────────────────────────┐
                                                │  Go MCP server (existing) │
                                                │  mcp-server/              │
                                                └──────────────────────────┘
                                                         │
                                            ┌────────────┴────────────┐
                                            │ Confluence API           │
                                            │ local files (resources/) │
                                            │ ChromaDB RAG (see §4.5)  │
                                            └──────────────────────────┘
```

- **No Python in the runtime path.** The Go backend talks to the Go MCP server (language-consistent, already CORS-on) and to the LLM via an OpenAI-compatible HTTP endpoint.
- `main.py` → Go `main()` / router bootstrap.
- `routers/*` → Go HTTP handlers grouped by domain.
- `mcp_client.py` → Go MCP client (use `mark3labs/mcp-go/client` stdio stream).
- `context_builder.py` + `domain_config.py` → the brain; port to Go (see §3).
- `rag.py` → see §4.5.
- `auth.py` + `conversations.py` → in-memory stores with the same IDs; `bcrypt` via `golang.org/x/crypto/bcrypt`.

---

## 3. The core brain — port `context_builder.py` + `domain_config.py` to Go

This is the highest-value, most intricate piece. It is the reason the port exists, and it has **specific subtleties that must survive verbatim**.

### 3.1 `domain_config.py` → Go data structure
`DOMAINS` is a plain dict of `HR/Pathology/Radiology/Operations` → `{display_name, icon, sub_categories{key:{display_name,icon,keywords}}, keywords, confluence_spaces}`.

Port as a Go `map[string]Domain` (or typed struct with a JSON tag round-trip). It feeds **two** things:
1. `GET /api/domains` (render pills in Wails) — must keep exact JSON field names (`display_name`, `sub_categories`).
2. `ContextBuilder` routing.

Keep the `confluence_spaces` values exactly — they scope Confluence calls (`PMP,DPAI,DHMG,SDX` etc.).

### 3.2 Keyword extraction (`extract_keywords_from_message`)
Port **exactly**:
- `stop_words` set (including the explicitly-listed conversational fillers — do not drop them).
- regex `\b[a-z]+(?:\d+[a-z]*)?\b` on lowercased input.
- `len(t) >= 3` filter.
- truncation for tokens `> 8` chars ending in `ing/tion/ment/ness/ance/ence` → strip suffix, keep if `>= 3`.
- sort by length desc, return top **5**.
- Go regex: `regexp.MustCompile(`\b[a-z]+(?:\d+[a-z]*)?\b`)`. Behavior matches Python's `re` for this input.

### 3.3 `get_search_terms` ordering (critical parity)
Order is a **product decision**, not cosmetic — it determines which keyword wins. Preserve exactly:
1. message keywords first (highest priority),
2. then sub-category keywords,
3. then domain keywords (broadest), deduplicated, all lowercased.
Note `build_context` only uses `search_terms[:3]` and caps MCP calls at 3 terms — keep those slices.

### 3.4 `_get_tools_for_domain` → routing matrix
Port the full if/elif matrix for HR/Pathology/Radiology/Operations × their sub-categories (onboarding, skills, policies, contacts, forms, processes, systems). This includes the `onboard*`/`owner`/`imaging`/`referral`/`PACS`/`DICOM`/`SOP`/`operations manual` keyword args. Keep every keyword literal.

Two fallback branches to preserve:
- **no sub-category** on a recognized domain → fall back to broad search across all 5 MCP tools.
- **unknown domain** (the `else`) → broad search across all 5.
- **empty result for a known domain** → broad search across all 5.

Then the **scope filter**: for each domain's `confluence_spaces`, rewrite every `confluence_search` arg to add `space=<comma-joined>`. If empty, do not add.

### 3.5 Concurrency: `asyncio.gather` → goroutines
`build_context` runs all MCP calls + RAG queries **in parallel** via `asyncio.gather(..., return_exceptions=True)`. Go equivalent: launch a goroutine per task, collect via channels / `errgroup`, and **map results back in task order** (the code zips `all_tasks` with `results`). Preserve the result-processing order (MCP first, then RAG) and the de-dup logic (confluence IDs, RAG chunk IDs).

`return_exceptions=True` → each task must never panic; wrap in the handler and emit `MCP tool 'X' error: ...` / `RAG search error: ...` strings identically (the `detect_no_useful_context` regex keys off these).

---

## 4. The four knowledge sources (thin wrappers over MCP)

These routers are thin: they call MCP tools and format the output. Port as Go handlers that call the MCP client.

| Python router | What it does | Go equivalent |
|---|---|---|
| `confluence.py` | `GET /search?q=` → `confluence_search` tool text | handler → MCP `confluence_search` |
| `documents.py` | `parse_documents_text` regex + `documents_list/search/get_content` | port `parse_documents_text` regex; call MCP tools |
| `forms.py` | `parse_forms_text` regex + `forms_list/forms_search` | port regex; call MCP tools |
| `skills.py` | `parse_skills_text` regex + `skills_list/skills_search` | port regex; call MCP tools |
| `processes.py` | local JSON read + `process_*` MCP tools | port JSON read; call MCP tools |

- **Regexes:** `parse_documents_text`, `parse_forms_text`, `parse_skills_text` — port verbatim to Go `regexp`. They match `## Title (ID: id)`-style output. Since the Go MCP server produces that output, and Go regex closely mirrors Python here, keep patterns identical and **test each against a sample output**.
- **Path resolution in `processes.py`:** it reads `resources/documents/...` relative to `MCP_WORK_DIR`. In Go, resolve the same dir from an env var / fixed relative path.
- **`confluence.py` + `messages.py` share Confluence URL construction:** `{CONFLUENCE_BASE_URL}/pages/viewpage.action?pageId={id}`. Centralize this once in Go and use it in both places (Python had it duplicated).

---

## 5. `messages.py` — orchestration + LLM

This is the router that ties it together. Port `MessageRequest`/`MessageResponse`, `send_message`, `build_system_prompt`, `detect_no_useful_context`, `get_llm_answer`.

### 5.1 `build_system_prompt`
Copy the dual-identity system prompt verbatim (SuperSonicIQ + Friendly Agent). It's a string literal — just embed it. Keep the citation instructions (`=== RAG Document Search` / `[[link]]`) intact because they steer the LLM.

### 5.2 `detect_no_useful_context`
Port exactly. It returns `true` when to skip context — logic: empty text; or most lines contain "error" with `<100` chars; but `false` if `=== RAG Document Search` present OR a source ends in `.pdf/.md/.docx/.doc`. This directly controls answer quality (avoids robotic "nothing found"). Keep the heuristics.

### 5.3 `confluence_links`
After `build_context`, turn `confluence_refs` → clickable URLs using `CONFLUENCE_BASE_URL`. Centralize (see §4). Feed the URLs back into context as a `=== Available Confluence Links` block.

### 5.4 `get_llm_answer` — LLM call
Python uses `litellm.completion(model=f"openai/{model}", api_key, base_url, temperature)`. The Go backend calls an **OpenAI-compatible HTTP endpoint** directly (no litellm needed):

- `model` (default `gpt-4o`), `api_key`, `temperature` (default `0.1`) from env.
- `LLM_BASE_URL` optional (local servers, e.g. Ollama `http://localhost:11434/v1`).
- POST `{model, messages, temperature}` → `{choices:[{message:{content}}]}`.
- On any error, return `"Sorry, I encountered an error: ..."` (keep the message — Wails may display it).
- Keep the two-message shape: `[{system prompt}, {context-or-not} + user question]`.

**Decision:** use `net/http` + `encoding/json` (or `openai-go`/`ollama-go` if you already depend on one). Do **not** pull litellm-equivalent breadth; one OpenAI-compatible client is enough.

---

## 6. `mcp_client.py` → Go MCP client

Port the **behavior**, not the class:

1. **Subprocess lifecycle** — start the Go MCP binary with `-work-dir <dir>`; run stdio handshake (`initialize`); sleep 2s; `list_tools` to verify readiness; store the session.
   - In Go: `os/exec` to launch the binary, pipe `stdin`/`stdout`, drive the MCP JSON-RPC protocol with `mark3labs/mcp-go/client` (which implements stdio + JSON-RPC). Verify the installed MCP-go version exposes a stdio client + `CallTool`.
2. **`call_tool(name, args)`** → returns concatenated text content (`item.text`).
3. **`list_tools()`** → returns tool list.
4. **Singleton** `get_mcp_manager()` — Go `sync.Once` / package-level var.
5. **Path resolution** — keep the work-dir resolution: env `MCP_WORK_DIR` (absolute) → else `..` up from a known anchor → else `<work>/mcp-server/supersoniciq-mcp-server`. Windows `.exe` suffix handling → replicate.
6. **Env passing** — inherit `os.Environ()`.
7. **`_clean_env_value`** (strip control chars + surrounding quotes) — port the helper; env parsing is a common failure point.

**Verify before porting:** confirm `mark3labs/mcp-go` in `mcp-server/go.mod` has a usable **client** package (the server side already exists). If not, add the client dep.

---

## 7. In-memory stores: `auth.py` + `conversations.py`

- **`auth.py`:** `users_db` map; `register` (reject dup username, hash with bcrypt, return `{user_id,username,email}`); `login` (bcrypt verify, return `{access_token,token_type}`); `me` (return first user). Keep IDs `USR-NNNN`. Use `golang.org/x/crypto/bcrypt`.
  - ⚠️ Keep the **demo posture** as-is for now (unsigned token, in-memory users) unless hardening is explicitly in scope — don't invent a JWT system mid-port.
- **`conversations.py`:** `conversations_db` map; `list` (returns `[]` of values), `create` (`CONV-NNNN`, fixed timestamps `2024-12-01T00:00:00Z`), `get` (404 if missing), `delete` (`{message:"Conversation deleted"}`). **Keep the exact ID format and timestamp** — the Wails sidebar renders them.

Both are trivial maps in Go; keep response shapes byte-for-byte.

---

## 8. `rag.py` + `pwa.py`

### 8.1 `rag.py` — the open risk (§4.5)
ChromaDB (SQLite-backed vector store) + `sentence-transformers` (`all-MiniLM-L6-v2`, 384-dim) + an offline CLI indexer. See §4.5 for the decision. Once resolved, port `rag_store.search(query, limit=5)` and `GET /api/rag/categories`/`POST /api/rag/search`.

### 8.2 `pwa.py`
If Python stays for static assets, skip. If Go owns the runtime, serve `index.html`/`manifest.json`/`sw.js`/icons from `frontend/dist` (embed with `go:embed`).

---

## 9. The one open decision — RAG in Go

`sentence-transformers` / `chromadb` have **no mature first-class Go equivalent**. Three options, pick one before starting:

| Option | Effort | Notes |
|---|---|---|
| **A. Keep RAG as a Python micro-service**, Go calls it over HTTP (`/rag/*`) | Low | Smallest Go surface; adds a new service boundary. RAG stays behind Python. |
| **B. External embedding API** (OpenAI `text-embedding-3-small`, or the local Ollama/llama-cpp embedding endpoint) | Medium | Go owns the store (embeddings via HTTP). Embedding model decoupled from the backend. |
| **C. Port embeddings to Go** (ONNX runtime + `all-MiniLM-L6-v2` model, or a Go transformer lib) | High | Full self-contained Go. Real engineering; model parity must be verified (embeddings differ between libs). |

**Recommendation:** for a hackathon-fast port, **Option A** (keep RAG in Python) minimizes Go risk while still removing Python from the MCP/Confluence/documents/forms/skills/processes/messages path. Port everything *except* `rag.py`, and have Go call a small standalone `/rag/*` service. Decide now — it gates the `context_builder` port because `build_context` queries RAG.

---

## 10. Bootstrap — `main.py` → Go `main()`

Replicate the app setup:
- `load_dotenv()` — `joho/godotenv` or `spf13/viper/env`. Read the **same** env keys as `src/backend/.env` (`HOST`, `PORT`, `DEBUG`, `LLM_*`, `CONFLUENCE_*`, `MCP_*`, `RAG_ENABLED`, etc.).
- FastAPI app → Go router building `/api/*` sub-routes (mirror the mount structure in `main.py`).
- **CORS:** currently `allow_origins=["*"]` with credentials enabled — reproduce the same policy so the Wails app works unchanged. (Harden only if in scope.)
- Server: `uvicorn ... --host HOST --port PORT` → Go `http.Server{Addr: HOST:PORT}` (default `0.0.0.0:8000`).
- Start the MCP manager once at boot (like the Python singleton), shut it down on signal.

---

## 11. Module map (where every Python file goes)

| Python file | Go location | Effort | Parity risk |
|---|---|---|---|
| `main.py` | `main.go` / `internal/server/` | L | low |
| `routers/domains.py` | `internal/routers/domains.go` | S | low |
| `routers/messages.py` | `internal/routers/messages.go` | H | **high** (system prompt + heuristics) |
| `context_builder.py` | `internal/context/` | H | **high** (keywords, ordering, concurrency) |
| `domain_config.py` | `internal/domain/` | M | medium (JSON field names) |
| `mcp_client.py` | `internal/mcp/` | M | medium (stdio handshake) |
| `routers/confluence.py` | `internal/routers/confluence.go` | S | low |
| `routers/documents.py` | `internal/routers/documents.go` | S | medium (regex) |
| `routers/forms.py` | `internal/routers/forms.go` | S | medium (regex) |
| `routers/skills.py` | `internal/routers/skills.go` | S | medium (regex) |
| `routers/processes.py` | `internal/routers/processes.go` | M | medium (JSON path) |
| `routers/conversations.py` | `internal/routers/conversations.go` | S | low (IDs, timestamps) |
| `routers/auth.py` | `internal/routers/auth.go` | S | low (bcrypt) |
| `routers/rag.py` | *see §4.5* | — | — |
| `rag.py` | *see §4.5* | — | — (open risk) |
| `routers/pwa.py` | *see §4.5* | S | low |

Effort: S = small, M = medium, H = high.

---

## 12. Test plan — contract parity (the definition of done)

The Wails frontend is already built against the current shapes. **The Go backend must return byte-compatible responses.** Verify with the Wails app as the oracle.

**Automated contract tests (`go test`):** for each endpoint, assert the exact response fields the frontend reads:
- `/api/domains` → each domain has `key`, `display_name`, `icon`, `sub_categories` with `display_name`/`icon`.
- `/api/messages` → has `answer`, `sources`, `confluence_links[]` with `title`+`url`.
- `/api/conversations` list/create/get/delete → `id` is `CONV-NNNN`, `messages` is `[]`, timestamps present.
- `/api/{documents,forms,skills,processes}` → `parse_*` regexes produce the same objects from sample MCP output.
- `/api/auth/{register,login,me}` → `user_id` is `USR-NNNN`; login returns `access_token`.
- `POST /api/messages` → `answer` never contains a raw `"MCP tool ... error"` when tools succeed; when all fail it returns a friendly error (parity with `detect_no_useful_context`).

**Keyword-extraction unit tests:** feed known messages and assert the exact keyword list from `extract_keywords_from_message` + `get_search_terms` ordering. This is the cheapest way to catch a drift in the brain during port.

**MCP client test:** start the existing `mcp-server` binary, run `confluence_search`, `documents_search`, `forms_search`, `skills_search`, `process_search`, assert each returns non-error text and the parsers extract objects.

**End-to-end (manual):** launch Wails app + Go backend; select each domain+sub-category; send a question; confirm the answer + expandable RAG/Confluence sources render identically to Streamlit.

---

## 13. Phased sequence

1. **Bootstrap** — Go `main()` loads env, mounts `/api/*`, CORS, HTTP server, starts MCP manager. (1 day)
2. **Pure-routing routers** — `domains`, `conversations`, `auth`, `documents`, `forms`, `skills`, `processes`, `confluence`. These call MCP; no brain. (2 days)
3. **The brain** — `domain_config` + `context_builder` (keywords, ordering, routing matrix, goroutine concurrency). (2 days)
4. **Messages** — `messages.py` port: system prompt, `detect_no_useful_context`, Confluence URL building, LLM call. (2 days)
5. **RAG decision + wiring** — implement §4.5 (Option A/B/C), then `build_context` RAG branch. (varies)
6. **Contract tests** — automate §12. (2 days)
7. **Wails parity pass** — run the desktop app against the Go backend, fix any shape drift. (1–2 days)

**Total:** ~2 weeks for a single developer, dominated by §3/§4 and the RAG decision.

---

## 14. Invariants to protect (do not "improve" during port)

- **No endpoint-shape changes.** The Wails app is built against these. Loose coupling: Go replaces Python behind the same boundary.
- **Exact response field names & types** at every frontend boundary (`display_name`, `sub_categories`, `confluence_links`, `CONV-NNNN`, `sources`, `answer`).
- **Keyword ordering & stop-words** — a product decision, verbatim.
- **Error-string formats** (`MCP tool 'X' error:`, `=== RAG Document Search`, `=== Available Confluence Links`) — the answer-quality heuristics key off these.
- **Confluence URL format** and per-domain `confluence_spaces` scoping.
- **Demo auth posture** unless hardening is explicitly in scope.

---

## 15. What changes (and why)

| Concern | Python (current) | Go (target) |
|---|---|---|
| HTTP | FastAPI + routers | `net/http` + mux |
| MCP client | `mcp` stdio (`mcp_client.py`) | `mark3labs/mcp-go` client stdio |
| LLM | `litellm` | `net/http` to OpenAI-compatible endpoint |
| bcrypt | `passlib[bcrypt]` | `golang.org/x/crypto/bcrypt` |
| env | `python-dotenv` | `joho/godotenv` |
| concurrency | `asyncio.gather` | goroutines + channels/`errgroup` |
| RAG embeddings | `sentence-transformers` + `chromadb` | **decide (§4.5)** |
| static/PWA | `pwa.py` (Python) | `go:embed` (if Python dropped for static) |
