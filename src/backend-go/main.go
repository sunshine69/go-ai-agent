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
)

func main() {
	envDotPath := ""
	if len(os.Args) > 1 {
		envDotPath = os.Args[1]
	}

	println("[DEBUG] envDotPAth: " + envDotPath)
	cfg := config.Load(envDotPath)

	fmt.Printf("[DEBUG] config %v\n", cfg)
	// --- MCP manager -------------------------------------------------------
	var manager *mcpclient.ResilientMCPClient
	if cfg.MCPEnabled {
		manager = mcpclient.NewManager(cfg.MCPServerPath, cfg.MCPWorkDir)
		if err := manager.Inner.Initialize(); err != nil {
			log.Printf("warning: MCP init failed, some tools will be unavailable: %v", err)
		}
		defer manager.Inner.Close()
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
