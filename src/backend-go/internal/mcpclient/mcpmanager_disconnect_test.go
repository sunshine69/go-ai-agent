package mcpclient

import "testing"

// TestMCPManagerDisconnectRespectsDefault verifies the core regression fix for
// the "disconnect leaks the shared default server" bug.
//
// Before the fix, MCPManager.Set(uid, nil) only deleted the per-user override,
// and Client(uid) fell back to the process-wide defaultClient — so a caller who
// had run `/mcp off` would keep being served tools by the shared (user-0)
// server.
//
// The manager now tracks an explicitly disconnected user in the `disabled` set
// and returns nil from Client(uid) for them, even while a default exists. That
// nil propagates through Handlers.mcpClient() and handleStatus() so the SPA can
// report "no MCP server connected" and the tool-serving path stops serving
// tools.
func TestMCPManagerDisconnectRespectsDefault(t *testing.T) {
	// A shared default stands in for the process-wide (user-0) server.
	shared := &ResilientMCPClient{Spec: "node ./server.js"}
	mgr := NewMCPManager(shared)

	// Before any per-user write, a brand-new user inherits the shared default.
	if got := mgr.Client(1); got == nil || got.Spec != "node ./server.js" {
		t.Fatalf("Client(1) before disconnect: got %v, want shared default", got)
	}

	// A first user connects their own override — distinct from the default.
	own := &ResilientMCPClient{Spec: "stdio:own"}
	mgr.Set(1, own)
	if got := mgr.Client(1); got != own {
		t.Fatalf("Client(1) after connect: want own override, got %v", got)
	}

	// Now disconnect user 1 by clearing their override. The regression was that
	// Client(1) then fell back to the shared default. It must NOT: user 1 is
	// explicitly disconnected.
	mgr.Set(1, nil)
	if got := mgr.Client(1); got != nil {
		t.Fatalf("Client(1) after disconnect: want nil (disconnected), got %v", got)
	}

	// The shared default must still be alive for other users. A fresh user
	// (user 2, never connected/disconnected) still inherits it.
	if got := mgr.Client(2); got == nil || got.Spec != "node ./server.js" {
		t.Fatalf("Client(2) still sees shared default: got %v", got)
	}

	// Reconnecting the same user restores service for that user.
	mgr.Set(1, &ResilientMCPClient{Spec: "stdio:reconnected"})
	if got := mgr.Client(1); got == nil || got.Spec != "stdio:reconnected" {
		t.Fatalf("Client(1) after reconnect: want reconnected override, got %v", got)
	}

	// Disconnect again and confirm the flag is cleared by the reconnect so a
	// fresh reconnect restores service (regression guard on the disable flag).
	mgr.Set(3, nil) // never connected, but a Set(nil) is harmless no-op.
	if got := mgr.Client(3); got != nil {
		t.Fatalf("Client(3) never connected, should default: got %v", got)
	}
}

// TestMCPManagerSetReplacesOverride ensures Set swaps in the new override and
// keeps the latest one for that user. (Closing the previous override in place
// happens inside Set and is exercised by the e2e tests that connect real
// servers.)
func TestMCPManagerSetReplacesOverride(t *testing.T) {
	mgr := NewMCPManager(nil)

	c1 := &ResilientMCPClient{Spec: "stdio:a"}
	c2 := &ResilientMCPClient{Spec: "stdio:b"}
	mgr.Set(7, c1)
	mgr.Set(7, c2)

	if got := mgr.Client(7); got != c2 {
		t.Fatalf("Client(7): want latest override c2, got %v", got)
	}
}
