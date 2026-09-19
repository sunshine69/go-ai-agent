# Adding a New Slash Command to the Go Backend

A reference implementation of a complete, end-to-end slash command is
`/ragdir` (the RAG working-directory command), with `/mcpdir` as an even simpler
variant and `/mcp` as a stateful variant. This file documents the pattern so the
next command can be added by following the same layers.

## The command surface

A slash command has **five layers**, from the user's keyboard to the database:

```
User types /ragdir rag_data   →   App.tsx.handleSlashCommand
                                   →   api.ts setRAGWorkdir()   (frontend)
                                   →   routers/ragdir.go handleSet (HTTP POST /api/ragdir)
                                   →   db.Settings.Set(uid, "ragDBPath", value)  (persistence)
                                   →   ragmanager.SetUIDDir(uid, value)  (runtime effect)
```

There are two flavours of command, and you should pick the one that matches the
feature:

| Flavour | Example | Stores a setting in `user_settings`? | Needs a Manager/registry? |
|---------|---------|:---:|:---:|
| **DB-backed setting** | `/ragdir`, `/mcpdir`, `/ctx` | ✅ | Sometimes (only if it changes runtime state, like which store to open) |
| **Ephemeral query** | `/mcp` (reports status) | ❌ | Reads a shared Manager at request time |

For a **new setting that must persist and/or change runtime behaviour**, follow
the `/ragdir` recipe below. For a pure status/report command, follow `/mcp`.

---

## Layer 1 — Backend HTTP handler (`internal/routers/*.go`)

Each DB-backed command lives in its **own file** in `internal/routers/`. The
naming convention is `<feature>.go` (e.g. `ragdir.go`, `mcpdir.go`). A handler is
a small struct with a constructor:

```go
// Package routers — ragdir.go: per-user RAG DB directory endpoint
// (GET/POST /api/ragdir), backed by the user_settings table under the key
// "ragDBPath".
package routers

import (
	"net/http"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragmanager"
)

// Response type — the exact JSON shape sent to the SPA.
type ragdirResponse struct {
	Value string `json:"value"`
}

// Handler struct — holds whatever the handler needs (the DB, managers, etc.).
type ragdirHandler struct {
	db       *db.DB
	rmanager *ragmanager.Manager
}

func newRagdirHandler(dbStore *db.DB, rmanager *ragmanager.Manager) *ragdirHandler {
	return &ragdirHandler{db: dbStore, rmanager: rmanager}
}
```

### The shared boilerplate in every handler

Every handler method repeats the same **4-step body-guard pattern**:

```go
func (h *ragdirHandler) handleSet(w http.ResponseWriter, r *http.Request) {
	// 1. Method check
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// 2. Auth — extract and verify the caller
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// 3. Decode + validate the request body
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	value := strings.TrimSpace(req.Value)

	// 4. Domain validation + persistence + runtime effect
	if value != "" && !ragmanager.IsValidRagDir(value) {
		writeError(w, http.StatusBadRequest,
			"ragdir must be a relative directory with no '..' component")
		return
	}
	if err := h.db.Settings.Set(uid, "ragDBPath", value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save rag dir")
		return
	}
	// ...notify the manager so next request uses the new value...
	writeJSON(w, http.StatusOK, ragdirResponse{Value: value})
}
```

Note the four shared helpers it depends on (all defined in `routers.go`):

| Helper | Defined in | What it does |
|--------|-----------|--------------|
| `writeJSON(w, status, v)` | `routers.go` | Marshals `v`, sets `Content-Type: application/json`, writes `status` |
| `writeError(w, status, msg)` | `routers.go` | Writes `{"detail": msg}` in the Python-FastAPI error shape |
| `decodeBody(r, v)` | `routers.go` | `json.NewDecoder(r.Body).Decode(v)` |
| `currentID(r)` | `middleware.go` | Extracts & verifies `Authorization: Bearer <jwt>`, returns the int64 user id |
| `currentIDOr(r, fallback)` | `middleware.go` | Like `currentID` but returns the fallback int64 instead of a bool |

### GET — "list" the current value

```go
func (h *ragdirHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	value := ""
	if v, valid := resolveDefaultRAGDBPath(h.db, uid); valid {
		value = v
	}
	writeJSON(w, http.StatusOK, ragdirResponse{Value: value})
}
```

The `resolveDefault*` helper is the single source of truth for reading the
setting back. It returns `(value, valid)` and is also consulted by the
`main.go` startup code when seeding the default (user-0) state.

### Validation helpers

Validation lives in the feature's own package and is exported for reuse by the
routers layer and its tests:

```go
// in ragmanager/ragmanager.go
func IsValidRagDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	return isValidWorkDir(dir)
}
```

The rule is: **relative, no `..` component, not `.`**. A not-yet-existing
directory is **accepted** (the manager lazily creates it). See
`internal/mcpclient/pathutil.go` for the canonical `isValidWorkDir` implementation
if you need to copy it.

---

## Layer 2 — Register the routes (`internal/routers/routers.go`)

The `Handlers` struct is the dependency-injection bundle passed to every handler.
Two things must change here:

