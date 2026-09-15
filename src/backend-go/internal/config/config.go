// Package config loads configuration from environment variables (and an optional
// .env file), mirroring the env-driven design of the Python backend.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	// LLM_BACKEND lets the operator force the wire format of OpenAI-compatible
	// fields that diverge between servers (most notably tool_choice). Supported
	// values: "llama_cpp" / "ollama" (bare-string tool_choice, e.g. "auto"),
	// else the OpenAI object form is used (default). Also auto-detected from
	// LLM_BASE_URL / model when unset. Resolution lives in llm.detectBackend.
	LLMBackend string
	// LLMTimeout is the per-request HTTP timeout for the LLM client. It mirrors
	// the reference app's 45m default (overridable via the LLM_TIMEOUT env var,
	// which also accepts an integer number of seconds). Default: 45m.
	LLMTimeout time.Duration

	// Context compression. These fields are ports of the reference go-ai-chat
	// CLI's context-trimming knobs (see ../go-ai-chat/chat/ai-context.go).
	//
	// ContextLimit caps the estimated token count of a conversation before its
	// history is compressed. A value of 0 disables context compression entirely.
	// It is also the baseline for the burst limit below.
	ContextLimit int
	// CtxOverSizeAllowed is the per-turn burst budget: within a single user turn
	// the context may temporarily exceed this number of tokens, letting the model
	// gather full information across multiple tool rounds. When unset (0) it
	// defaults to 2 * ContextLimit. It must be greater than ContextLimit to be
	// useful.
	CtxOverSizeAllowed int
	// SummaryModel overrides the model used for context summarisation. When
	// empty, the main LLMModel is used.
	SummaryModel string
	// SummaryModelUrl overrides the endpoint used for the summariser model. When
	// empty, the global LLM_BASE_URL is used.
	SummaryModelUrl string
	// SummaryModelTimeout bounds the summariser sub-call. Accepts a Go duration
	// string (e.g. "120s") or a plain integer number of seconds. Defaults to 1m.
	SummaryModelTimeout string
	// ShowThinking toggles emission of the model's thinking/reasoning text into
	// the persisted assistant content.
	ShowThinking bool

	// MCP (stdio subprocess to the structured-data search server)
	MCPEnabled    bool
	MCPServerPath string
	MCPWorkDir    string
	// MCP_TOOL_EXEC_CMD: a command template executed verbatim to launch a stdio
	// MCP server. Tokens ${tool}, ${args}, ${workdir} are substituted. Takes
	// precedence over MCP_SERVER_PATH. Optional.
	MCPToolExecCmd string
	// MCP_ENDPOINT: a streamable-HTTP MCP endpoint (http://host:port/mcp) to
	// connect to. If set, it takes precedence over MCP_TOOL_EXEC_CMD and
	// MCP_SERVER_PATH (the app connects via Streamable HTTP). Optional.
	MCPServerURL string
	// MCP_BLOCK_LIST: comma-separated list of tool-name filters (regex or plain
	// substring) the model may NOT call. Optional. Default empty = no blocks.
	MCPBlockList string

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
	CORSEnabled bool
	// DB (application datastore: users, conversations, messages)
	DBPath           string
	DBDriver         string
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
	FEATURE_TOOL_USE     string
	MODEL_MAX_TOOL_CALLS int
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

// timeoutDuration parses a Go duration string (e.g. "45m", "1h30m") or a plain
// integer (interpreted as a number of seconds) from an environment variable.
// The fallback is returned when the var is unset, empty, or unparseable.
func timeoutDuration(name string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		// A bare integer is treated as seconds (e.g. LLM_TIMEOUT=2700); a Go
		// duration string (e.g. LLM_TIMEOUT=45m) is tried next.
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return time.Duration(secs) * time.Second
		}
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
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
		Host:           envKey("HOST", "0.0.0.0"),
		Port:           envKey("PORT", "8000"),
		LLMModel:       envKey("LLM_MODEL", "gpt-4o"),
		LLMAPIKey:      envKey("LLM_API_KEY", "sk-placeholder"),
		LLMBASEURL:     envKey("LLM_BASE_URL", "http://localhost:11434"),
		LLMTemperature: envFloat("LLM_TEMPERATURE", 0.1),
		LLMTimeout:     timeoutDuration("LLM_TIMEOUT", 45*time.Minute),
		LLMBackend:     envKey("LLM_BACKEND", ""), // "", "llama_cpp", or "ollama"

		// Context compression defaults. These are env-driven to mirror the
		// reference app's config knobs; they all default to disabled so the
		// Go backend's default behaviour is unchanged until an operator turns
		// the feature on.
		ContextLimit:        envInt("CONTEXT_LIMIT", 0),
		CtxOverSizeAllowed:  envInt("CTX_OVER_SIZE_ALLOWED", 0),
		SummaryModel:        envKey("SUMMARY_MODEL", ""),
		SummaryModelUrl:     envKey("SUMMARY_MODEL_URL", ""),
		SummaryModelTimeout: envKey("SUMMARY_MODEL_TIMEOUT", "60s"),
		ShowThinking:        envBool("SHOW_THINKING", false),

		MCPEnabled:        envBool("MCP_ENABLED", true),
		MCPServerPath:     envKey("MCP_SERVER_PATH", "geniq-mcp-server"),
		MCPToolExecCmd:    envKey("MCP_TOOL_EXEC_CMD", ""),
		MCPWorkDir:        envKey("MCP_WORK_DIR", wd),
		MCPBlockList:      envKey("MCP_BLOCK_LIST", ""),
		ConfluenceBaseURL: envKey("CONFLUENCE_BASE_URL", ""),
		RAGEnabled:        envBool("RAG_ENABLED", true),
		RAGDocsDir:        rAGDocsDir,
		RAGChunkSize:      envInt("RAG_CHUNK_SIZE", 512),
		RAGChunkOverlap:   envInt("RAG_CHUNK_OVERLAP", 50),
		RAGSearchLimit:    envInt("RAG_SEARCH_LIMIT", 5),
		RAGScoreThreshold: envFloat("RAG_SCORE_THRESHOLD", 0.25),
		RAGDBPath:         envKey("RAG_DB_PATH", filepath.Join(wd, "rags.db")),
		RAGEmbeddingModel: envKey("RAG_EMBEDDING_MODEL", "nomic-embed"),
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
	cfg.EmbeddingDim = envInt("EMBEDDING_DIM", 768)

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
