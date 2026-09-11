package mcpclient

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// IsValidWorkdir reports whether p is a valid MCP working-directory selector.
// It is the exported wrapper around the package-private isValidWorkDir, so the
// routers package (and its tests) can validate a user-supplied /mcpdir value
// without duplicating the "relative, no .. component" rules.
func IsValidWorkdir(p string) bool { return isValidWorkDir(p) }

// ResolveWorkdir cleans p relative to the process cwd and ensures the directory
// exists (creating it if missing). It is the exported wrapper around
// resolveWorkDir and returns the cleaned absolute path.
func ResolveWorkdir(p string) (string, error) { return resolveWorkDir(p) }

// isValidWorkDir reports whether p is a valid, safe working directory selector:
// it must be relative (not absolute) and contain no parent-directory traversal
// component (e.g. "../" or "..\"). A bare "." is rejected as meaningless. The
// check runs on the raw input *before* filepath.Clean so that a path like
// "foo/../bar" is rejected (it contains a traversal component) rather than
// silently collapsed to "bar".
func isValidWorkDir(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return false
	}
	if isAbsPath(p) {
		return false
	}
	// Reject any ".." segment, using the OS separator (and forward slash as a
	// safety net on Windows) so "../", "..\..", and "a/../b" are all refused.
	seps := []string{string(filepath.Separator), "/"}
	for _, sep := range seps {
		if sep == "" {
			continue
		}
		if strings.Contains(p, sep+".."+sep) ||
			strings.HasPrefix(p, ".."+sep) ||
			strings.HasSuffix(p, sep+"..") ||
			p == ".."+sep {
			return false
		}
	}
	// Also refuse a bare ".." that no separator caught.
	if p == ".." {
		return false
	}
	return true
}

// resolveWorkDir cleans p relative to the current process working directory and
// ensures the directory exists (creating it if missing). It returns the cleaned
// absolute path. The input must already have passed isValidWorkDir.
func resolveWorkDir(p string) (string, error) {
	cleaned := filepath.Clean(p)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("failed to create workdir %q: %w", abs, err)
	}
	return abs, nil
}

// isAbsPath reports whether s is an absolute path on the current OS.
func isAbsPath(s string) bool {
	if s == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		// Drive letters, UNC paths, etc.
		return len(s) >= 2 && s[1] == ':' ||
			strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, `//`)
	}
	return strings.HasPrefix(s, "/")
}

// joinPath joins path elements with the OS separator.
func joinPath(elems ...string) string {
	var parts []string
	for _, e := range elems {
		e = strings.TrimSpace(e)
		if e != "" {
			parts = append(parts, e)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return filepath.Join(parts...)
}

// filepathAbspath returns the cleaned absolute form of p, or an error.
func filepathAbspath(p string) (string, error) {
	return filepath.Abs(p)
}

// runtimeIsWindows reports whether the binary runs on Windows.
func runtimeIsWindows() bool { return runtime.GOOS == "windows" }

// hasFileExtension reports whether path contains a "." separator component.
func hasFileExtension(path string) bool {
	_, ext := filepath.Split(path)
	return ext != "" && strings.HasPrefix(ext, ".")
}
