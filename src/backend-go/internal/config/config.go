// Package config loads configuration from environment variables (and an optional
// .env file), mirroring the env-driven design of the Python backend.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all runtime configuration resolved from environment variables.
type Config struct {
	// Server
	Host string
	Port string

	// LLM (OpenAI-compatible endpoint)
	LLMModel       string
	LLMAPIKey      string
	LLMBASEURL     string
	LLMTemperature float64

	// MCP (stdio subprocess to the structured-data search server)
	MCPEnabled    bool
	MCPServerPath string
	MCPWorkDir    string
	// MCP_TOOL_EXEC_CMD: a command template executed verbatim to launch a stdio
	//   MCP server. Tokens ${tool}, ${args}, ${workdir} are substituted. Takes
	//   precedence over MCP_SERVER_PATH. Optional.
	MCPToolExecCmd string
	// MCP_ENDPOINT: a streamable-HTTP MCP endpoint (http://host:port/mcp) to
	//   connect to. If set, it takes precedence over MCP_TOOL_EXEC_CMD and
	//   MCP_SERVER_PATH (the app connects via Streamable HTTP). Optional.
	MCPServerURL   string
	// MCP_BLOCK_LIST: comma-separated list of tool-name filters (regex or plain
	//   substring) the model may NOT call. Optional. Default empty = no blocks.
	MCPBlockList   string

	// Confluence
	ConfluenceBaseURL string

	// RAG (vector search via sqlite-vec + external embeddings)
	RAGEnabled        bool
	RAGDocsDir        string
	RAGChunkSize      int
	RAGChunkOverlap   int
	RAGSearchLimit    int
	RAGScoreThreshold float64
	RAGDBPath         string
	RAGEmbeddingModel string

	// Embeddings (OpenAI-compatible /v1/embeddings)
	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingDim     int

	// CORS (HTTP middleware for browser-based frontends, e.g. the Wails WebView
	// at origin "wails://"). Every knob is dotenv-configurable; the defaults are
	// already permissive enough that no changes are needed to make the Wails app
	// work. Set CORS_ENABLED=false to disable the middleware entirely.
	CORSEnabled      bool
	// DB (application datastore: users, conversations, messages)
	DBPath    string
	DBDriver string
	CORSAuthority    string // Access-Control-Allow-Origin (":" = any, or a comma list)
	CORSMethods      string // Access-Control-Allow-Methods
	CORSAllowHeaders string // Access-Control-Allow-Headers
	CORSMaxAge       int    // Access-Control-Max-Age (seconds)
	CORSAllowCreds   bool   // Access-Control-Allow-Credentials: true

	// Feature flags for the model-driven tool-use (function calling) feature.
	// FEATURE_TOOL_USE selects the mode the message handlers use when a model
	// turn is served with or without handing tools to the model:
	//   "false" = hybrid-only: always use ContextBuilder (default baseline)
	//   "true"  = tool-use-only: always hand tools to the model
	//   "auto"  = probe-then-use: run the capability probe and pick per-request
	// MODEL_MAX_TOOL_CALLS caps how many tool-call+re-request rounds the loop
	// performs for a single user turn (guards against runaway tool loops).
	FEATURE_TOOL_USE       string
	MODEL_MAX_TOOL_CALLS   int
}

func envKey(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		b, _ := strconv.ParseBool(strings.ToLower(v))
		return b
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

// stripTrailingSuffix removes the given suffix from s, if present.
func stripTrailingSuffix(s, suffix string) string {
	if suffix != "" && strings.HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}
func envFloat(name string, fallback float64) float64 {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return f
		}
	}
	return fallback
}

