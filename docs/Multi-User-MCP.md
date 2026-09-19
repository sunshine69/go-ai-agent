# Multi-User MCP Implementation Plan

**Date:** 2025
**Status:** 3.1 and 3.2 implemented; 3.3 pending.

---

## 1. Scope

Per-user control of MCP server lifecycle in the Go backend (`src/backend-go`), including:
- Explicit process kill on disconnect (fixes the stdio-process-kill bug).
- Per-user permission to start MCP.
- Multiple concurrent MCP processes, each with its own working directory.

---

## 2. Findings

### 2.1 stdio MCP process is NOT killed on `/mcp off` (confirmed bug)

**Chain:**
```
/mcp off
  → POST /api/mcp {action:"disconnect"}
  → routers/mcp.go  handleConnect()
  → m.h.Manager.Swap(nil)
  → mcpclient.go  ResilientMCPClient.Swap()
      if r.Inner != nil && other == nil {
          _ = r.Inner.Close()   // only place the process is touched
      }
  → ResilientMCPClient.Close()
  → MCPClient.Close()
  → pipeReadWriter.Close()      // lines 117-121
```

**Root cause:** `pipeReadWriter.Close()` only closes stdin/stdout and calls `cmd.Wait()`:
```go
func (p *pipeReadWriter) Close() error {
	p.in.Close()
	p.out.Close()
	return p.cmd.Wait()
}
```
`cmd.Wait()` **waits**, it does not **kill**. If the stdio MCP server does not exit on stdin EOF, the process is orphaned and keeps running; `cmd.Wait()` blocks until it dies (which may never happen).

**Impact:** stdio MCP servers — the child-process transport launched via `ConnectStdio`/`NewManager`/`Connect`. Not applicable to Streamable HTTP (`ConnectStreamableHTTP`), which spawns no child process.

### 2.2 No per-user permission to start MCP (confirmed)

- `/api/mcp` connects/disconnects with only an authenticated token — **no permission check** beyond login.
- `main.go` launches one global MCP server at startup from a single `MCP_WORK_DIR`.
- All users share one `Manager` (`Handlers.Manager`); a `Swap` by one user affects all.

### 2.3 No per-admin / per-working-directory MCP (confirmed)

- Working directory is fixed at startup (`cfg.MCPWorkDir`).
- No API accepts a `workdir` (or `toolexec`/`endpoint`) per session.
- No way for multiple admins to run separate MCP processes.

---

## 3. Proposed fixes

### 3.1 Fix stdio process kill (high priority)

`pipeReadWriter.Close()`:
```go
func (p *pipeReadWriter) Close() error {
	_ = p.in.Close()
	_ = p.out.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
			p.cmd.Wait()
		}
		return fmt.Errorf("killed stdio MCP after graceful-exit timeout")
	}
}
```
- Graceful-exit timeout: 5s (configurable).
- Add a regression test confirming the child process is gone after `Close()`.

### 3.2 Per-user permission to start MCP

- Add `allow_mcp` (or similar) flag to `db.User` / `UserRepo`.
- Gate `/api/mcp` connect behind the flag; admin always allowed.
- ✅ IMPLEMENTED. The `allow_mcp` flag is added to `db.User`/`UserView`, persisted in the `users` table (new column `allow_mcp`), repaired on existing DBs via `migrateSchema`, and threaded through `UserRepo.Insert`, `scanUser`, `toView`, and the admin create-user form (`authUserRequest.AllowMcp`).

- ✅ IMPLEMENTED. `POST /api/mcp` `connect` is gated: admins and any user with `allow_mcp` true may connect; everyone else receives `403 "MCP access is not permitted for your account"`. See the enforcement block at the top of the `connect` case in `routers/mcp.go`.

### 3.3 Multiple concurrent MCP processes

- Extend `Manager` to hold per-user clients (e.g. `users map[int64]*ResilientMCPClient`).
- New endpoints accept `workdir`/`toolexec`/`endpoint` overrides.
- `/mcp off` disconnects only the caller's own session.

---

## 4. Files to touch

| File | Change |
|------|--------|
| `internal/mcpclient/mcpclient.go` | `pipeReadWriter.Close()` kill-on-timeout |
| `internal/mcpclient/mcpclient.go` | `Manager` per-user clients (3.3) |
| `internal/db/user_repo.go` | `allow_mcp` field + access |
| `internal/routers/mcp.go` | guard connect, per-user connect/off |
| `internal/routers/routers.go` | wire new endpoints |
| `internal/config/config.go` | (optional) timeout knobs |

---

## 5. Testing

- **Regression:** child stdio process is gone after `/mcp off`.
- **Unit:** `Swap(nil)` closes the inner client without hanging.
- **Integration:** per-user permission enforcement; multiple admins running separate MCP processes in different working dirs.

---

## 6. Open questions

- Should permission be a new `allow_mcp` flag or reuse the `IsAdmin` flag?
- Max concurrent MCP processes per server (any cap)?
- Should non-admin users be restricted to a fixed workdir?
