package mcpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 types
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC      string          `json:"jsonrpc"`
	ID           interface{}     `json:"id"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *jsonRPCError   `json:"error,omitempty"`
	ExtraContent *ExtraContent   `json:"extra_content,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// MCP protocol types
// ---------------------------------------------------------------------------

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpCallToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// ---------------------------------------------------------------------------
// OpenAI tool-call types (what the LLM returns)
// ---------------------------------------------------------------------------

// ExtraContent holds provider-specific metadata attached to a tool call.
type ExtraContent struct {
	Google *GoogleExtraContent `json:"google,omitempty"`
}

type GoogleExtraContent struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ToolCall mirrors the OpenAI delta.tool_calls structure
type ToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	ExtraContent *ExtraContent `json:"extra_content,omitempty"`
}

// ---------------------------------------------------------------------------
// MCPClient
// ---------------------------------------------------------------------------

type MCPClient struct {
	mu               sync.Mutex
	conn             io.ReadWriteCloser // TCP or pipe wrapper
	scanner          *bufio.Scanner
	nextID           atomic.Int64
	tools            []mcpTool
	spec             string // original spec for display
	isSSE            bool   // true if using SSE/HTTP mode
	isStreamableHTTP bool   // true if using streamable HTTP (single POST endpoint)
	postURL          string // the endpoint URL for POSTing requests in SSE/streamable mode
	httpClient       *http.Client
	sessionID        string // Mcp-Session-Id for streamable HTTP sessions
}

// pipeReadWriter wraps an exec.Cmd's stdin/stdout into a single ReadWriteCloser
type pipeReadWriter struct {
	in  io.WriteCloser
	out io.ReadCloser
	cmd *exec.Cmd
}

func (p *pipeReadWriter) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *pipeReadWriter) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *pipeReadWriter) Close() error {
	p.in.Close()
	p.out.Close()
	// Graceful-exit fallback: cmd.Wait() does NOT kill the child. Give stdio MCP server a chance to exit on EOF; if it is still alive, kill it.
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
		return fmt.Errorf("stdio MCP process still alive after graceful-exit timeout; killed it")
	}
}

// sseReadWriteCloser wraps an io.ReadCloser to satisfy io.ReadWriteCloser
// because in SSE mode, writing is handled via HTTP POST, not the stream.
type sseReadWriteCloser struct {
	io.ReadCloser
}

func (s *sseReadWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil // No-op
}

// MCPManagerConfig holds everything NewManager needs to choose a transport
// and to filter tool names. Priority: MCP_ENDPOINT (streamable HTTP) >
// MCP_TOOL_EXEC_CMD (verbatim command launch).
type MCPManagerConfig struct {
	MCPWorkDir     string
	MCPToolExecCmd string
	MCPServerURL   string
	MCPBlockList   string
}

// compileBlockList parses a comma-separated block-list string into a slice of
// compiled regular expressions. An empty/whitespace-only string yields a nil
// slice (i.e. nothing blocked). Matching is applied to the tool's Name.
func compileBlockList(blockedPatternsStr string) []*regexp.Regexp {
	if strings.TrimSpace(blockedPatternsStr) == "" {
		return nil
	}

	segments := strings.Split(blockedPatternsStr, ",")
	var activeRegexes []*regexp.Regexp

	for _, segment := range segments {
		cleanPattern := strings.TrimSpace(segment)
		if cleanPattern == "" {
			continue
		}
		// If the literal does not compile as a regex, treat it as a plain
		// substring match so operators can pass simple tool-name filters.
		if expr, err := regexp.Compile(cleanPattern); err == nil {
			activeRegexes = append(activeRegexes, expr)
			continue
		}
		activeRegexes = append(activeRegexes, regexp.MustCompile(regexp.QuoteMeta(cleanPattern)))
	}

	return activeRegexes
}

// toolIsBlocked reports whether the tool name matches any block-list pattern.
func toolIsBlocked(re []*regexp.Regexp, name string) bool {
	for _, expr := range re {
		if expr.MatchString(name) {
			return true
		}
	}
	return false
}

