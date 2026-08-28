# SupersonicIQ — Desktop Frontend (Go Wails)

**Status:** MVP scaffolded, awaiting the React UI port.

---

## What exists now

- Go module initialized, Wails scaffold with `main.go` / `app.go`
- React Vite scaffold in `frontend/` (TypeScript, React 19)
- Assets copied: Nunito font, logo
- Build targets defined in `wails.json`

**Missing:** The React app that renders the SupersonicIQ UX (the existing Streamlit JSX has been ported to `frontend/src/components/`).

---

## How to run

### 1. Wails dev (compiles Go, proxies React dev server + backend)

```bash
cd $(go env GOPATH)/bin
./wails dev
```

- React dev server runs on `http://localhost:5173` (configured in `vite.config.ts`)
- Wails exposes the same URL and proxies `/api/*` to the Python FastAPI backend
- Press `r` to rebuild the Go binary

### 2. Build and run locally

```bash
wails dev
# or, after a prod build:
wails dev
```

For a release build:

```bash
wails build
# the executable is at <build>/dist/SupersonicIQ/SupersonicIQ
```

### 3. Run the Python FastAPI backend separately

```bash
cd ../backend && python -m venv venv && source venv/bin/activate
pip install -r requirements.txt
uvicorn app.main:app --port 8000
```

---

## Frontend architecture

**State:** `src/stores/app.ts` — last domain, last sub‑category, last question, `isThinking`, `conversationId`. Persisted via `wails.SecureStorage` (on supported OS) + `localStorage`.

**Components:**

| File | Purpose |
|---|---|
| `App.tsx` | Root UI — domain pills / sub‑category pills / chat / history / sidebar |
| `DomainPill.tsx` | Domain‑level selection pills |
| `SubCategoryPill.tsx` | Sub‑category pills (inline, no reload) |
| `ChatInput.tsx` | Always‑available chat input; placeholder reflects scope |
| `MessageBubble.tsx` | Assistant answer with expandable RAG/Confluence sources |
| `Sidebar.tsx` | New conversation, current scope, quick actions, conversations list, last question |

---

## Backend contract

The frontend expects these exact paths and response shapes:

- `GET /api/domains` → `{ "domains": [ {key, display_name, icon, sub_categories: {…}}] }`
- `POST /api/messages` → `{ conversation_id, answer, sources, confluence_links }`
- `GET /api/conversations` → `{ "conversations": [ {id, title, …}] }`
- `/pwa/*` — served by the Python backend as‑is (PWA assets)

No endpoint‑shape changes allowed during the port; the Wails frontend is already built against them.

---

## Build targets (`wails.json`)

```json
{
  "frontend:install": "npm install",
  "frontend:build": "npm run build",
  "frontend:dev:watcher": "npm run dev",
  "frontend:dev:serverUrl": "auto",
  "author": { "name": "steve kieu", "email": "msh.computing@gmail.com" }
}
```

Wails automatically:
- Compiles the Go binary
- Serves the React dev server
- Proxies `/api/*` to the Python backend
- Rebuilds on source changes

---

## Current gaps

| Gap | Priority | Notes |
|---|---|---|
| React UI not yet rendering (pills, chat, sources) — code already created in `frontend/src/components/` | Critical — the UI is the last piece to port |
| Backend → MCP stdio path (stdio vs HTTP) — need to verify which is active | Medium — check `mcp_client.py` vs Docker env |
| PWA/static layer — reuse Python for assets (recommended) | Low — optional for now |
| Offline graceful handling (retry banner) | Low — polish |

---

## Next steps

1. Run `wails dev` — verify Wails compiles and serves the React dev server
2. Inspect the live URL — ensure domain pills, sub‑category pills, chat input, message history all render
3. Post a question — confirm the API calls work and answers appear with RAG/Confluence sources
4. Desktop polish (tray, auto‑update, themes, offline banner) — Phase 3
5. Hardening / next steps (Option B: Go‑own the brain) — Phase 4

---

*This plan preserves the SuperSonicIQ knowledge engine as‑is and swaps only the Streamlit UI for a Go Wails desktop frontend. Cross‑references: docs/architecture.md, docs/frontend-plan.md, RAG_ARCHITECTURE.md.*
