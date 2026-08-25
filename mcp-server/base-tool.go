package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	mcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	u "github.com/sunshine69/golang-tools/utils"
)

const MAX_OUPUT_SIZE = 20000

type BaseToolManager struct {
	AllowedPathPattern string // File path handling
	BlockedPathPattern string
}

func (t *BaseToolManager) checkPath(path string) (*mcp.CallToolResult, error) {
	// Normalize separators so regex patterns written with '/' work on Windows too.
	normalizedPath := filepath.ToSlash(path)

	if t.AllowedPathPattern != "" {
		if !regexp.MustCompile(t.AllowedPathPattern).MatchString(normalizedPath) {
			return mcp.NewToolResultText("[ERROR]"), fmt.Errorf("[ERROR] denied access for path: '%s'. Allowed path pattern: '%s'", path, t.AllowedPathPattern)
		}
	}
	if t.BlockedPathPattern != "" {
		if regexp.MustCompile(t.BlockedPathPattern).MatchString(normalizedPath) {
			return mcp.NewToolResultText("[ERROR]"), fmt.Errorf("[ERROR] denied access for path: '%s'. Blocked path pattern: '%s'", path, t.BlockedPathPattern)
		}
	}
	return nil, nil
}

func (t *BaseToolManager) listDirectory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	path := ""
	if p, ok := args["path"]; ok {
		path = fmt.Sprintf("%v", p)
	}
	cleanPath := filepath.Clean(path)
	if res, err := t.checkPath(cleanPath); err != nil {
		return res, err
	}
	entries, err := os.ReadDir(cleanPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Directory listing for: %s\n", cleanPath))
	sb.WriteString(strings.Repeat("-", 40) + "\n")
	for _, entry := range entries {
		icon := "📄 "
		if entry.IsDir() {
			icon = "📁 "
		}
		sb.WriteString(fmt.Sprintf("%s%s\n", icon, entry.Name()))
	}
	return mcp.NewToolResultText(sb.String()), nil
}

func (t *BaseToolManager) fileGlobSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	root := ""
	if r, ok := args["root"]; ok {
		root = fmt.Sprintf("%v", r)
	}
	pattern := ""
	if p, ok := args["pattern"]; ok {
		pattern = fmt.Sprintf("%v", p)
	}
	maxResults := 100
	if mr, ok := args["max_results"]; ok {
		switch v := mr.(type) {
		case float64:
			maxResults = int(v)
		case int:
			maxResults = v
		default:
			maxResults = 100
		}
	}

	cleanRoot := filepath.Clean(root)
	if res, err := t.checkPath(cleanRoot); err != nil {
		return res, err
	}
	namePattern := pattern
	recursive := false
	if strings.HasPrefix(pattern, "**/") {
		recursive = true
		namePattern = strings.TrimPrefix(pattern, "**/")
	}

	var matches []string
	err := filepath.WalkDir(cleanRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		matched := false
		var matchErr error
		if recursive {
			matched, matchErr = filepath.Match(namePattern, d.Name())
		} else {
			rel, _ := filepath.Rel(cleanRoot, path)
			matched, matchErr = filepath.Match(pattern, rel)
		}
		if matchErr != nil {
			return fmt.Errorf("invalid glob pattern: %w", matchErr)
		}
		if matched {
			matches = append(matches, path)
			if len(matches) >= maxResults {
				return io.EOF
			}
		}
		return nil
	})

	if err != nil && err != io.EOF {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d file(s) matching '%s' under %s", len(matches), pattern, cleanRoot))
	if len(matches) == maxResults {
		sb.WriteString(fmt.Sprintf(" (result capped at %d)", maxResults))
	}
	sb.WriteString(":\n")
	for _, m := range matches {
		sb.WriteString("  " + m + "\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (t *BaseToolManager) fetchUrl(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	url := ""
	if u, ok := args["url"]; ok {
		url = fmt.Sprintf("%v", u)
	}
	if url == "" {
		return mcp.NewToolResultError("Missing or invalid 'url' parameter"), nil
	}

	markdownText, err := fetchAndConvert(url)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	docSize := len(markdownText)
	if docSize > MAX_OUPUT_SIZE {
		tempDir := u.Must(os.MkdirTemp("", "aig*"))
		tempFile := filepath.Join(tempDir, "fetch-url-doc.md")
		u.CheckErr(os.WriteFile(tempFile, []byte(markdownText), 0o644), "write doc file")
		return mcp.NewToolResultText(fmt.Sprintf("Document saved to file; path '%s'. Size %d bytes. You can extract information from it using current available text extraction tools. Avoid reading the whole file with that size to avoid context overflow. Clean up the file after you no longer need it", tempFile, docSize)), nil
	}
	return mcp.NewToolResultText(markdownText), nil
}

func registerBaseTool(s *server.MCPServer, t *BaseToolManager) {
	s.AddTool(mcp.NewTool("fetch_url",
		mcp.WithDescription("Fetches a URL over HTTP and converts its HTML content into markdown text. If the content is larger than 20kb the content will be saved to a file and the result will be the file path so you can selectively read using grep using text tool"),
		mcp.WithString("url", mcp.Required(), mcp.Description("The URL to fetch and convert into markdown text")),
	), t.fetchUrl)

	s.AddTool(mcp.NewTool("list_directory",
		mcp.WithDescription("Lists the files and directories in a given path. Useful for understanding project structure."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The directory path to list. Use '.' for the current directory.")),
	), t.listDirectory)

	s.AddTool(mcp.NewTool("read_file",
		mcp.WithDescription("Reads the content of a file from the local filesystem."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The path to the file you want to read.")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		path := ""
		if p, ok := args["path"]; ok {
			path = fmt.Sprintf("%v", p)
		}
		content, err := readFileContent(path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(content), nil
	})

	s.AddTool(mcp.NewTool("file_glob_search",
		mcp.WithDescription("Searches for files matching a glob pattern under a root directory and returns their paths."),
		mcp.WithString("root", mcp.Required(), mcp.Description("The root directory to search within.")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern to match filenames (e.g. '*.go', '**/*.json').")),
		mcp.WithInteger("max_results", mcp.Description("Maximum number of results to return.")),
	), t.fileGlobSearch)
}