// applyBlockFilter returns only the tools whose names are not blocked.
func applyBlockFilter(allTools []mcpTool, re []*regexp.Regexp) []mcpTool {
	if len(re) == 0 {
		return allTools
	}
	var allowed []mcpTool
	for _, tool := range allTools {
		if toolIsBlocked(re, tool.Name) {
			continue
		}
		allowed = append(allowed, tool)
	}
	return allowed
}

// expandCmd expands a verbatim command template into exec arguments.
// The only supported substitution token is ${workdir}, which is replaced with
// the configured work directory. The remainder is split on whitespace into
// individual argv entries.
//
// This lets operators launch a stdio MCP server with arbitrary options, e.g.:
//
//	MCP_TOOL_EXEC_CMD="python3 -m my_mcp_server"
//	MCP_TOOL_EXEC_CMD="node --max-old-space-size=2048 ./server.js"
func expandCmd(template, workdir string) ([]string, error) {
	expanded := template
	if strings.Contains(expanded, "${workdir}") {
		expanded = strings.ReplaceAll(expanded, "${workdir}", workdir)
	}

	fields := strings.Fields(expanded)
	if len(fields) == 0 {
		return nil, fmt.Errorf("command template expanded to empty")
	}
	return fields, nil
}

// NewManager selects a transport and returns a resilient MCP client.
//
// Priority (highest first):
//  1. MCP_ENDPOINT  — streamable-HTTP endpoint (http://host:port/mcp).
//  2. MCP_TOOL_EXEC_CMD — verbatim command template executed to launch stdio.//
//
// MCP_BLOCK_LIST (comma-separated regex/substring tool-name filters) is applied
// by the returned client in both Tools() and CallTool().
func NewManager(cfg MCPManagerConfig) (newMCP *ResilientMCPClient) {
	// Priority 1: streamable HTTP endpoint. MCP_ENDPOINT always wins.
	if url := strings.TrimSpace(cfg.MCPServerURL); url != "" {
		if !strings.HasPrefix(url, "http") {
			fmt.Fprintf(os.Stderr, "⚠️  MCP_ENDPOINT %q does not start with http:// or https://; ignoring\n", url)
		} else {
			raw, e := ConnectStreamableHTTP(url)
			if e != nil {
				fmt.Fprintf(os.Stderr, "❌ MCP connect failed: %v\n", e)
				return nil
			}
			m := NewResilientPassthrough(raw)
			m.blockRe = compileBlockList(cfg.MCPBlockList)
			fmt.Fprintf(os.Stderr, "🌐 MCP connected via Streamable HTTP at %s\n", url)
			return m
		}
	}

	// Priority 2: verbatim command template for stdio.
	cmd := strings.TrimSpace(cfg.MCPToolExecCmd)
	if cmd != "" {
		parts, e := expandCmd(cmd, cfg.MCPWorkDir)
		if e != nil {
			fmt.Fprintf(os.Stderr, "❌ MCP command template expansion failed: %v\n", e)
			return nil
		}
		fmt.Fprintf(os.Stderr, "🚀 Launching MCP server: %v\n", parts)
		// cfg.MCPWorkDir may be absolute (the default is the process cwd) or
		// relative; NewResilientStdio resolves it to a valid directory and sets
		// the child's cmd.Dir, or falls back to the process cwd if unresolved.
		m, e := NewResilientStdio(parts, cfg.MCPWorkDir)
		if e != nil {
			fmt.Fprintf(os.Stderr, "❌ MCP connect failed: %v\n", e)
			return nil
		}
		m.blockRe = compileBlockList(cfg.MCPBlockList)
		return m
	}

	// No transport configured — report MCP as unavailable.
	return nil
}

