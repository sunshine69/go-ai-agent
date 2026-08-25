// Documents and Forms MCP Server
// Provides MCP tools to query local document collections and forms
// This allows the LLM to dynamically retrieve document metadata and content

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Document represents a document in a collection
type Document struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Path        string            `json:"path"`
	Category    string            `json:"category"`
	Description string            `json:"description"`
	CreatedDate string            `json:"created_date"`
	UpdatedDate string            `json:"updated_date"`
	Tags        []string          `json:"tags,omitempty"`
	FormType    string            `json:"form_type,omitempty"`
}

// DocumentCollection represents a collection of documents
type DocumentCollection struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Path        string       `json:"path"`
	Documents   []Document   `json:"documents"`
}

// DocumentManager manages document collections
type DocumentManager struct {
	collections map[string]*DocumentCollection
	docsIndex   map[string]*Document // by ID
}

// NewDocumentManager creates a new document manager
func NewDocumentManager(basePath string) *DocumentManager {
	return &DocumentManager{
		collections: make(map[string]*DocumentCollection),
		docsIndex:   make(map[string]*Document),
	}
}

// LoadCollections loads all document collections from the base path
func (m *DocumentManager) LoadCollections(basePath string) error {
	// Load from collections metadata file
	collectionsFile := filepath.Join(basePath, "_collections.json")
	if fileExists(collectionsFile) {
		data, err := os.ReadFile(collectionsFile)
		if err != nil {
			return fmt.Errorf("failed to read collections: %w", err)
		}
		var collections []DocumentCollection
		if err := json.Unmarshal(data, &collections); err != nil {
			return fmt.Errorf("failed to parse collections: %w", err)
		}

		for i := range collections {
			c := &collections[i]
			m.collections[c.Name] = c
			for j := range c.Documents {
				m.docsIndex[c.Documents[j].ID] = &c.Documents[j]
			}
		}
		return nil
	}

	// Auto-discover from directory structure
	return m.autoDiscover(basePath)
}

// autoDiscover discovers documents from directory structure
func (m *DocumentManager) autoDiscover(basePath string) error {
	// Walk directories and build collections
	dir, err := os.ReadDir(basePath)
	if err != nil {
		return err
	}

	for _, entry := range dir {
		if !entry.IsDir() || entry.Name() == "_collections.json" {
			continue
		}

		// Create collection for this directory
		coll := &DocumentCollection{
			Name:     entry.Name(),
			Path:     filepath.Join(basePath, entry.Name()),
		}

		// Read directory entries
		entries, err := os.ReadDir(coll.Path)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if e.IsDir() || !isDocumentFile(e.Name()) {
				continue
			}

			doc := Document{
				ID:       fmt.Sprintf("%s-%s", coll.Name, e.Name()),
				Title:    strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
				Path:     filepath.Join(coll.Path, e.Name()),
				Category: coll.Name,
				Tags:     []string{e.Name()},
			}

			coll.Documents = append(coll.Documents, doc)
			m.docsIndex[doc.ID] = &doc
		}

		m.collections[coll.Name] = coll
	}

	return nil
}

// isDocumentFile checks if a file is a document file
func isDocumentFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	docExts := []string{".md", ".txt", ".docx", ".pdf", ".xlsx", ".json", ".csv"}
	for _, e := range docExts {
		if ext == e {
			return true
		}
	}
	return false
}

// ListCollections lists all document collections
func (m *DocumentManager) ListCollections() *DocumentCollection {
	var allDocs []Document
	for _, coll := range m.collections {
		coll.Description = fmt.Sprintf("Collection: %s (%d documents)", coll.Name, len(coll.Documents))
		allDocs = append(allDocs, coll.Documents...)
	}

	return &DocumentCollection{
		Name:      "All Collections",
		Documents: allDocs,
	}
}

// GetCollection retrieves a specific collection
func (m *DocumentManager) GetCollection(name string) *DocumentCollection {
	return m.collections[name]
}

// SearchDocuments searches for documents by keyword
func (m *DocumentManager) SearchDocuments(keyword string) []Document {
	var results []Document
	keyword = strings.ToLower(keyword)

	for _, doc := range m.docsIndex {
		if strings.Contains(strings.ToLower(doc.Title), keyword) ||
			strings.Contains(strings.ToLower(doc.Description), keyword) ||
			containsKeywordInTags(doc.Tags, keyword) {
			results = append(results, *doc)
		}
	}

	return results
}

// GetDocument retrieves a specific document
func (m *DocumentManager) GetDocument(docID string) *Document {
	return m.docsIndex[docID]
}

// GetDocumentContent retrieves the content of a document by ID
func (m *DocumentManager) GetDocumentContent(docID string) (string, error) {
	doc := m.GetDocument(docID)
	if doc == nil {
		return "", fmt.Errorf("document not found: %s", docID)
	}

	content, err := os.ReadFile(doc.Path)
	if err != nil {
		return "", fmt.Errorf("failed to read document: %w", err)
	}

	return string(content), nil
}

