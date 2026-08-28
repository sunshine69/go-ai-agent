// Package mcpclient provides a stdio-bound client for the Go MCP server
// (mcp-server/). It drives the MCP JSON-RPC protocol over an in-process
// subprocess, mirroring the behaviour of the Python backend's mcp_client.py:
// the manager starts the MCP binary once at boot, runs the initialize
// handshake, verifies tool availability, and exposes call_tool / list_tools.
//
// CallTool results are returned as concatenated text content (item.text),
// identical to the mcp_client.CManager.call_tool contract consumed elsewhere.
package mcpclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 message types
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpCallToolResult mirrors the MCP tools/call response body.
type mcpCallToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// ---------------------------------------------------------------------------
// pipeReadWriter bridges an exec.Cmd's stdin/stdout into a single
// io.ReadWriteCloser, so the MCPClient can both write requests and read
// newline-delimited responses from the subprocess.
// ---------------------------------------------------------------------------

type pipeReadWriter struct {
	in  io.WriteCloser
	out io.ReadCloser
	cmd *exec.Cmd
}

func (p *pipeReadWriter) Read(b []byte) (int, error) { return p.out.Read(b) }

func (p *pipeReadWriter) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *pipeReadWriter) Close() error {
	p.in.Close()
	p.out.Close()
	return p.cmd.Wait()
}

// MCPClient is a minimal hand-rolled MCP stdio client. It mirrors the proven
// pattern in sunshine69/go-ai-chat/chat/mcp-client.go but is scoped to the
// tools the backend needs: initialize, tools/list, tools/call.
//
// A single MCPClient must not be used concurrently by multiple goroutines —
// callers must serialise access via the manager's mutex (or a client pool).
type MCPClient struct {
	mu      sync.Mutex
	conn    io.ReadWriteCloser
	scanner *bufio.Scanner
	nextID  int64
}

// send writes a single newline-terminated JSON-RPC request.
func (c *MCPClient) send(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.conn.Write(data)
	return err
}

// recv reads the next non-empty JSON-RPC response line. Notifications (no id)
// are skipped.
func (c *MCPClient) recv() (*jsonRPCResponse, error) {
	for c.scanner.Scan() {
		line := strings.TrimSpace(c.scanner.Text())
		if line == "" {
			continue
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		return &resp, nil
	}
	if err := c.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// call performs a synchronous JSON-RPC request/response.
func (c *MCPClient) call(method string, params interface{}) (*jsonRPCResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID + 1
	c.nextID = id
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.send(req); err != nil {
		return nil, err
	}

	for {
		resp, err := c.recv()
		if err != nil {
			return nil, err
		}
		if resp.ID == nil {
			continue // notification, ignore
		}
		switch v := resp.ID.(type) {
		case float64:
			if int64(v) == id {
				return resp, nil
			}
		case int64:
			if v == id {
				return resp, nil
			}
		}
	}
}

// Initialize runs the MCP handshake (initialize + notifications/initialized)
// and refreshes the cached tool list.
func (c *MCPClient) Initialize() error {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "go-ai-agent-backend",
			"version": "1.0.0",
		},
	}
	resp, err := c.call("initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}
	// Send the notifications/initialized handshake.
	if err := c.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}
	return c.refreshTools()
}

// refreshTools lists the tools exposed by the MCP server and caches the count.
func (c *MCPClient) refreshTools() error {
	resp, err := c.call("tools/list", nil)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}
	// Parse tools to surface the count for diagnostics.
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err == nil && len(result.Tools) > 0 {
		names := make([]string, len(result.Tools))
		for i, t := range result.Tools {
			names[i] = t.Name
		}
		fmt.Printf("[MCP] %d tools available: %s\n", len(names), strings.Join(names, ", "))
	}
	return nil
}

// CallTool invokes a named MCP tool with the given arguments and returns the
// concatenated text content of the result.
func (c *MCPClient) CallTool(name string, arguments map[string]interface{}) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	params := map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	}
	resp, err := c.call("tools/call", params)
	if err != nil {
		return "", fmt.Errorf("tools/call %s: %w", name, err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("tool %s error: %s", name, resp.Error.Message)
	}
	var result mcpCallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}
	var parts []string
	for _, item := range result.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	out := strings.Join(parts, "\n")
	return out, nil
}

// Close shuts down the MCP subprocess.
func (c *MCPClient) Close() error { return c.conn.Close() }

// ---------------------------------------------------------------------------
// Manager — subprocess lifecycle + singleton
// ---------------------------------------------------------------------------

// Manager owns the MCP subprocess lifecycle and provides tool calling. It is
// started once at boot (like the Python MCPClientManager singleton).
type Manager struct {
	command     string
	workDir     string
	env         []string
	client      *MCPClient
	clientMu    sync.Mutex
	ready       bool
	initialized bool
}