// ConnectStreamableHTTP connects to a modern MCP server using the Streamable HTTP
// transport (MCP spec 2025-03-26). Each JSON-RPC call is an independent POST —
// no persistent SSE stream is needed. This is what llama-server and newer MCP
// clients expect at a single endpoint like /mcp.
func ConnectStreamableHTTP(url string) (*MCPClient, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] ConnectStreamableHTTP called: %s\n", url)
	c := &MCPClient{
		isStreamableHTTP: true,
		postURL:          url,
		spec:             url,
		httpClient:       &http.Client{Timeout: 600 * time.Second},
		// conn is unused for streamable HTTP but must be non-nil for Close()
		conn: &sseReadWriteCloser{ReadCloser: io.NopCloser(strings.NewReader(""))},
	}

	// Establish a session before any other request. Sending the "initialize"
	// RPC is what makes the server assign a Mcp-Session-Id header; subsequent
	// requests (tools/list, tools/call, ...) must carry that header. Without it
	// the server answers with "HTTP 404: Invalid session ID" (matching the
	// behaviour of the reference initialize() handshake).
	initParams := map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "aig",
			"version": "1.0.0",
		},
	}
	if _, err := c.call("initialize", initParams); err != nil {
		// A missing/absent initialize response is non-fatal for some servers;
		// still attempt to proceed so tools/list can run against whatever
		// session may have been created by the header above.
		fmt.Fprintf(os.Stderr, "⚠️  MCP initialize skipped/failed: %v\n", err)
	}

	if err := c.refreshTools(); err != nil {
		return nil, err
	}
	return c, nil
}

// Connect launches or connects to an MCP server from the /mcp command spec,
// mirroring NewManager's transport selection but driven at runtime by the /mcp
// slash command rather than at startup by config. A spec beginning with http:// or
// https:// connects via Streamable HTTP; anything else is whitespace-split and
// launched as a stdio server. The block-list is applied to the returned client. The
// returned client is a fresh ResilientMCPClient — the caller is responsible for
// swapping it into the shared manager (see Swap) so every backend handler sees it.
// Returns an error without mutating any global state on failure.
func Connect(spec string, blockList string, workdir string) (*ResilientMCPClient, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty MCP spec")
	}

	var m *ResilientMCPClient
	var err error
	if strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://") {
		raw, e := ConnectStreamableHTTP(spec)
		if e != nil {
			fmt.Fprintf(os.Stderr, "❌ MCP connect failed: %v\n", e)
			return nil, e
		}
		m = NewResilientPassthrough(raw)
	} else {
		parts := strings.Fields(spec)
		if len(parts) == 0 {
			return nil, fmt.Errorf("empty MCP command")
		}
		m, err = NewResilientStdio(parts, workdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ MCP connect failed: %v\n", err)
			return nil, err
		}
	}
	m.blockRe = compileBlockList(blockList)
	fmt.Fprintf(os.Stderr, "✅ MCP connected via /mcp: %s (%d tool(s))\n", m.Spec, len(m.Tools()))
	return m, nil
}

// ---------------------------------------------------------------------------
// Internal JSON-RPC helpers
// ---------------------------------------------------------------------------

func (c *MCPClient) send(req jsonRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	if c.isSSE {
		if c.postURL == "" {
			return fmt.Errorf("SSE postURL not Initialized")
		}
		resp, err := c.httpClient.Post(c.postURL, "application/json", bytes.NewBuffer(data))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}

	// Standard TCP/Stdio: write with newline
	data = append(data, '\n')
	_, err = c.conn.Write(data)
	return err
}

// callStreamableHTTP performs a single POST for the streamable HTTP transport.
// It handles both plain JSON responses and SSE-wrapped responses (data: {...}).
// It also tracks the Mcp-Session-Id header for session continuity.
func (c *MCPClient) callStreamableHTTP(method string, params interface{}) (*jsonRPCResponse, error) {
	id := c.nextID.Add(1)
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", c.postURL, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("Mcp-Protocol-Version", "2025-03-26")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	// 202 Accepted = server acknowledged but has no response body (e.g. notifications)
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// Parse plain JSON or SSE-wrapped response
	line := strings.TrimSpace(string(body))
	for _, l := range strings.Split(line, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "data: ") {
			line = strings.TrimPrefix(l, "data: ")
			break
		}
		if strings.HasPrefix(l, ":") || strings.HasPrefix(l, "event:") || l == "" {
			continue
		}
		if strings.HasPrefix(l, "{") {
			line = l
			break
		}
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal([]byte(line), &rpcResp); err != nil {
		return nil, fmt.Errorf("parse response: %w\nbody: %s", err, string(body))
	}
	return &rpcResp, nil
}