### 2a. Add the handler to the shared `Handlers` struct + constructor

For handlers that need a Manager or extra dependencies, add a field to `Handlers`
and thread it through `NewHandlers`:

```go
type Handlers struct {
	// ... existing fields ...
	rmanager *ragmanager.Manager   // field added for /ragdir
}

func NewHandlers(
	mcpManager *mcpclient.MCPManager,
	llmClient *llm.Client,
	rag *ragstore.RAGStore,
	ragManager *ragmanager.Manager,   // new constructor param
	cfg *config.Config,
	db *db.DB,
	frontend *serving.Server,
) Handlers {
	return Handlers{
		// ... existing fields ...
		rmanager: ragManager,           // assign new field
	}
}
```

> If your command **only** needs the DB (like `/mcpdir`), you can skip 2a and just
> store what it needs directly in its handler struct via `newXHandler(db)`.

### 2b. Instantiate + register the routes in `ServeMux()`

```go
func (h Handlers) ServeMux() http.Handler {
	mux := http.NewServeMux()
	// ... other handlers ...
	ragdir := newRagdirHandler(h.DB, h.ragManager)   // instantiate

	// Register GET + POST. Every command registers both. requireAuth wraps them.
	mux.HandleFunc("GET /api/ragdir", requireAuth(ragdir.handleList))
	mux.HandleFunc("POST /api/ragdir", requireAuth(ragdir.handleSet))
	// ...
}
```

The Go 1.21+ method pattern `"POST /api/ragdir"` binds the handler to both the
path **and** the method. Always register **both** `GET` and `POST` variants.

---

## Layer 3 — Persist to the DB (`internal/db/settings_repo.go`)

The persistence layer is already generic and shared. You do **not** write a new
table. You only pick a **setting key** string and call the repo:

```go
// db.Settings.Set(uid, key, value)  — INSERT ... ON CONFLICT(user_id, key)
// db.Settings.Get(uid, key)         — reads one key back
// db.Settings.GetAll(uid)           — reads every key (used by /settings)
```

The `user_settings` table schema (from `internal/db/db.go`) is:

```sql
CREATE TABLE IF NOT EXISTS user_settings (
	user_id BIGINT NOT NULL,
	key       TEXT NOT NULL,
	value     TEXT NOT NULL,
	PRIMARY KEY (user_id, key)
);
```

> **Key naming convention:** camelCase (`ragDBPath`, `mcpWorkdir`, `ctxLimit`).
> Store **text**; decode to int/bool/etc. at the call site.

For a command that must affect the default (user-0) server at **startup**, also add
a `resolveDefault*` helper and call it from `main.go` when building the manager.

---

## Layer 4 — Runtime effect (the "Manager" pattern)

If the setting must change **runtime behaviour** (which RAG store to open, which
working directory a child process runs in), the value is stored in memory in a
per-user **registry**. This is the `Manager` pattern:

### For `/ragdir` — `ragmanager.Manager`

```go
// SetUIDDir records uid → rawDir so the next message re-opens that store.
h.rmanager.SetUIDDir(uid, value)
```

The request handler in `internal/routers/messages.go` resolves the right store
at call time via `ragManager.Client(currentUserIDOr(r, 0))` and passes the
result into the `ContextBuilder` — so the command's effect is applied **lazily**
on the next message with no rebind needed.

On **startup**, `main.go` calls `ragMgr.LoadFromDB(d)` to rehydrate the
uid→rawDir map from the persisted `user_settings` table, so a `/ragdir` choice
survives a restart. Add the same `LoadFromDB(d)` call at the end of `main()` for
a new command whose registry must survive restart.

### For `/mcp` — `mcpclient.MCPManager` (ephemeral, no DB)

`/mcp` does **not** persist to `user_settings`. It reads/writes the shared
`mcp` Manager directly at request time via `Handlers.mcp.Set(uid, manager)`, and
reports status via `Handlers.mcp.Client(uid)` in the GET handler. Follow the
`mcp.go` file if you add a second stateful MCP endpoint.

> **Rule of thumb:** persist to `user_settings` when the value should survive a
> restart **and** be per-user; use an in-memory Manager when the state is
> inherently runtime-only (a live connection, an open file handle, …). You can
> do both — store the setting *and* update the Manager — which is exactly what
> `/ragdir`'s `handleSet` does.

---

## Layer 5 — Frontend SPA wiring

### 5a. `api.ts` — typed HTTP wrappers

Add a typed interface + `get`/`set` (or the single-action) wrapper. Naming
mirrors the endpoint:

```ts
// RAGWorkdir matches the ragdirResponse returned by GET/POST /api/ragdir.
export interface RAGWorkdir { value: string; }

export async function getRAGWorkdir(baseUrl: string): Promise<RAGWorkdir> {
  return getJSON<RAGWorkdir>(baseUrl, "/api/ragdir");
}

export async function setRAGWorkdir(baseUrl: string, value: string): Promise<RAGWorkdir> {
  return postJSON<RAGWorkdir>(baseUrl, "/api/ragdir", { value });
}
```

