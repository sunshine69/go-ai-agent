// Command ragindex indexes the RAG documents directory into the RAG store's
// SQLite+sqlite-vec vector table, mirroring the Python RAG indexer CLI.
//
// Usage:
//
//	ragindex [env-dot-path] [-mode full|incremental|reset|dry-run] [-docs <dir>] [-out <dir>] [-category <name>] [-search <text>] [-limit N] [-verbose] [-force]
//
// Examples:
//
//	# Full re-index of all categories, using backend-go/.env
//	ragindex src/backend-go/.env -mode full -verbose
//
//	# Reset mode from a custom output dir (creates the dir if missing)
//	ragindex -mode reset -out /tmp/rag
//
//	# Vector-search the already-indexed store (uses -out dir to find the DB)
//	ragindex -search "MRI safety procedures" -out /tmp/ragtest
//
// The indexer reads RAG_DOCS_DIR (default: <cwd>/resources/rag_documents) and
// RAG_DB_PATH from the .env, embeds chunk text via the configured OpenAI-
// compatible /v1/embeddings endpoint, and persists a _index_state.json sidecar.
// With -search it skips indexing and instead returns the top vector-similarity
// matches from the store, so you can probe indexed documents straight from the
// CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/embeddings"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragindex"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run parses args and executes the indexer.
//
// Go's flag package stops parsing at the first non-flag argument. This means
// "ragindex src/backend-go/.env -mode full" would parse the .env as a
// positional and then STOP — the -mode flag would be silently ignored, and
// the indexer would always run in incremental mode. To fix that we manually
// split args into positionals and flags before invoking flag.Parse().
func run(args []string) error {
	// Split positionals from flags. Everything up to the first flag-looking
	// token is positional; from the first -x onward, everything is a flag
	// (including its value). Long --mode forms are normalized to -mode.
	var positional []string
	flagArgs := []string{}
	i := 0
	for i < len(args) {
		a := args[i]
		if len(a) >= 2 && a[:2] == "--" {
			if len(a) > 3 {
				a = "-" + a[3:]
			} else {
				a = "-"
			}
		}
		if len(a) > 0 && a[0] == '-' && a != "-" {
			flagArgs = append(flagArgs, a)
			// If it's not a bool flag, the next arg is its value.
			if i+1 < len(args) {
				flagArgs = append(flagArgs, args[i+1])
				i += 2
			} else {
				i++
			}
			continue
		}
		positional = append(positional, a)
		i++
	}

	var (
		modeFlag = flag.String("mode", "incremental", "indexing mode: full|incremental|reset|dry-run")
		outFlag  = flag.String("out", "", "output directory for the RAG db (overrides RAG_DB_PATH from .env)")
		docsFlag = flag.String("docs", "", "directory of RAG documents to scan for indexing (overrides RAG_DOCS_DIR env var / default <cwd>/resources/rag_documents)")
		catFlag  = flag.String("category", "", "only index this category directory")
		srch     = flag.String("search", "", "vector-search the store instead of indexing; prints top matches")
		limitFlg = flag.Int("limit", 5, "max results for -search")
		forceLog = flag.Bool("force", false, "force re-index of all files even if unchanged")
		verbose  = flag.Bool("verbose", false, "print detailed per-file debug output")
	)
	if err := flag.CommandLine.Parse(flagArgs); err != nil {
		return err
	}

	// Positional args: .env path (first), out dir (second).
	envDotPath := ""
	outDir := ""
	for _, p := range positional {
		if p == "" {
			continue
		}
		if envDotPath == "" {
			envDotPath = p
			continue
		}
		if outDir == "" {
			outDir = p
		}
	}

	mode := ragindex.Mode(*modeFlag)
	switch mode {
	case ragindex.ModeFull, ragindex.ModeIncremental, ragindex.ModeReset, ragindex.ModeDryRun:
	default:
		return fmt.Errorf("unknown mode %q (want full|incremental|reset|dry-run)", mode)
	}

	cfg := config.Load(envDotPath)

	// -docs overrides the docs directory (RAG_DOCS_DIR / default <cwd>/resources/
	// rag_documents), so the CLI is usable without editing the .env.
	if *docsFlag != "" {
		cfg.RAGDocsDir = *docsFlag
	}

	// Resolve output DB path. If -out is given, create the directory if it
	// doesn't exist and drop the default db filename inside it.
	if outDir != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return fmt.Errorf("create out dir %q: %w", outDir, err)
		}
		cfg.RAGDBPath = filepath.Join(outDir, ".geniq_rag.db")
	} else if *outFlag != "" {
		if err := os.MkdirAll(*outFlag, 0o755); err != nil {
			return fmt.Errorf("create out dir %q: %w", *outFlag, err)
		}
		cfg.RAGDBPath = filepath.Join(*outFlag, ".geniq_rag.db")
	}

	if !cfg.RAGEnabled {
		fmt.Println("RAG is disabled (RAG_ENABLED=false); nothing to index")
		return nil
	}

	fmt.Printf("[ragindex] mode=%s docs=%s db=%s dim=%d verbose=%v force=%v\n",
		mode, cfg.RAGDocsDir, cfg.RAGDBPath, cfg.EmbeddingDim, *verbose, *forceLog)

	embedder := embeddings.New(embeddings.Config{
		BaseURL: cfg.EmbeddingBaseURL,
		APIKey:  cfg.EmbeddingAPIKey,
		Model:   cfg.RAGEmbeddingModel,
	}, cfg.EmbeddingDim)

	store, err := ragstore.New(ragstore.ConfigFromConfigMap(cfg), embedder)
	if err != nil {
		return fmt.Errorf("RAG store init: %w", err)
	}
	defer store.Close()

	// -search: vector-search the store and print matches instead of indexing.
	if *srch != "" {
		fmt.Printf("[search] query=%q limit=%d\n\n", *srch, *limitFlg)
		results, err := store.Search(context.Background(), *srch, *limitFlg, "")
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No matches found.")
			return nil
		}
		for i, r := range results {
			fmt.Printf("[%d] score=%.3f  %s  [%s]\n", i+1, r.SimilarityScore, r.DocumentTitle, r.SourceCategory)
			fmt.Printf("      file: %s (page %d)\n", r.SourceFile, r.PageNumber)
			truncated := r.Content
			if len(truncated) > 300 {
				truncated = truncated[:300] + "..."
			}
			fmt.Printf("      %s\n", truncated)
		}
		return nil
	}

	ix, err := ragindex.New(store, ragindex.Options{
		DocsDir:      cfg.RAGDocsDir,
		ChunkSize:    cfg.RAGChunkSize,
		ChunkOverlap: cfg.RAGChunkOverlap,
		Category:     *catFlag,
		Mode:         mode,
		Verbose:      *verbose,
		Force:        *forceLog,
	})
	if err != nil {
		return fmt.Errorf("create indexer: %w", err)
	}

	return ix.Run()
}
