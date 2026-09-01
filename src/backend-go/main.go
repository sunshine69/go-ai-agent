// Command backend-go runs the HTTP backend that mirrors the Python FastAPI app.
// It wires config, MCP, LLM, RAG, and the routers into a single server.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/embeddings"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
	"github.com/stevek/go-ai-agent/backend-go/internal/mcpclient"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
	"github.com/stevek/go-ai-agent/backend-go/internal/routers"
	"github.com/stevek/go-ai-agent/backend-go/internal/serving"
)

func main() {
	envDotPath := ""
	if len(os.Args) > 1 {
		envDotPath = os.Args[1]
	}

	println("[DEBUG] envDotPAth: " + envDotPath)

	// Resolve the SPA serving path: the optional second CLI argument
	// (os.Args[1] is the .env path), falling back to the FRONTEND_PATH env var.
	// Passing "" lets serving.New use FRONTEND_PATH, which is then resolved
	// relative to the current working directory.
	frontendArg := ""
	if len(os.Args) > 2 {
		frontendArg = os.Args[2]
	}

	// Load the .env FIRST. config.Load injects FRONTEND_PATH (and every other
	// config key) into the process environment. serving.New reads FRONTEND_PATH
	// from os.Getenv, so it must run AFTER config.Load — otherwise the .env
	// value is invisible to it (which is exactly why .env previously seemed
	// "ignored" while env=FRONTEND_PATH=... worked: a real process env var is
	// present before any Go code runs).
	cfg := config.Load(envDotPath)

	frontend := serving.New(frontendArg)
	frontendEnabled := frontend.Enabled()
	if frontendEnabled {
		fmt.Printf("[server] serving SPA from %s at %s\n", frontend.Root(), serving.Prefix)
	} else {
		fmt.Println("[server] SPA disabled (set FRONTEND_PATH env or pass a path as the second CLI arg)")
	}

	fmt.Printf("[DEBUG] config %v\n", cfg)
	// --- MCP manager -------------------------------------------------------
	var manager *mcpclient.ResilientMCPClient
	if cfg.MCPEnabled {
		manager = mcpclient.NewManager(cfg.MCPServerPath, cfg.MCPWorkDir)
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
	h := routers.Handlers{
		Manager: manager,
		LLM:     llmClient,
		Rag:     rag,
		Cfg:     cfg,
		Frontend: frontend,
	}

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
