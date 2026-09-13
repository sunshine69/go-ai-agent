// Command backend-go runs the HTTP backend that mirrors the Python FastAPI app.
// It wires config, MCP, LLM, RAG, and the routers into a single server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/db"
	"github.com/stevek/go-ai-agent/backend-go/internal/embeddings"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
	"github.com/stevek/go-ai-agent/backend-go/internal/mcpclient"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
	"github.com/stevek/go-ai-agent/backend-go/internal/routers"
	"github.com/stevek/go-ai-agent/backend-go/internal/serving"
)

func main() {
	envDotPath := ""
	frontendArg := ""

	flag.StringVar(&envDotPath, "envfile", "", "Dotenv file for config")
	flag.StringVar(&frontendArg, "ui-path", "", "Path to teh SPA app for the frontend")
	flag.Parse()

	println("[DEBUG] envDotPAth: " + envDotPath)
	cfg := config.Load(envDotPath)

	if frontendArg == "" {
		frontendArg = os.Getenv("FRONTEND_PATH")
	}
	frontend := serving.New(frontendArg)
	frontendEnabled := frontend.Enabled()
	if frontendEnabled {
		fmt.Printf("[server] serving SPA from %s at %s\n", frontend.Root(), serving.Prefix)
	} else {
		fmt.Println("[server] SPA disabled (set FRONTEND_PATH env or pass a path as the second CLI arg)")
	}

	fmt.Printf("[DEBUG] config %v\n", cfg)

	// --- DB: application datastore (users, conversations, messages) --------
	// The DB-backed auth & conversations endpoints require this. When left nil
	// (see below), those endpoints would panic at request time, so we must
	// open the store and pass it through to routers.Handlers. It is opened
	// before the MCP manager so the default (user-0) server can inherit any
	// per-user mcpWorkdir setting chosen via /mcpdir.
	d, err := db.Open(cfg.DBPath, cfg.DBDriver) // "sqlite3" default (SQLite)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer d.Close()
	// Seed the initial admin account if none exists yet.
	if _, err := d.SeedAdmin("admin", "admin@example.com", "admin"); err != nil {
		log.Printf("warning: seed admin failed: %v", err)
	}

	// --- MCP manager -------------------------------------------------------
	var manager *mcpclient.ResilientMCPClient
	if cfg.MCPEnabled {
		// /mcpdir seed: inherit the calling user's (uid 0) per-user
		// "mcpWorkdir" setting so the default (user-0) server runs in the
		// directory the operator chose at runtime. ResolveDefaultMCPWorkdir
		// re-validates the stored value and returns ("", false) when unset or
		// invalid, in which case the configured MCPWorkDir is used unchanged.
		workdir := cfg.MCPWorkDir
		if wd, ok := mcpclient.DefaultMCPWorkdir(d, 0); ok {
			workdir = wd
		}
		manager = mcpclient.NewManager(mcpclient.MCPManagerConfig{
			MCPServerPath:  cfg.MCPServerPath,
			MCPWorkDir:     workdir,
			MCPToolExecCmd: cfg.MCPToolExecCmd,
			MCPServerURL:   cfg.MCPServerURL,
			MCPBlockList:   cfg.MCPBlockList,
		})
		if manager == nil {
			// NewManager returns nil on failure (e.g. MCP binary not found).
			// Treat it as "MCP unavailable" rather than panicking so the HTTP
			// server still comes up for the other endpoints.
			log.Printf("warning: MCP manager failed to initialise; knowledge-base search will be unavailable")
		} else {
			if err := manager.Inner.Initialize(); err != nil {
				log.Printf("warning: MCP init failed, some tools will be unavailable: %v", err)
			}
			defer manager.Inner.Close()
		}
	} else {
		log.Printf("MCP disabled; knowledge-base search endpoints will not function")
	}

	// --- LLM ---------------------------------------------------------------
	llmClient := llm.New(llm.Config{
		BaseURL:     cfg.LLMBASEURL,
		APIKey:      cfg.LLMAPIKey,
		Model:       cfg.LLMModel,
		Temperature: cfg.LLMTemperature,
		Timeout:     cfg.LLMTimeout,
		// LLM_BACKEND forces the wire format; "" lets llm.New() auto-detect
		// from the model name / base URL (which is what fixes the
		// tool_choice object-rejection problem on local llama.cpp/ollama).
		Backend: cfg.LLMBackend,
	})

	// --- RAG ---------------------------------------------------------------
	var rag *ragstore.RAGStore
	if cfg.RAGEnabled {
		embedder := embeddings.New(embeddings.Config{
			BaseURL: cfg.EmbeddingBaseURL,
			APIKey:  cfg.EmbeddingAPIKey,
			Model:   cfg.RAGEmbeddingModel,
		}, cfg.EmbeddingDim)
		r, err := ragstore.New(ragstore.ConfigFromConfigMap(cfg), embedder)
		if err != nil {
			log.Printf("warning: RAG init failed: %v", err)
		} else {
			rag = r
		}
	} else {
		log.Println("RAG disabled")
	}

	// --- Handlers ----------------------------------------------------------
	// Build a per-user MCP manager over the shared default (possibly nil).
	mgr := mcpclient.NewMCPManager(manager)
	h := routers.NewHandlers(mgr, llmClient, rag, cfg, d, frontend)

	mux := h.ServeMux()

	srv := &http.Server{
		Addr:    cfg.Host + ":" + cfg.Port,
		Handler: mux,
	}

	go func() {
		fmt.Printf("[server] listening on http://%s (host=%s port=%s)\n", srv.Addr, cfg.Host, cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// --- Graceful shutdown -------------------------------------------------
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
