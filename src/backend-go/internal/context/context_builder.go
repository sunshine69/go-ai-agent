package context

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/domainconfig"
	"github.com/stevek/go-ai-agent/backend-go/internal/mcpclient"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
)

// ContextBuilder is a port of src/backend/app/context_builder.py::ContextBuilder.
// It extracts keywords, orders search terms, routes to MCP tools based on the
// domain/sub-category configuration, and runs MCP + RAG retrieval concurrently.
//
// It is safe for concurrent use: BuildContext spawns goroutines guarded by the
// caller's Manager (each call is an independent MCP request).
type ContextBuilder struct {
	mcpEnabled bool
	manager    *mcpclient.ResilientMCPClient
	ragStore   *ragstore.RAGStore
}

// New builds a ContextBuilder. The manager may be nil when MCP is disabled. The
// ragStore may be nil when RAG is disabled.
func New(cfg *config.Config, manager *mcpclient.ResilientMCPClient, ragStore *ragstore.RAGStore) *ContextBuilder {
	return &ContextBuilder{
		mcpEnabled: cfg.MCPEnabled,
		manager:    manager,
		ragStore:   ragStore,
	}
}

var (
	confluenceSearchRefRe = regexp.MustCompile(`^## (.+?) \(ID: (\d+)\)$`)
	confluenceReadPageTtl = regexp.MustCompile(`^# (.+)`)
	confluenceReadPageID  = regexp.MustCompile(`Page ID: (\d+)`)
)

// extractConfluenceRefs mirrors the module-level helper in context_builder.py,
// parsing both the search result heading and the read-page "Page ID" layout.
func extractConfluenceRefs(toolName, text string) []map[string]string {
	refs := []map[string]string{}
	if toolName == "confluence_search" {
		for _, m := range confluenceSearchRefRe.FindAllStringSubmatch(text, -1) {
			refs = append(refs, map[string]string{"title": m[1], "id": m[2]})
		}
	} else if toolName == "confluence_read_page" {
		if tm := confluenceReadPageTtl.FindStringSubmatch(text); tm != nil {
			if im := confluenceReadPageID.FindStringSubmatch(text); im != nil {
				refs = append(refs, map[string]string{"title": tm[1], "id": im[1]})
			}
		}
	}
	return refs
}

// DomainKeywords returns the domain's keyword list from the loaded config.
func DomainKeywords(domain string) []string {
	if d, ok := domainconfig.Load()[domain]; ok {
		return d.Keywords
	}
	return nil
}

// SubCategoryKeywords returns the sub-category keyword list from the loaded config.
func SubCategoryKeywords(domain, subCategory string) []string {
	d, ok := domainconfig.Load()[domain]
	if !ok {
		return nil
	}
	if sc, ok := d.SubCategories[subCategory]; ok {
		return sc.Keywords
	}
	return nil
}

// getSearchTerms orders message keywords first, then sub-category, then domain
// keywords (broadest/lowest priority). Mirrors Python get_search_terms.
func getSearchTerms(message, domain, subCategory string) []string {
	seen := map[string]bool{}
	var ordered []string
	add := func(kw string) {
		kw = strings.ToLower(kw)
		if !seen[kw] {
			seen[kw] = true
			ordered = append(ordered, kw)
		}
	}

	for _, kw := range extractKeywordsFromMessage(message) {
		add(kw)
	}
	for _, kw := range SubCategoryKeywords(domain, subCategory) {
		add(kw)
	}
	for _, kw := range DomainKeywords(domain) {
		add(kw)
	}
	return ordered
}

// toolsToCall returns the (tool_name, args) list for a domain + sub-category,
// scoping confluence_search to the domain's Confluence spaces when configured.
func toolsToCall(domain, subCategory string) []struct {
	name string
	args map[string]any
} {
	defaultTools := []struct {
		name string
		args map[string]any
	}{
		{"confluence_search", map[string]any{"keyword": "*"}},
		{"documents_search", map[string]any{"keyword": "*"}},
		{"forms_search", map[string]any{"keyword": "*"}},
		{"process_search", map[string]any{"keyword": "*"}},
	}

	if domain == "" {
		return defaultTools
	}
	d, ok := domainconfig.Load()[domain]
	if !ok || subCategory == "" {
		return defaultTools
	}
	sc, ok := d.SubCategories[subCategory]
	if !ok || len(sc.Tools) == 0 {
		return defaultTools
	}

	tools := make([]struct {
		name string
		args map[string]any
	}, 0, len(sc.Tools))
	for _, te := range sc.Tools {
		if te.Tool != "" {
			tools = append(tools, struct {
				name string
				args map[string]any
			}{te.Tool, te.Args})
		}
	}

	if len(d.ConfluenceSpaces) > 0 {
		spaceFilter := strings.Join(d.ConfluenceSpaces, ",")
		for i := range tools {
			if tools[i].name == "confluence_search" {
				tools[i].args["space"] = spaceFilter
			}
		}
	}
	return tools
}