func (c *MCPClient) recv() (*jsonRPCResponse, error) {
	for c.scanner.Scan() {
		line := strings.TrimSpace(c.scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}

		// For SSE, the data is prefixed with "data: "
		if c.isSSE && strings.HasPrefix(line, "data: ") {
			line = strings.TrimPrefix(line, "data: ")
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

func (c *MCPClient) call(method string, params interface{}) (*jsonRPCResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Streamable HTTP: each call is a self-contained POST/response — no scanner needed
	if c.isStreamableHTTP {
		resp, err := c.callStreamableHTTP(method, params)
		if err != nil {
			return nil, err
		}
		// nil response is valid for notifications (202/204)
		if resp == nil {
			return &jsonRPCResponse{JSONRPC: "2.0"}, nil
		}

		return resp, nil
	}

	id := c.nextID.Add(1)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := c.send(req); err != nil {
		return nil, err
	}
	// Skip notifications (no id) until we get our response
	for {
		resp, err := c.recv()
		if err != nil {
			return nil, err
		}
		if resp.ID == nil {
			continue // notification — ignore
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

// ---------------------------------------------------------------------------
func (c *MCPClient) refreshTools() error {
	resp, err := c.call("tools/list", nil)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}
	var result mcpToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse tools: %w", err)
	}
	c.tools = result.Tools
	return nil
}

// CallTool invokes a named MCP tool with the given arguments (JSON-encoded map).
func (c *MCPClient) CallTool(name string, arguments map[string]interface{}) (string, error) {
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
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	out := strings.Join(parts, "\n")
	if result.IsError {
		return "", fmt.Errorf("tool error: %s", out)
	}
	return out, nil
}

// Tools returns the list of available MCP tools.
func (c *MCPClient) Tools() []mcpTool { return c.tools }

// Close shuts down the MCP connection.
func (c *MCPClient) Close() error { return c.conn.Close() }

// ---------------------------------------------------------------------------
// OpenAI "tools" schema conversion
// ---------------------------------------------------------------------------

// ToOpenAITools converts MCP tool descriptors to OpenAI-compatible tool specs.
func ToOpenAITools(tools []mcpTool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		var schema interface{}
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  schema,
			},
		})
	}
	return out
}

type mcpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type mcpResourcesListResult struct {
	Resources []mcpResource `json:"resources"`
}

type mcpResourcesReadResult struct {
	Contents []struct {
		URI      string `json:"uri"`
		MimeType string `json:"mimeType,omitempty"`
		Text     string `json:"text,omitempty"`
		Blob     string `json:"blob,omitempty"`
	} `json:"contents"`
}

