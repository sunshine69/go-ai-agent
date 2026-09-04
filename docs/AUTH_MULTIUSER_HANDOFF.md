# Hand-off: Multi-User / Database Auth (IN PROGRESS — build compiles; runtime wiring pending)

**Date:** 2025
**Branch:** `testing`
**Status:** ⚠️ Do NOT push. Working tree is mid-refactor; backend **compiles and db tests pass**, but runtime DB wiring in `main.go` is incomplete — auth/conversations endpoints will get a nil `db.DB` and panic at runtime.**
**Goal of this branch:** add real, **DB-backed multi-user** auth (was previously a single in-memory demo user).

---

## TL;DR (what the next agent must do)

The working tree rewrites `auth.go` and `conversations.go` to be **backed by the `db` package** (users,
conversations, messages persisted to SQLite/Postgres), wiring a new `db.DB` instance into the router via
`routers.Handlers`. The rewrite is now **incomplete but compiles and tests pass**. What remains: wire the DB in `main.go` (item 3) and register the new endpoints in `routers.go` (item 4). Item 1 (compile errors) and item 2 (re-add `handleRegister`) are **done**. To finish:

1. **Fix the 6 compile errors** (see below) — add a missing `db` import, wire the `db.DB` from `main.go`
   into `routers.Handlers`, and reconcile the `authHandler`/`conversationsHandler` constructors with
   `routers.go`.
2. **Re-add `handleRegister`** — ✅ **DONE.** `handleRegister` has been re-added to `auth.go` and routes to `/api/auth/register`.
3. **Wire the DB in `main.go`** — open the DB (`db.Open`), seed an admin, pass `db.DB` into `routers.Handlers`. ❌ **PENDING.** This is the current blocker: without it, `h.DB` is nil and every auth/conversations endpoint panics at runtime.
4. **Wire the new endpoints** in `routers.go`: `/api/auth/users`, `/api/auth/users/{id}`, `/api/auth/logout`,
   `/api/auth/me/profile`, `/api/auth/me/profile/password` (handlers exist in `auth.go` but are **not registered**). ❌ **PENDING** — do this after item 3 so the endpoints actually work.
5. Run `go build ./...` from `src/backend-go/` and `go test ./...` until green. ✅ Partial: `go build ./internal/db ./internal/routers .` and `go test ./internal/db/...` are already green. Re-run the full build after item 3.

---

## Current file states (working tree vs. committed)