// callMCPTool runs a single MCP tool call, returning the raw text result or an
// error string on failure.
func (b *ContextBuilder) callMCPTool(name string, args map[string]any) string {
	if b.manager == nil {
		return "MCP tool '" + name + "' error: MCP manager unavailable"
	}
	fmt.Printf("[DBG-MCP] %s START args=%v\n", name, args)
	result, err := b.manager.CallTool(name, args)
	if err != nil {
		fmt.Printf("[DBG-MCP] %s ERROR after timeout: %v\n", name, err)
		return "MCP tool '" + name + "' error: " + err.Error()
	}
	fmt.Printf("[DBG-MCP] %s RETURNED (took ~s)\n", name)
	return result
}

// ragSearch runs a single RAG search, returning the formatted text block or an
// error string on failure.
func (b *ContextBuilder) ragSearch(ctx context.Context, query string) taskResult {
	if b.ragStore == nil {
		fmt.Printf("[DBG-RAG] ragSearch query=%q — RAG store is nil, returning error\n", query)
		return taskResult{text: "RAG search error: RAG store unavailable"}
	}
	raw, err := b.ragStore.Search(ctx, query, 5, "")
	if err != nil {
		fmt.Printf("[DBG-RAG] ragSearch query=%q — store.Search error: %v\n", query, err)
		return taskResult{text: "RAG search error: " + err.Error()}
	}
	fmt.Printf("[DBG-RAG] ragSearch query=%q — store.Search returned %d raw results (before formatting)\n", query, len(raw))
	if len(raw) == 0 {
		fmt.Printf("[DBG-RAG] ragSearch query=%q — no results, returning empty\n", query)
		return taskResult{}
	}
	seen := map[string]bool{}
	var bld strings.Builder
	sources := []string{}
	for _, r := range raw {
		if seen[r.ChunkID] {
			continue
		}
		seen[r.ChunkID] = true
		page := r.PageNumber
		bld.WriteString("=== ")
		bld.WriteString(r.DocumentTitle)
		bld.WriteString(" (page ")
		bld.WriteString(itoa(page))
		bld.WriteString("): ")
		content := r.Content
		if len(content) > 500 {
			content = content[:500]
		}
		bld.WriteString(content)
		bld.WriteString("...")
		bld.WriteString("\n")
		// Mirror the Python build_context: each RAG result contributes its
		// source_file as a reported source (falling back to document title).
		src := r.SourceFile
		if src == "" {
			src = r.DocumentTitle
		}
		if src != "" && !containsSource(sources, src) {
			sources = append(sources, src)
		}
	}
	fmt.Printf("[DBG-RAG] ragSearch query=%q — collected %d source files\n", query, len(sources))
	return taskResult{text: bld.String(), sources: sources}
}