### 5b. `App.tsx` — wire the command into `handleSlashCommand`

**Step 1 — Load the initial value on auth** (mirror the existing effects):

```tsx
useEffect(() => {
	if (!showAuthedUI) return;
	getRAGWorkdir(API_BASE).then((w) => setRAGDirState(w.value)).catch(() => undefined);
}, [showAuthedUI]);
```

**Step 2 — Add a `case` in the `switch` inside `handleSlashCommand`:**

```tsx
case "/ragdir":
  // arg is the raw RAG directory value; omit the arg to report the current value.
  void handleRAGDir(arg);
  break;
```

**Step 3 — Add the handler function** (mirror `handleRAGDir`):

```tsx
const handleRAGDir = async (value?: string) => {
	const v = (value ?? "").trim();
	try {
		const updated = v === ""
			? await setRAGWorkdir(API_BASE, "")
			: await setRAGWorkdir(API_BASE, v);
		setRAGDirState(updated.value);
		if (updated.value === "") {
			appendFeedback("ok", "RAG working directory cleared.");
		} else {
			appendFeedback("ok", `RAG working directory set to: ${updated.value}`);
		}
	} catch (e) {
		appendFeedback("error", "Failed to set RAG working directory: " + (e instanceof Error ? e.message : String(e)));
	}
};
```

**Step 4 — Import the new api functions** at the top of `App.tsx`:

```ts
import { ..., getRAGWorkdir, setRAGWorkdir } from "./utils/api";
```

**Step 5 — Register the command in `COMMAND_HELP_TEXT`** so `/help` documents it.

### 5c. `appendFeedback` — how command results render

`appendFeedback(type, text)` appends an assistant message tagged with
`error: "command_result:ok"` or `"command_result:error"`, which `MessageBubble`
renders in a distinct style. This makes the result **persist** (not a toast that
auto-dismisses). Use it for every command's user-facing output.

---

## Testing (`internal/routers/*_test.go`)

Follow `mcpdir_test.go` exactly. The pattern:

1. Open an in-memory SQLite DB: `db.Open(":memory:", "sqlite3")`.
2. Seed an admin user: `d.SeedAdmin("admin", "admin@example.com", "1qa2ws")`.
3. Build `Handlers{DB: d, Cfg: config.Load("")}` and `handlers.ServeMux()`.
4. **Assert routes are registered** (the `requireRegistered` helper) — this
   catches a missing `mux.HandleFunc` registration.
5. **Assert 401 for anonymous** GET + POST.
6. Login, extract the `access_token`, attach `Authorization: Bearer <token>`.
7. Drive GET (expect empty initially), POST (valid value), POST (invalid value →
   400), POST (empty value → clears), and verify the round-trip.
8. **Verify persistence**: `d.Settings.Get(uid, "yourKey")`.

---

## The complete checklist for a new DB-backed setting command

- [ ] **Backend handler** `internal/routers/<feature>.go` — `handleList` (GET),
      `handleSet` (POST), response struct, `newXHandler` constructor,
      a `resolveDefault*` reader, domain validation.
- [ ] **Route registration** in `routers.go` `ServeMux()` — both GET + POST,
      `requireAuth`-wrapped. Add a field to `Handlers` + `NewHandlers` if it needs
      a Manager.
- [ ] **Setting key** chosen in `user_settings` (camelCase), persisted via
      `db.Settings.Set(uid, key, value)`, read via `db.Settings.Get`.
- [ ] **Manager** (if runtime effect) — `SetUIDDir`/`Client` + `LoadFromDB` at
      startup in `main.go`.
- [ ] **Validation helper** exported from the feature package (copy
      `isValidWorkDir` if new).
- [ ] **Frontend** — `api.ts` wrappers (`get`/`set` + typed interface),
      `App.tsx` case in `handleSlashCommand`, handler function, `useEffect` load
      on auth, import, and `COMMAND_HELP_TEXT` entry.
- [ ] **Test** in `internal/routers/<feature>_test.go` following `mcpdir_test.go`.

---

## Commands that follow this pattern (quick reference)

| Command | File | Setting key | HTTP endpoint | Manager |
|---------|------|-------------|---------------|---------|
| `/ctx [N]` | `settings.go` | `ctxLimit` | `GET/POST /api/settings` | ❌ |
| `/mcpdir [path]` | `mcpdir.go` | `mcpWorkdir` | `GET/POST /api/mcpdir` | ❌ |
| `/ragdir [path]` | `ragdir.go` | `ragDBPath` | `GET/POST /api/ragdir` | ✅ `ragmanager` |
| `/mcp [spec\|off]` | `mcp.go` | *(ephemeral)* | `GET/POST /api/mcp` | ✅ `mcpclient` |
| `/clear` | *(App.tsx only)* | — | — | ❌ |
| `/help` | *(App.tsx only)* | — | — | ❌ |

Use `/ragdir` as your primary template — it has both the DB layer **and** the
Manager layer, covering everything a complex command needs. Use `/mcpdir` when the
command is a simple setting with no runtime registry.