// Resources returns the list of available MCP resources.
func (c *MCPClient) Resources() ([]mcpResource, error) {
	resp, err := c.call("resources/list", nil)
	if err != nil {
		return nil, fmt.Errorf("resources/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}
	var result mcpResourcesListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse resources: %w", err)
	}
	return result.Resources, nil
}

// ReadResource fetches the contents of a resource by URI.
func (c *MCPClient) ReadResource(uri string) (string, error) {
	params := map[string]interface{}{
		"uri": uri,
	}
	resp, err := c.call("resources/read", params)
	if err != nil {
		return "", fmt.Errorf("resources/read %s: %w", uri, err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("resources/read error: %s", resp.Error.Message)
	}
	var result mcpResourcesReadResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse resource read: %w", err)
	}
	var parts []string
	for _, r := range result.Contents {
		if r.Text != "" {
			parts = append(parts, r.Text)
		} else if r.Blob != "" {
			parts = append(parts, fmt.Sprintf("<binary resource %s>", r.URI))
		}
	}
	return strings.Join(parts, "\n"), nil
}

// SubscribeResource subscribes to change notifications for a resource URI.
func (c *MCPClient) SubscribeResource(uri string) error {
	params := map[string]interface{}{"uri": uri}
	_, err := c.call("resources/subscribe", params)
	return err
}

// UnsubscribeResource unsubscribes from change notifications for a resource URI.
func (c *MCPClient) UnsubscribeResource(uri string) error {
	params := map[string]interface{}{"uri": uri}
	_, err := c.call("resources/unsubscribe", params)
	return err
}

// ---------------------------------------------------------------------------
// ResilientMCPClient — auto-reconnecting wrapper for stdio servers
// ---------------------------------------------------------------------------

type MCPClientIface interface {
	CallTool(name string, arguments map[string]interface{}) (string, error)
	Tools() []mcpTool
	Resources() ([]mcpResource, error)
	ReadResource(uri string) (string, error)
	refreshTools() error
	Close() error
}

type ResilientMCPClient struct {
	// blockRe holds the compiled regexes/substring filters from MCP_BLOCK_LIST.
	// A tool whose name matches any of them is filtered out of Tools() and
	// rejected by CallTool(). Nil/empty means no blocking.
	blockRe []*regexp.Regexp
	mu      sync.Mutex
	Inner   *MCPClient
	parts   []string
	// For reconnect, the argv used to launch the stdio server.
	// workdir is the resolved absolute directory the stdio child runs in. It is
	// set only for stdio transports (empty for Streamable HTTP) and threaded
	// through ConnectStdio so the child actually chdir's there; the value is
	// also carried across reconnect so an auto-restarted child keeps cwd.
	workdir         string
	hasWorkdir      bool
	partsForConnect []string
	maxRetry        int
	backoff         time.Duration
	Spec            string
}

func NewResilientStdio(parts []string, workdir string) (*ResilientMCPClient, error) {
	// Resolve the workdir to an absolute, existing path (creating it if
	// necessary) once so the child launch and every reconnect share the same
	// directory. A workdir of "." is treated as "no workdir" so stdio servers
	// fall back to the backend process cwd, preserving prior behaviour.
	resolved, isResolved := resolveConnectWorkdir(workdir)
	inner, err := ConnectStdio(parts, resolved)
	if err != nil {
		return nil, err
	}
	return &ResilientMCPClient{
		Inner:           inner,
		partsForConnect: parts,
		// Persist the resolved workdir on the struct so reconnect() reuses the
		// same directory for an auto-restarted child.
		workdir:    resolved,
		hasWorkdir: isResolved,
		maxRetry:   3,
		backoff:    500 * time.Millisecond,
		Spec:       inner.spec,
	}, nil
}

// resolveConnectWorkdir validates and resolves a requested workdir. It returns
// an empty string (meaning "use the backend process cwd") for empty input or a
// literal ".", and otherwise resolves p to an absolute path, creating the
// directory if missing. It returns the resolved path and whether it was
// non-empty/resolvable so the caller knows whether to carry the workdir across
// reconnects.
func resolveConnectWorkdir(p string) (string, bool) {
	if strings.TrimSpace(p) == "" || strings.TrimSpace(p) == "." {
		return "", false
	}
	if !isValidWorkDir(p) {
		// Report the rejected value on stderr so operators can see what was
		// refused, then fall back to the backend process cwd.
		fmt.Fprintf(os.Stderr, "⚠️  MCP workdir %q rejected (must be relative with no \"..\" component); using process cwd\n", p)
		return "", false
	}
	dir, err := resolveWorkDir(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  MCP workdir %q unusable (%v); using process cwd\n", p, err)
		return "", false
	}
	return dir, true
}

func NewResilientPassthrough(c *MCPClient) *ResilientMCPClient {
	return &ResilientMCPClient{Inner: c, Spec: c.spec}
}

func (r *ResilientMCPClient) reconnect() error {
	if len(r.partsForConnect) == 0 {
		return fmt.Errorf("reconnect not supported for non-stdio connections")
	}
	if r.Inner != nil {
		r.Inner.Close()
	}
	fmt.Fprintln(os.Stderr, "🔄 MCP server died — reconnecting…")
	var lastErr error
	for attempt := 1; attempt <= r.maxRetry; attempt++ {
		// Reuse the resolved workdir from the original launch so an
		// auto-restarted child runs in the same directory.
		wd := ""
		if r.hasWorkdir {
			wd = r.workdir
		}
		c, err := ConnectStdio(r.partsForConnect, wd)
		if err == nil {
			r.Inner = c
			fmt.Fprintf(os.Stderr, "✅ MCP reconnected (attempt %d)\n", attempt)
			return nil
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "   ⚠️  attempt %d/%d failed: %v\n", attempt, r.maxRetry, err)
		time.Sleep(r.backoff * time.Duration(attempt))
	}
	return fmt.Errorf("could not reconnect after %d attempts: %w", r.maxRetry, lastErr)
}

func isDeadErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "io: read/write on closed pipe") ||
		strings.Contains(msg, "file already closed")
}