// cleanEnvValue strips whitespace, accidental surrounding quotes, and embedded
// control characters from an env value. Windows users commonly quote .env
// values containing spaces/backslashes, and paths pasted from Explorer can
// carry a stray \r or other control char that would make a path unresolvable.
func cleanEnvValue(value string) string {
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 0x20 && r != 0x7f) || r == '\t' {
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// resolveWorkDir mirrors mcp_client.py::MCPClientManager __init__ work-dir
// resolution: 1) explicit arg, 2) MCP_WORK_DIR env (absolute), 3) fall back to
// cwd (the backend chdir's to the work dir at startup).
func resolveWorkDir(explicit string) string {
	if explicit != "" {
		if abs, err := filepathAbspath(explicit); err == nil {
			return abs
		}
		return explicit
	}
	if v := cleanEnvValue(os.Getenv("MCP_WORK_DIR")); v != "" && isAbsPath(v) {
		return v
	}
	wd, _ := os.Getwd()
	return wd
}

// resolveServerPath mirrors mcp_client.py MCP server-path resolution:
// 1) explicit arg, 2) MCP_SERVER_PATH env (absolute), 3) fall back to
// <work_dir>/mcp-server/<server>. Relative paths resolve against work_dir.
func resolveServerPath(explicit, workDir string) string {
	server := explicit
	if server == "" {
		server = cleanEnvValue(os.Getenv("MCP_SERVER_PATH"))
	}
	if server == "" {
		server = "geniq-mcp-server"
	}
	if !isAbsPath(server) {
		server = joinPath(workDir, "mcp-server", server)
	}
	if abs, err := filepathAbspath(server); err == nil {
		server = abs
	}
	// On Windows the built binary carries a .exe suffix.
	if runtimeIsWindows() && !hasFileExtension(server) {
		if _, err := os.Stat(server + ".exe"); err == nil {
			server += ".exe"
		}
	}
	return server
}

// NewManager constructs a Manager, resolving the server path from explicit arg
// or environment. The subprocess is NOT started until Initialize() is called.
func NewManager(explicitServer, workDir string) *Manager {
	wd := resolveWorkDir(workDir)
	server := resolveServerPath(explicitServer, wd)
	fmt.Printf("[MCP] work_dir=%s\n", wd)
	fmt.Printf("[MCP] server=%s\n", server)
	return &Manager{
		command: server,
		workDir: wd,
		env:     os.Environ(),
	}
}

// Initialize starts the MCP subprocess and runs the handshake. Safe to call
// once; repeated calls are no-ops.
func (m *Manager) Initialize() error {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()

	if m.initialized {
		return nil
	}
	fmt.Printf("[MCP] starting subprocess: %s (work-dir %s)\n", m.command, m.workDir)
	client, err := startStdioProcess(m.command, m.workDir, m.env)
	if err != nil {
		// Don't fail the whole backend — the Python backend also logs and
		// continues, letting tools fail per-request.
		fmt.Printf("[MCP] failed to start subprocess: %v\n", err)
		return fmt.Errorf("start MCP subprocess: %w", err)
	}
	if err := client.Initialize(); err != nil {
		client.Close()
		return fmt.Errorf("initialize MCP: %w", err)
	}
	m.client = client
	m.initialized = true
	m.ready = true
	fmt.Println("[MCP] server ready and available!")
	return nil
}

// startStdioProcess launches the MCP binary and wires stdin/stdout pipes.
func startStdioProcess(command, workDir string, env []string) (*MCPClient, error) {
	cmd := exec.Command(command, "-work-dir", workDir)
	cmd.Env = env
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start MCP server %q: %w", command, err)
	}
	pw := &pipeReadWriter{in: stdin, out: stdout, cmd: cmd}
	c := &MCPClient{conn: pw}
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	return c, nil
}

// CallTool calls a named tool. If the manager isn't initialized, it attempts a
// lazy initialize (mirrors the Python manager which initializes on first call).
func (m *Manager) CallTool(name string, args map[string]interface{}) (string, error) {
	m.clientMu.Lock()
	client := m.client
	m.clientMu.Unlock()

	if client == nil {
		if err := m.Initialize(); err != nil {
			return "", fmt.Errorf("MCP tool '%s' error: %s", name, err)
		}
		m.clientMu.Lock()
		client = m.client
		m.clientMu.Unlock()
	}
	if client == nil {
		return "", fmt.Errorf("MCP tool '%s' error: MCP server not available", name)
	}
	return client.CallTool(name, args)
}

// ListTools returns the names of available MCP tools.
func (m *Manager) ListTools() ([]string, error) {
	m.clientMu.Lock()
	client := m.client
	m.clientMu.Unlock()
	if client == nil {
		if err := m.Initialize(); err != nil {
			return nil, err
		}
		m.clientMu.Lock()
		client = m.client
		m.clientMu.Unlock()
	}
	if client == nil {
		return nil, fmt.Errorf("MCP server not available")
	}
	resp, err := client.call("tools/list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Tools))
	for _, t := range result.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

// Shutdown terminates the MCP subprocess.
func (m *Manager) Shutdown() {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	m.initialized = false
	m.ready = false
}

// ManagerReady reports whether the MCP subprocess is up.
func (m *Manager) ManagerReady() bool {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	return m.ready
}

// ---------------------------------------------------------------------------
// Singleton
// ---------------------------------------------------------------------------

var (
	globalManager *Manager
	once          sync.Once
)

// GetManager returns the process-wide MCP manager, starting the subprocess on
// first use. This mirrors the Python get_mcp_manager() singleton.
func GetManager() *Manager {
	once.Do(func() {
		globalManager = NewManager(os.Getenv("MCP_SERVER_PATH"), os.Getenv("MCP_WORK_DIR"))
		// Initialize eagerly at boot (matches main.py startup_event).
		if err := globalManager.Initialize(); err != nil {
			fmt.Printf("[MCP] warning: %v\n", err)
		}
	})
	return globalManager
}
