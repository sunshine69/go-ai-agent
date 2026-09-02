# Hand-off: Multi-User / Database Auth (IN PROGRESS — build is broken)

**Date:** 2025
**Branch:** `testing`
**Status:** ⚠️ Do NOT push. Working tree is mid-refactor; backend **does not compile**.
**Goal of this branch:** add real, **DB-backed multi-user** auth (was previously a single in-memory demo user).

---

## TL;DR (what the next agent must do)

The working tree rewrites `auth.go` and `conversations.go` to be **backed by the `db` package** (users,
conversations, messages persisted to SQLite/Postgres), wiring a new `db.DB` instance into the router via
`routers.Handlers`. The rewrite is **incomplete and does not compile**. To finish:

1. **Fix the 6 compile errors** (see below) — add a missing `db` import, wire the `db.DB` from `main.go`
   into `routers.Handlers`, and reconcile the `authHandler`/`conversationsHandler` constructors with
   `routers.go`.
2. **Re-add `handleRegister`** — `routers.go` still routes `/api/auth/register` to `auth.handleRegister`,
   but that method was **deleted** from `auth.go` during the refactor.
3. **Wire the DB in `main.go`** — open the DB (`db.Open`), seed an admin, pass `db.DB` into `routers.Handlers`.
4. **Wire the new endpoints** in `routers.go`: `/api/auth/users`, `/api/auth/users/{id}`, `/api/auth/logout`,
   `/api/auth/me/profile`, `/api/auth/me/profile/password` (handlers exist in `auth.go` but are **not registered**).
5. Run `go build ./...` from `src/backend-go/` and `go test ./...` until green.

---

## Current file states (working tree vs. committed)

| File | State |
|------|-------|
| `internal/db/*.go` | ✅ Done, looks complete. `DB`, `UserRepo`, `ConversationRepo`, `jwt.go` all present. No changes needed except exporting `getByID`. |
| `internal/config/config.go` | ✅ Done (this branch's own additions): added `DBPath`, `DBDriver` fields + `DB_PATH`/`DB_DRIVER` env resolution. |
| `internal/routers/auth.go` | ⚠️ Rewritten to be DB-backed (`authHandler{db *db.DB}`), all new handlers written, **but `handleRegister` was lost** and it's **not wired** in `routers.go`. |
| `internal/routers/conversations.go` | ⚠️ Rewritten to be DB-backed (`conversationsHandler{db *db.DB}`), but **missing the `db` import**. |
| `internal/routers/middleware.go` | ✅ Done: `requireAdmin(r, h *authHandler, w)` and `h.db` access. |
| `internal/routers/routers.go` | ❌ Broken: calls `newConversationsHandler()` and `newAuthHandler()` with **no args**, but the constructors now require `*db.DB`. Also missing the new user/profile/logout routes. |
| `main.go` | ❌ Not done: never opens a DB, never seeds an admin, never passes a `db.DB` to `routers.Handlers`. |

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