// CallTool invokes a named MCP tool with the given arguments (JSON-encoded map).
// Names in the MCP_BLOCK_LIST are rejected before any network call.
func (r *ResilientMCPClient) CallTool(name string, arguments map[string]interface{}) (string, error) {
	// Block-list enforcement: reject a blocked tool name early with a clear
	// message rather than hitting the server and relying on its error.
	if toolIsBlocked(r.blockRe, name) {
		return "", fmt.Errorf("tool %q is blocked by MCP_BLOCK_LIST", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Inner == nil {
		return "", fmt.Errorf("no MCP server connected")
	}

	result, err := r.Inner.CallTool(name, arguments)
	if err == nil {
		return result, nil
	}
	if !isDeadErr(err) || len(r.partsForConnect) == 0 {
		return "", err
	}

	if reconnErr := r.reconnect(); reconnErr != nil {
		return "", fmt.Errorf("tool call failed and reconnect failed: %w", reconnErr)
	}
	return r.Inner.CallTool(name, arguments)
}

// Tools returns the list of available MCP tools minus any blocked by
// MCP_BLOCK_LIST.
func (r *ResilientMCPClient) Tools() []mcpTool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Inner == nil {
		return nil
	}
	all := r.Inner.Tools()
	return applyBlockFilter(all, r.blockRe)
}

func (r *ResilientMCPClient) Resources() ([]mcpResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Inner == nil {
		return nil, fmt.Errorf("no MCP server connected")
	}
	return r.Inner.Resources()
}

func (r *ResilientMCPClient) ReadResource(uri string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Inner == nil {
		return "", fmt.Errorf("no MCP server connected")
	}
	return r.Inner.ReadResource(uri)
}
func (r *ResilientMCPClient) refreshTools() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Inner == nil {
		return fmt.Errorf("no MCP server connected")
	}
	return r.Inner.refreshTools()
}

// RefreshTools re-queries the MCP server for its current tool list. It is the
// exported counterpart of refreshTools so the backend can expose an MCP
// "refresh" action (e.g. via an HTTP endpoint) without leaking the private
// refreshTools symbol. It returns an error when no server is connected.
func (r *ResilientMCPClient) RefreshTools() error {
	return r.refreshTools()
}

func (r *ResilientMCPClient) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Inner != nil {
		return r.Inner.Close()
	}
	return nil
}

// Swap replaces this object's inner client in place with another ResilientMCPClient.
// Every backend handler stores a *copy* of this *ResilientMCPClient pointer (see
// Handlers), so all handlers, context providers, and the tool-use controller read
// the same underlying object. Mutating this object in place — rather than
// reassigning the pointer field on each handler — is what propagates a
// connect/disconnect to every reader. Passing a nil client swaps the inner client
// out entirely (closing any live inner) while keeping the ResilientMCPClient alive;
// the nil-guarded Tools()/CallTool()/Resources()/ReadResource() then report
// "no MCP server connected" instead of panicking.
func (r *ResilientMCPClient) Swap(other *ResilientMCPClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Inner != nil { // Close the OLD inner on every replacement (reconnect OR disconnect), so the previous child process never leaks.
		_ = r.Inner.Close()
	}
	r.Inner = nil
	r.Spec = ""
	if other != nil {
		r.Inner = other.Inner
		r.Spec = other.Spec
	}
}

// ---------------------------------------------------------------------------
// ConnectStdio launches a local MCP server process and communicates via stdio.
// ---------------------------------------------------------------------------

func ConnectStdio(parts []string, workdir string) (*MCPClient, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	// Set the working directory the child process runs in, if provided. An
	// empty workdir leaves the child inheriting the backend process cwd (the
	// prior behaviour); a non-empty one makes cmd.Dir point the child there so
	// the stdio server's relative file access and CWD-based discovery target
	// the operator's chosen directory rather than wherever the backend started.
	if w := strings.TrimSpace(workdir); w != "" {
		cmd.Dir = w
	}
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server %q: %w", parts, err)
	}

	pw := &pipeReadWriter{in: stdin, out: stdout, cmd: cmd}
	c := &MCPClient{conn: pw, spec: parts[0]}
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	if err := c.refreshTools(); err != nil {
		pw.Close()
		return nil, err
	}
	return c, nil
}