| File | State |
|------|-------|
| `internal/db/*.go` | ✅ Done, looks complete. `DB`, `UserRepo`, `ConversationRepo`, `jwt.go` all present. No changes needed except exporting `getByID`. |
| `internal/config/config.go` | ✅ Done (this branch's own additions): added `DBPath`, `DBDriver` fields + `DB_PATH`/`DB_DRIVER` env resolution. |
| `internal/routers/auth.go` | ✅ Rewritten to be DB-backed (`authHandler{db *db.DB}`), all new handlers written **including `handleRegister` (re-added)**. Handlers are **not yet registered** in `routers.go` (see item 4). |
| `internal/routers/conversations.go` | ✅ Rewritten to be DB-backed (`conversationsHandler{db *db.DB}`). The `db` import is now present. |
| `internal/routers/middleware.go` | ✅ Done: `requireAdmin(r, h *authHandler, w)` and `h.db` access. |
| `internal/routers/routers.go` | ✅ Constructor calls fixed: `newConversationsHandler(h.DB)` / `newAuthHandler(h.DB)`; `Handlers.DB` field added. ❌ Still missing registration of the new user/profile/logout routes (item 4). |
| `main.go` | ⚠️ **IN PROGRESS:** `routers.Handlers` struct is created but `DB` field is **never set** — `db.Open`, `SeedAdmin`, and `DB: d` are all missing. Must be done to make auth/conversations endpoints operational. |

---

## Compile errors (confirmed)

Run from `src/backend-go/`:
```
go build ./internal/db ./internal/routers ./
```
Errors:
```
internal/routers/conversations.go:20:6:   undefined: db          # missing "db" import
internal/routers/conversations.go:23:34:  undefined: db
internal/routers/conversations.go:135:47: undefined: db
internal/routers/auth.go:122:23:          h.db.Users.getByID undefined (cannot refer to unexported method getByID)
internal/routers/auth.go:154:22:          cannot use h.db (*db.DB) as *authHandler value in argument to requireAdmin
internal/routers/auth.go:171:22:          (same)
internal/routers/auth.go:198:22:          (same)
internal/routers/auth.go:229:23:          h.db.Users.getByID undefined (same root cause)
internal/routers/auth.go:260:21:          (same)
internal/routers/auth.go:285:25:          (same)
```

### Root causes & fixes

1. **`conversations.go` missing `db` import** — add `github.com/stevek/go-ai-agent/backend-go/internal/db`
   to the import block.
2. **`requireAdmin` takes `*authHandler`, but callers pass `h.db` (`*db.DB`)** — every `requireAdmin(r, h.db, w)`
   call must become `requireAdmin(r, h, w)` (pass the handler, not its db field). `middleware.go` already does
   `h.db.Users.getByID(...)`.
3. **`getByID` is unexported** and called across packages (`auth.go`, `middleware.go`). Export it as `GetByID`
   in `user_repo.go` and update all callers.
4. **`routers.go` constructor calls** — change `newConversationsHandler()` → `newConversationsHandler(h.db)`
   and `newAuthHandler()` → `newAuthHandler(h.db)`, which means `routers.Handlers` must carry a `db *db.DB`.
5. **`main.go` wiring** — open the DB, seed admin, store it in `routers.Handlers`.

---

## Missing wiring to add in `routers.go`

`routers.go` currently registers:
```
/api/auth/register   ← handleRegister is DELETED, must re-add
/api/auth/login      ← ok
/api/auth/me         ← ok
```

`auth.go` defines these handlers but they are **not registered**:
- `handleLogout` (`POST /api/auth/logout`)
- `handleUsers` (`GET /api/auth/users`)
- `handleCreateUser` (`POST /api/auth/users`)
- `handleDeleteUser` (`DELETE /api/auth/users/{id}`)
- `handleProfile` (`GET /api/auth/me/profile`)
- `handleProfileUpdate` (`PATCH /api/auth/me/profile`)
- `handlePasswordChange` (`POST /api/auth/me/profile/password`)

Register all of them. `routers.Handlers` needs a `db *db.DB` field passed through from `main.go`.

---

## `main.go` wiring (todo)

```go
d, err := db.Open(cfg.DBPath, cfg.DBDriver)   // "sqlite3" default
if err != nil { log.Fatalf(...) }
defer d.Close()
if _, err := d.SeedAdmin("admin", "admin@example.com", "<default>"); err != nil { ... }

h := routers.Handlers{
    ...
    DB: d,   // <-- add field to Handlers struct
}
```
Add a `DB *db.DB` field to the `routers.Handlers` struct so the constructors receive it.

---

## How to verify

```sh
cd src/backend-go
go build ./internal/db ./internal/routers ./
go test ./internal/db/...
```

Also confirm `handleRegister` (and every `handle*` referenced in `routers.go`) exists in `auth.go`
after wiring — the router references `auth.handleRegister`, which currently **does not exist**.

---

## Notes / gotchas

- The `db` package supports **SQLite** (default driver, `SetMaxOpenConns(1)` to avoid "database is locked")
  and **Postgres** (`DB_DRIVER=postgres`). Default DB path is `.sonic.db`.
- Auth is JWT HS256: `db.IssueToken` / `db.ParseToken`, `db.TokenExpiry = 720h`. Secret from `JWT_SECRET` / `JWT_SECRET_FILE`.
- `requireAdmin` requires `*authHandler`; do **not** change its signature — pass the handler instead of `h.db`.
- The old single-user behavior lives in the **parent commit** if a reference is needed.
- `db_test.go` (`TestDBLifecycle`) already exercises the `db` API — a good signal the repo layer is correct.

---

## Progress log

| Date | Item | Status |
|------|------|--------|
| (this session) | Document status + file-state table updated to reflect current working-tree state. | ✅ done |
| (this session) | Item 1 (compile errors) — verified all compile errors from handoff are fixed in working tree. | ✅ done |
| (this session) | Item 2 (`handleRegister` re-added) — verified `handleRegister` exists in `auth.go`. | ✅ done |
| (this session) | Item 5 partial — `go build ./internal/db ./internal/routers .` and `go test ./internal/db/...` are green. | ✅ partial |
| (this session) | **Item 3 (`main.go` DB wiring)** — **NOT STARTED.** `main.go` still never opens a DB, seeds an admin, or sets `DB:` on `routers.Handlers`. This is the current blocker. | 🚧 pending |
| (this session) | **Item 4 (register new auth routes)** — **NOT STARTED.** `routers.go` only registers `/api/auth/register`, `/api/auth/login`, `/api/auth/me`. Missing: logout, users (GET/POST/DELETE), profile (GET/PATCH), password change. | 🚧 pending |

### Next agent (continue from here)
1. **Item 3 — `main.go` DB wiring.** Add (before `h := routers.Handlers{`):
   ```go
   d, err := db.Open(cfg.DBPath, cfg.DBDriver) // "sqlite3" default
   if err != nil { log.Fatalf("db open: %v", err) }
   defer d.Close()
   if _, err := d.SeedAdmin("admin", "admin@example.com", "admin"); err != nil {
       log.Printf("seed admin: %v", err)
   }
   ```
   Then add `DB: d` to the `routers.Handlers{ ... }` literal. Need to import `github.com/stevek/go-ai-agent/backend-go/internal/db`.
2. **Item 4 — register new auth routes.** After the existing `/api/auth/me` line in `routers.go`:
   ```go
   mux.HandleFunc("/api/auth/logout", auth.handleLogout)             // POST
   mux.HandleFunc("/api/auth/users", auth.handleUsers)              // GET
   mux.HandleFunc("/api/auth/users", auth.handleCreateUser)         // POST (same path, different method)
   mux.HandleFunc("/api/auth/users/", auth.handleDeleteUser)        // DELETE /{id}
   mux.HandleFunc("/api/auth/me/profile", auth.handleProfile)       // GET
   mux.HandleFunc("/api/auth/me/profile", auth.handleProfileUpdate) // PATCH
   mux.HandleFunc("/api/auth/me/profile/password", auth.handlePasswordChange) // POST
   ```
3. **Re-verify** after items 3 & 4:
   ```sh
   cd src/backend-go
   go build ./internal/db ./internal/routers .
   go test ./internal/db/...
   ```
4. Run the backend once (with `nohup ./backend-go &`) and smoke-test `/api/auth/login` + `/api/auth/me` to confirm runtime wiring works.