// ListForms lists all request forms across all collections
func (m *DocumentManager) ListForms() []Document {
	var forms []Document
	for _, doc := range m.docsIndex {
		if strings.Contains(strings.ToLower(doc.Title), "form") ||
			strings.Contains(strings.ToLower(doc.Title), "request") {
			forms = append(forms, *doc)
		}
	}
	return forms
}

// SearchForms searches for forms by keyword
func (m *DocumentManager) SearchForms(keyword string) []Document {
	forms := m.ListForms()
	var results []Document
	keyword = strings.ToLower(keyword)

	for _, form := range forms {
		if strings.Contains(strings.ToLower(form.Title), keyword) ||
			strings.Contains(strings.ToLower(form.Description), keyword) {
			results = append(results, form)
		}
	}

	return results
}

// MCP Tool Handlers

func handleDocumentsList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getDocumentManager()
	if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	collection := req.GetString("collection", "")

	if collection == "" {
		// List all collections
		var sb strings.Builder
		sb.WriteString("Available document collections:\n\n")

		for name, coll := range manager.collections {
			sb.WriteString(fmt.Sprintf("### %s\n", name))
			sb.WriteString(fmt.Sprintf("- Documents: %d\n", len(coll.Documents)))
			sb.WriteString(fmt.Sprintf("- Path: %s\n\n", coll.Path))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}

	// List documents in a specific collection
	coll := manager.GetCollection(collection)
	if coll == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Collection not found: %s. Available collections: %v", collection, getAvailableCollections())), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Documents in collection: %s (%d documents)\n\n", coll.Name, len(coll.Documents)))

	for _, doc := range coll.Documents {
		sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", doc.Title, doc.ID))
		sb.WriteString(fmt.Sprintf("Category: %s\n", doc.Category))
		sb.WriteString(fmt.Sprintf("Path: %s\n", doc.Path))
		if doc.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", doc.Description))
		}
		if len(doc.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(doc.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleDocumentsSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getDocumentManager()
	if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	keyword := req.GetString("keyword", "")

	if keyword == "" {
		return mcp.NewToolResultError("Missing required parameter: keyword"), nil
	}

	// Search all documents
	documents := manager.SearchDocuments(keyword)

	if documents == nil {
		return mcp.NewToolResultText(fmt.Sprintf("No documents found matching: %s\n\nTry searching for: 'form', 'procedure', 'onboarding', 'collection', 'thyroid', etc.", keyword)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d documents matching '%s':\n\n", len(documents), keyword))

	for _, doc := range documents {
		sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", doc.Title, doc.ID))
		sb.WriteString(fmt.Sprintf("Category: %s\n", doc.Category))
		sb.WriteString(fmt.Sprintf("Path: %s\n\n", doc.Path))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleDocumentsGetContent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getDocumentManager()
	if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	docID := req.GetString("document_id", "")

	if docID == "" {
		return mcp.NewToolResultError("Missing required parameter: document_id"), nil
	}

	doc := manager.GetDocument(docID)
	if doc == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Document not found: %s. Available documents: %v", docID, getAvailableDocuments())), nil
	}

	content, err := manager.GetDocumentContent(docID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", doc.Title))
	sb.WriteString(fmt.Sprintf("**ID:** %s\n", doc.ID))
	sb.WriteString(fmt.Sprintf("**Category:** %s\n", doc.Category))
	sb.WriteString(fmt.Sprintf("**Path:** %s\n\n", doc.Path))

	sb.WriteString("## Content\n\n")
	sb.WriteString(content)

	return mcp.NewToolResultText(sb.String()), nil
}

func handleFormsList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getDocumentManager()
	if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	collection := req.GetString("collection", "")

	if collection == "" {
		// List all forms
		forms := manager.ListForms()

		var sb strings.Builder
		sb.WriteString("Available request forms:\n\n")

		for _, form := range forms {
			sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", form.Title, form.ID))
			sb.WriteString(fmt.Sprintf("Category: %s\n", form.Category))
			sb.WriteString(fmt.Sprintf("Path: %s\n\n", form.Path))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}

	// List forms in a specific collection
	coll := manager.GetCollection(collection)
	if coll == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Collection not found: %s", collection)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Forms in collection: %s\n\n", coll.Name))

	for _, doc := range coll.Documents {
		if strings.Contains(strings.ToLower(doc.Title), "form") ||
			strings.Contains(strings.ToLower(doc.Title), "request") {
			sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", doc.Title, doc.ID))
			sb.WriteString(fmt.Sprintf("Category: %s\n", doc.Category))
			sb.WriteString(fmt.Sprintf("Path: %s\n\n", doc.Path))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleFormsSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	manager := getDocumentManager()
	if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	keyword := req.GetString("keyword", "")

	if keyword == "" {
		// List all forms
		forms := manager.ListForms()

		var sb strings.Builder
		sb.WriteString("Available request forms:\n\n")

		for _, form := range forms {
			sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", form.Title, form.ID))
			sb.WriteString(fmt.Sprintf("Category: %s\n", form.Category))
			sb.WriteString(fmt.Sprintf("Path: %s\n\n", form.Path))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}

	// Search forms by keyword
	forms := manager.SearchForms(keyword)

	if forms == nil {
		return mcp.NewToolResultText(fmt.Sprintf("No forms found matching: %s\n\nTry searching for: 'blood', 'thyroid', 'hba1c', 'lipid', etc.", keyword)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d forms matching '%s':\n\n", len(forms), keyword))

	for _, form := range forms {
		sb.WriteString(fmt.Sprintf("## %s (ID: %s)\n", form.Title, form.ID))
		sb.WriteString(fmt.Sprintf("Category: %s\n", form.Category))
		sb.WriteString(fmt.Sprintf("Path: %s\n\n", form.Path))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// Helper functions
func getAvailableCollections() []string {
	manager := getDocumentManager()
	if manager.collections == nil {
		if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
			return nil
		}
	}

	var names []string
	for name := range manager.collections {
		names = append(names, name)
	}
	return names
}

func getAvailableDocuments() []string {
	manager := getDocumentManager()
	if manager.docsIndex == nil {
		if err := manager.LoadCollections(getDocumentsBasePath()); err != nil {
			return nil
		}
	}

	var ids []string
	for id := range manager.docsIndex {
		ids = append(ids, id)
	}
	return ids
}

func containsKeywordInTags(tags []string, keyword string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), keyword) {
			return true
		}
	}
	return false
}

// Global manager instance
var globalDocumentManager *DocumentManager

func getDocumentManager() *DocumentManager {
	if globalDocumentManager == nil {
		basePath := os.Getenv("DOCUMENTS_BASE_PATH")
		globalDocumentManager = NewDocumentManager(basePath)
	}
	return globalDocumentManager
}

func getDocumentsBasePath() string {
	return os.Getenv("DOCUMENTS_BASE_PATH")
}

// RegisterDocumentsTools registers documents and forms MCP tools
func RegisterDocumentsTools(s *server.MCPServer) error {
	// Check if documents directory exists
	basePath := os.Getenv("DOCUMENTS_BASE_PATH")
	if basePath == "" {
		return fmt.Errorf("DOCUMENTS_BASE_PATH environment variable not set — documents tools unavailable")
	}

	if !dirExists(basePath) {
		return fmt.Errorf("documents base path does not exist: %s", basePath)
	}

	s.AddTool(mcp.NewTool("documents_list",
		mcp.WithDescription("List all document collections, or list documents in a specific collection if a collection is specified. Useful for discovering what documents are available."),
		mcp.WithString("collection", mcp.Description("The collection to list. If not provided, lists all collections. Available collections: confluence, procedures, forms, faq, skills")),
	), handleDocumentsList)

	s.AddTool(mcp.NewTool("documents_search",
		mcp.WithDescription("Search for documents by keyword. Returns documents matching the keyword in their title, description, or tags. Useful for finding specific documents when you don't know the exact name."),
		mcp.WithString("keyword", mcp.Required(), mcp.Description("Keyword to search for in documents (e.g., 'form', 'procedure', 'onboarding', 'collection'))")),
	), handleDocumentsSearch)

	s.AddTool(mcp.NewTool("documents_get_content",
		mcp.WithDescription("Get the full content of a document by its ID. Returns the document title, metadata, and content. Useful for reading the full content of a specific document."),
		mcp.WithString("document_id", mcp.Required(), mcp.Description("The document ID to get content for. Get this from documents_list or documents_search results. Example IDs: confluence-fbc_request_form.md, procedures-onboard_new_site.md")),
	), handleDocumentsGetContent)

	s.AddTool(mcp.NewTool("forms_list",
		mcp.WithDescription("List all request forms across all collections, or list forms in a specific collection if a collection is specified. Useful for finding all available forms."),
		mcp.WithString("collection", mcp.Description("The collection to list forms from. If not provided, lists all forms. Available collections: confluence, procedures, forms")),
	), handleFormsList)

	s.AddTool(mcp.NewTool("forms_search",
		mcp.WithDescription("Search for request forms by keyword, or list all forms if no keyword is specified. Returns forms matching the keyword in their title or description. Useful for finding specific forms."),
		mcp.WithString("keyword", mcp.Description("Keyword to search for in forms (e.g., 'blood', 'thyroid', 'hba1c', 'lipid'). If not provided, lists all forms.")),
	), handleFormsSearch)

	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