// Load reads configuration from the environment. If envDotPath is a path to a
// .env file that exists, it is sourced first (KEY=value lines).
func Load(envDotPath string) *Config {
	if envDotPath != "" {
		envFileLoad(envDotPath)
	} else {
		// No explicit .env path given: mirror Python's load_dotenv() (no
		// argument) by sourcing ./.env from the current working directory if
		// one exists. A missing file is not fatal — callers run from different
		// dirs (repo root vs. src/backend-go) and not all have one.
		if wd, err := os.Getwd(); err == nil {
			envFileLoad(filepath.Join(wd, ".env"))
		}
	}

	// Resolve RAG docs dir: default to <cwd>/resources/rag_documents (matches
	// the Python backend's computed default).
	wd, _ := os.Getwd()
	rAGDocsDir := envKey("RAG_DOCS_DIR", filepath.Join(wd, "resources", "rag_documents"))

	cfg := &Config{
		Host:              envKey("HOST", "0.0.0.0"),
		Port:              envKey("PORT", "8000"),
		LLMModel:          envKey("LLM_MODEL", "gpt-4o"),
		LLMAPIKey:         envKey("LLM_API_KEY", "sk-placeholder"),
		LLMBASEURL:        envKey("LLM_BASE_URL", ""),
		LLMTemperature:    envFloat("LLM_TEMPERATURE", 0.1),
		MCPEnabled:        envBool("MCP_ENABLED", true),
		MCPServerPath:     envKey("MCP_SERVER_PATH", "geniq-mcp-server"),
		MCPToolExecCmd:  envKey("MCP_TOOL_EXEC_CMD", ""),
		MCPWorkDir:        envKey("MCP_WORK_DIR", wd),
		MCPBlockList:    envKey("MCP_BLOCK_LIST", ""),
		ConfluenceBaseURL: envKey("CONFLUENCE_BASE_URL", ""),
		RAGEnabled:        envBool("RAG_ENABLED", true),
		RAGDocsDir:        rAGDocsDir,
		RAGChunkSize:      envInt("RAG_CHUNK_SIZE", 1500),
		RAGChunkOverlap:   envInt("RAG_CHUNK_OVERLAP", 300),
		RAGSearchLimit:    envInt("RAG_SEARCH_LIMIT", 5),
		RAGScoreThreshold: envFloat("RAG_SCORE_THRESHOLD", 0.25),
		RAGDBPath:         envKey("RAG_DB_PATH", filepath.Join(wd, ".geniq_rag.db")),
		RAGEmbeddingModel: envKey("RAG_EMBEDDING_MODEL", "all-MiniLM-L6-v2"),
		EmbeddingAPIKey:   envKey("EMBEDDING_API_KEY", envKey("LLM_API_KEY", "sk-placeholder")),
	}
	// DB: application datastore (users, conversations, messages). SQLite is the
	// default driver; pass a registered driver name for PostgreSQL.
	cfg.DBPath = envKey("DB_PATH", filepath.Join(wd, ".sonic.db"))
	cfg.DBDriver = envKey("DB_DRIVER", "sqlite3")

	// Embedding base URL defaults to the LLM base URL unless explicitly set, so
	// a single local OpenAI-compatible server can serve both.
	if v := envKey("EMBEDDING_BASE_URL", cfg.LLMBASEURL); v != "" {
		// Normalise: strip a trailing "/v1" so the path (host-only) can be
		// safely appended with "/v1/embeddings" by the embeddings client,
		// mirroring how the LLM client builds "/v1/chat/completions".
		cfg.EmbeddingBaseURL = stripTrailingSuffix(v, "/v1")
	}

	// Embedding dimension: 384 is the dimension of all-MiniLM-L6-v2 (the default
	// model). Override via EMBEDDING_DIM for other models.
	cfg.EmbeddingDim = envInt("EMBEDDING_DIM", 384)

	// --- CORS --------------------------------------------------------------
	// --- Tool-use feature flags --------------------------------------------
	// FEATURE_TOOL_USE selects how message handlers serve a model turn with
	// or without handing MCP tools to the model. The default is "false" (pure
	// ContextBuilder hybrid) so the switch only turns on when explicitly set.
	cfg.FEATURE_TOOL_USE = strings.TrimSpace(envKey("FEATURE_TOOL_USE", "false"))
	cfg.MODEL_MAX_TOOL_CALLS = envInt("MODEL_MAX_TOOL_CALLS", 5)
	if cfg.MODEL_MAX_TOOL_CALLS <= 0 {
		cfg.MODEL_MAX_TOOL_CALLS = 5
	}
	// Allow the origin to opt out of the CORS middleware, or to pin down
	// specific values for a tighter security posture.
	cfg.CORSEnabled = envBool("CORS_ENABLED", true)

	corsAuthority := envKey("CORS_ORIGIN", "*")
	if corsAuthority != "" {
		cfg.CORSAuthority = corsAuthority
	}
	corsMethods := envKey("CORS_METHODS", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
	if corsMethods != "" {
		cfg.CORSMethods = corsMethods
	}
	corsAllowHeaders := envKey("CORS_ALLOW_HEADERS", "Content-Type, Authorization, Accept")
	if corsAllowHeaders != "" {
		cfg.CORSAllowHeaders = corsAllowHeaders
	}
	corsMaxAge := envInt("CORS_MAX_AGE", 3600)
	if corsMaxAge > 0 {
		cfg.CORSMaxAge = corsMaxAge
	}
	cfg.CORSAllowCreds = envBool("CORS_ALLOW_CREDENTIALS", false)

	return cfg
}

// Cors returns the resolved CORS settings.
func (c *Config) Cors() Cors {
	return Cors{
		Enabled:      c.CORSEnabled,
		Origin:       c.CORSAuthority,
		Methods:      c.CORSMethods,
		AllowHeaders: c.CORSAllowHeaders,
		MaxAge:       c.CORSMaxAge,
		AllowCreds:   c.CORSAllowCreds,
	}
}

// Cors holds the effective CORS settings for the HTTP middleware.
type Cors struct {
	Enabled      bool
	Origin       string // "*" for any origin, or a specific origin
	Methods      string
	AllowHeaders string
	MaxAge       int
	AllowCreds   bool
}
