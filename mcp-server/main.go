package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	u "github.com/sunshine69/golang-tools/utils"
)

// CLI flag parsing
type config struct {
	transport string // "stdio" | "sse" | "streamable"
	host      string
	port      int
	basePath  string
	workDir   string
}

var defaultAllowPath string

func init() {
	// The allow pattern acts as a whitelist — anything not matching is denied.
	// Users can set BLOCKED_PATH_PTN themselves for a denylist-only approach.
	switch runtime.GOOS {
	case "windows":
		// Allow: %TEMP%/%TMP%/%USERPROFILE% literals, relative paths (no drive
		// letter or leading backslash), and absolute paths under Users\, Temp\,
		// Windows\Temp\. (?i) because Windows paths are case-insensitive.
		defaultAllowPath = `(?i)^(?!.*\.\.)[^\\/:*?"<>|][^:*?"<>|]*$`

	default: // linux, darwin, and everything else
		// Allow /tmp/, /var/tmp/, or any relative path (no leading /).
		// The old pattern `(\/tmp|[^\/])[^\s]*$` had a bug: [^\/] matched any
		// single non-slash char, so `/etc/shadow` passed because `shadow` starts
		// with `s`. Anchoring explicitly closes that gap.
		defaultAllowPath = `^(?!/)(?!\.\./\.\./)(?:\.\./|\./)?[^\s]+$`
	}
}

func parseArgs() config {
	cfg := config{
		transport: "stdio",
		host:      "0.0.0.0",
		port:      8080,
		basePath:  "",
	}

	flag.StringVar(&cfg.transport, "t", cfg.transport, `Transport: "stdio" (default), "sse", or "streamable"`)
	flag.StringVar(&cfg.host, "H", cfg.host, "Host to listen on")
	flag.IntVar(&cfg.port, "p", cfg.port, "Port to listen on")
	flag.StringVar(&cfg.basePath, "base-path", cfg.basePath, "URL base path prefix")
	flag.StringVar(&cfg.workDir, "work-dir", ".", "Working dir")

	flag.Usage = printUsage
	flag.Parse()
	return cfg
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: mcp-server [options]

Options:
  -t            Transport: "stdio" (default), "streamable"
  -H            Host to listen on (default: 0.0.0.0)
  -p            Port to listen on (default: 8080)
  -base-path    URL base path prefix (default: "")
  -h            Show this help

Examples:
  # stdio (default — Claude Desktop / local MCP clients)
  mcp-server

  # Streamable HTTP transport (newer MCP clients, llama-server web UI)
  mcp-server -t streamable -p 8081
    POST /mcp   — single endpoint for all JSON-RPC

Environment variables (all accept Go regex patterns):
  ALLOWED_PATH_PTN       Whitelist for all file/directory operations. Anything not
                         matching is denied. Default (%s):
                         %s

  BLOCKED_PATH_PTN       Optional denylist (useful when no allow pattern is set).
                         Default: ""

`, runtime.GOOS, defaultAllowPath)
}

// resolvePaths resolves non-absolute paths relative to workDir and sets them as env vars.
// This is called after os.Chdir(workDir), so relative paths are just the path itself.
// But we still want to resolve them to absolute paths for the tool handlers.
func resolvePaths(workDir string) {
	// File-based paths: if not absolute, resolve relative to workDir
	// NETWORK paths (CONFLUENCE_*) are always absolute URLs, no resolution needed
	filePathVars := map[string]string{
		"DOCUMENTS_BASE_PATH": "resources/documents",
		"SKILLS_DIR_PATH":     "resources/documents/skills/skills_directory.json",
		"PROC_DIR_PATH":       "resources/documents/skills/processes.json",
	}

	for envName, defaultRelPath := range filePathVars {
		val := os.Getenv(envName)
		if val == "" {
			// No env var set — use default relative to workDir
			val = defaultRelPath
		}

		// If the path is not absolute (doesn't start with /), join with workDir
		if !strings.HasPrefix(val, "/") {
			val = workDir + "/" + val
		}

		// Set the resolved absolute path back as an env var
		_ = os.Setenv(envName, val)
	}
}

// buildServer registers all tool sets onto an MCPServer instance.
func buildServer(cfg config) *server.MCPServer {
	// Resolve paths before chdir so we have absolute paths
	absWorkDir, _ := filepath.Abs(cfg.workDir)
	resolvePaths(absWorkDir)

	// Now chdir to workDir
	u.CheckErr(os.Chdir(absWorkDir), "Can not chdir to work-dir "+absWorkDir)

	s := server.NewMCPServer(
		"mcp-fetch-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
	)

	baseTool := BaseToolManager{
		AllowedPathPattern: u.Getenv("ALLOWED_PATH_PTN", defaultAllowPath),
		BlockedPathPattern: u.Getenv("BLOCKED_PATH_PTN", ""),
	}

	// Debug: log resolved paths
	log.Printf("[DEBUG] DOCUMENTS_BASE_PATH=%s", os.Getenv("DOCUMENTS_BASE_PATH"))
	log.Printf("[DEBUG] SKILLS_DIR_PATH=%s", os.Getenv("SKILLS_DIR_PATH"))
	log.Printf("[DEBUG] PROC_DIR_PATH=%s", os.Getenv("PROC_DIR_PATH"))
	log.Printf("[DEBUG] CONFLUENCE_BASE_URL=%s", os.Getenv("CONFLUENCE_BASE_URL"))

	// Default tools to load
	registerBaseTool(s, &baseTool)
	registerTextTools(s, &TextToolManager{})

	// Always load domain-specific tools (documents, skills, processes, confluence)
	if err := RegisterDocumentsTools(s); err != nil {
		log.Printf("Warning: documents tools unavailable: %v", err)
	}
	if err := RegisterConfluenceTools(s); err != nil {
		log.Printf("Warning: confluence tools unavailable: %v", err)
	}
	if err := RegisterSkillsTools(s); err != nil {
		log.Printf("Warning: skills/processes tools unavailable: %v", err)
	}

	return s
}

func main() {
	cfg := parseArgs()
	s := buildServer(cfg)

	switch cfg.transport {
	case "stdio":
		log.Println("Starting MCP server (stdio transport)")
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("Server error: %v", err)
		}

	case "streamable", "streamablehttp":
		addr := fmt.Sprintf("%s:%d", cfg.host, cfg.port)
		endpoint := cfg.basePath + "/mcp"

		streamServer := server.NewStreamableHTTPServer(s,
			server.WithEndpointPath(endpoint),
		)

		// Wrap with CORS middleware so browser-based clients (e.g. llama-server
		// web UI) can connect without a proxy.
		corsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, Mcp-Session-Id, Last-Event-ID, Mcp-Protocol-Version")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

			// Respond to preflight and stop — if we fall through, the inner handler
			// writes its own response and the headers are already locked.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			streamServer.ServeHTTP(w, r)
		})

		log.Printf("Starting MCP server (Streamable HTTP + CORS) on http://%s%s", addr, endpoint)
		log.Printf("  Endpoint: POST http://%s%s", addr, endpoint)
		if err := http.ListenAndServe(addr, corsHandler); err != nil {
			log.Fatalf("Streamable HTTP server error: %v", err)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown transport %q — valid values: \"stdio\", \"streamable\"\n", cfg.transport)
		printUsage()
		os.Exit(1)
	}
}
