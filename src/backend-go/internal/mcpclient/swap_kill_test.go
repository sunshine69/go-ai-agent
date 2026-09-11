package mcpclient

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// minimalStdioMCPServer is a tiny stdio MCP server written in Python. It speaks
// just enough of the MCP JSON-RPC-over-stdio protocol for the Go client's
// Initialize()/Tools() handshake to succeed: it answers `initialize` and
// `tools/list`, and acknowledges `notifications/Initialized`. It lives on stdin
// until the parent process is killed.
func minimalStdioMCPServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_server.py")
	src := `import sys, json
def send(o):
    sys.stdout.write(json.dumps(o) + "\n")
    sys.stdout.flush()
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        m = json.loads(line)
    except Exception:
        continue
    meth = m.get("method")
    rid = m.get("id")
    if meth == "initialize":
        send({"jsonrpc": "2.0", "id": rid, "result": {"protocolVersion": "2025-06-18", "capabilities": {}, "serverInfo": {"name": "test-mcp", "version": "1.0"}}})
    elif meth == "notifications/Initialized":
        pass
    elif meth == "tools/list":
        tool = {"name": "echo", "description": "echo", "inputSchema": {"type": "object", "properties": {}}}
        send({"jsonrpc": "2.0", "id": rid, "result": {"tools": [tool]}})
        send({"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "not found"}})
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write mcp server: %v", err)
	}
	return path
}

func isAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false // already gone (or platform does not know it)
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}

// pidOf returns the OS PID of the child process backing an MCP client's stdio
// connection, or 0 if the connection is not a stdio pipe.
func pidOf(c *MCPClient) int {
	pw, ok := c.conn.(*pipeReadWriter)
	if !ok {
		return 0
	}
	if pw.cmd == nil || pw.cmd.Process == nil {
		return 0
	}
	return pw.cmd.Process.Pid
}

// TestSwapKillsOldProcess verifies the reconnect path does NOT orphan the old
// stdio MCP child. When a second server is connected while one is already live
// (the exact `/mcp mcp.exe` again-without-`/mcp off` scenario), Swap must close
// the previous inner client, whose Close() kills the child rather than leaving
// it running.
func TestSwapKillsOldProcess(t *testing.T) {
	srv := minimalStdioMCPServer(t)
	argv := []string{"python3", srv}

	r, err := NewResilientStdio(argv, "")
	if err != nil {
		t.Fatalf("new stdio client: %v", err)
	}
	if r.Inner == nil {
		t.Fatalf("expected a live inner client")
	}
	if got := len(r.Tools()); got != 1 {
		t.Fatalf("expected 1 tool after init, got %d", got)
	}

	oldPID := pidOf(r.Inner)
	if oldPID == 0 {
		t.Fatalf("could not resolve stdio child PID")
	}

	// Simulate the reconnect: a fresh client swapped in over the live one.
	other, err := NewResilientStdio(argv, "")
	if err != nil {
		t.Fatalf("new stdio client 2: %v", err)
	}
	r.Swap(other)
	if r.Inner == nil {
		t.Fatalf("expected inner after swap")
	}
	if pidOf(r.Inner) == oldPID {
		t.Fatalf("inner was not replaced after swap")
	}

	// The OLD child must be gone. Give Close()'s 5s graceful timeout + slack.
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if !isAlive(oldPID) {
			t.Logf("old child pid %d exited after swap", oldPID)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("old stdio MCP pid %d still alive after Swap — process leaked", oldPID)
}
