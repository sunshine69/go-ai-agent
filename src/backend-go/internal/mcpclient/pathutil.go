package mcpclient

import (
	"path/filepath"
	"runtime"
	"strings"
)

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