// BuildContext runs MCP + RAG retrieval concurrently and returns the assembled
// context text, the list of source names, and the Confluence page references.
// Mirrors context_builder.py::build_context.
func (b *ContextBuilder) BuildContext(message, domain, subCategory string) (string, []string, []map[string]string) {
	contextText, sources, confluenceRefs := "", []string{}, []map[string]string{}

	fmt.Printf("[DBG-RAG] BuildContext start: mcpEnabled=%v ragStore=%v domain=%q subCategory=%q message=%q\n",
		b.mcpEnabled, b.ragStore, domain, subCategory, message)

	searchTerms := getSearchTerms(message, domain, subCategory)
	fmt.Printf("[DBG-RAG] searchTerms=%v\n", searchTerms)

	tools := b.toolsForDomain(domain, subCategory, searchTerms)
	fmt.Printf("[DBG-RAG] toolsForDomain => %d tool tasks (mcpEnabled=%v)\n", len(tools), b.mcpEnabled)

	// Build the task list: MCP calls first, then RAG. Run concurrently.
	tasks := make([]task, 0, len(tools)+1+len(searchTerms))
	for _, t := range tools {
		tasks = append(tasks, task{taskType: "mcp", toolName: t.toolName, args: t.args, query: t.query})
	}
	ragQueries := []string{message}
	for _, term := range searchTerms {
		if strings.EqualFold(term, message) {
			continue
		}
		ragQueries = append(ragQueries, term)
		break // Python only injects one extra: the first non-matching search term.
	}
	fmt.Printf("[DBG-RAG] ragQueries=%d queries: %v\n", len(ragQueries), ragQueries)
	for _, q := range ragQueries {
		tasks = append(tasks, task{taskType: "rag", query: q})
	}

	// Run all tasks concurrently, preserving result ordering.
	ctx, cancel := context.WithTimeout(context.Background(), 60000000000) // 60s
	defer cancel()
	results := make([]taskResult, len(tasks))
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func(i int, t task) {
			defer wg.Done()
			results[i] = b.runTask(ctx, t)
		}(i, tasks[i])
	}
	wg.Wait()

	for i, t := range tasks {
		res := results[i]
		if res.text == "" {
			continue
		}
		if t.taskType == "mcp" {
			if strings.Contains(strings.ToLower(res.text), "error") {
				continue
			}
			sourceName := mcpSourceName(t.toolName)
			contextText += "=== " + sourceName + " Search (keyword: '" + t.query + "') ===\n" + res.text + "\n"
			if !containsSource(sources, sourceName) {
				sources = append(sources, sourceName)
			}
			for _, ref := range extractConfluenceRefs(t.toolName, res.text) {
				if ref["id"] != "" {
					confluenceRefs = append(confluenceRefs, ref)
				}
			}
		} else { // rag
			if !strings.Contains(res.text, "=== ") {
				fmt.Printf("[DBG-RAG] rag task result EMPTY for query %q (no '=== ' marker found) — RAG returned no docs\n", t.query)
				continue
			}
			contextText += "=== RAG Document Search (query: '" + t.query + "') ===\n" + res.text + "\n"
			// Mirrors the Python build_context: append each RAG result's
			// source_file to `sources` so the API reports real document
			// sources instead of falling back to "Direct Answer".
			for _, s := range res.sources {
				if !containsSource(sources, s) {
					sources = append(sources, s)
				}
			}
			fmt.Printf("[DBG-RAG] rag task query %q added %d source files (total distinct sources now %d)\n", t.query, len(res.sources), len(sources))
		}
	}

	return contextText, sources, confluenceRefs
}

// runTask dispatches a single task (MCP tool or RAG search) to its executor,
// returning both the formatted result text and the list of source names it
// represents (the latter is only populated for RAG searches).
func (b *ContextBuilder) runTask(ctx context.Context, t task) taskResult {
	if t.taskType == "mcp" {
		return taskResult{text: b.callMCPTool(t.toolName, t.args)}
	}
	return b.ragSearch(ctx, t.query)
}

// toolsForDomain builds the per-search-term tool-call list, rewriting the
// keyword/query argument to the dynamic search term and de-duplicating.
func (b *ContextBuilder) toolsForDomain(domain, subCategory string, searchTerms []string) []task {
	if !b.mcpEnabled {
		return nil
	}
	rawTools := toolsToCall(domain, subCategory)
	if len(rawTools) == 0 {
		return nil
	}

	keywordTools := map[string]bool{
		"confluence_search": true,
		"documents_search":  true,
		"forms_search":      true,
		"process_search":    true,
		"skills_search":     true, // retained for parity; dropped from config
	}

	var tasks []task
	called := map[string]bool{}
	for _, term := range searchTerms {
		for _, rt := range rawTools {
			callArgs := map[string]any{}
			for k, v := range rt.args {
				callArgs[k] = v
			}
			if keywordTools[rt.name] {
				if rt.name == "confluence_search" {
					callArgs["query"] = term
				} else {
					callArgs["keyword"] = term
				}
			}
			cacheKey := rt.name + "|" + stringifyArgs(callArgs)
			if called[cacheKey] {
				continue
			}
			called[cacheKey] = true
			tasks = append(tasks, task{taskType: "mcp", toolName: rt.name, args: callArgs, query: term})
		}
	}
	return tasks
}

// ---------- helpers ----------

type task struct {
	taskType string
	toolName string
	args     map[string]any
	query    string
}

// taskResult is the output of running one task: the formatted result text plus
// the list of source names it represents (populated only for RAG searches).
type taskResult struct {
	text    string
	sources []string
}

func mcpSourceName(toolName string) string {
	return strings.Title(strings.ReplaceAll(toolName, "_", " "))
}

func containsSource(sources []string, s string) bool {
	for _, x := range sources {
		if x == s {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func stringifyArgs(args map[string]any) string {
	var parts []string
	for k, v := range args {
		parts = append(parts, k+"="+toString(v))
	}
	return strings.Join(parts, ";")
}

func toString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []any:
		var out []string
		for _, e := range s {
			out = append(out, toString(e))
		}
		return "[" + strings.Join(out, ",") + "]"
	default:
		return "%!v"
	}
}
