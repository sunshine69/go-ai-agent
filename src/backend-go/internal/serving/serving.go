// Package serving exposes a minimal static file server for building the SPA.
//
// It serves a built SPA (e.g. the output of `npm run build` → <root>/dist) under
// a configurable URL prefix. This lets a single backend process serve both the
// JSON API (/api/*) and the SPA frontend (/frontend/*), so there is no longer a
// need to run `vite` / `npm run dev` as a separate process.
//
// The location of the directory is resolved from:
//
//   - the FRONTEND_PATH environment variable (preferred), or
//   - the command-line argument passed to the backend.
//
// If neither is provided, the server is disabled (returns a no-op handler) and a
// warning is logged. The path is interpreted relative to the process's current
// working directory, so it can be a relative path such as "../frontend/spa/dist".
package serving

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Prefix is the URL path segment under which the SPA is served.
const Prefix = "/frontend/"

// FrontendPath is the trailing-slash-less form of Prefix. A request to this
// path is redirected (302) to Prefix so that manual navigation to
// /frontend (without a slash) still loads the SPA, even though the subtree
// matcher for Prefix does not match it.
const FrontendPath = "/frontend"

// Server serves static files for a built SPA. Its zero value is not usable;
// construct it via New.
type Server struct {
	enabled bool
	root    string
}

// New builds a Server for the SPA located at path. If path is empty it falls
// back to the FRONTEND_PATH environment variable. The path is treated relative
// to the current working directory, and the resolved index.html must exist —
// otherwise New logs a warning and returns a disabled server.
//
// New strips a trailing slash from path.
func New(path string) *Server {
	if path == "" {
		path = os.Getenv("FRONTEND_PATH")
	}

	root := resolvePath(path)
	if root == "" {
		log.Printf("warning: no FRONTEND_PATH configured; frontend endpoint at %s will not be served", Prefix)
		return &Server{}
	}

	// Verify the entry file exists so we fail fast with a clear message rather
	// than serving 404s at request time.
	if err := verifyIndex(root); err != nil {
		log.Printf("warning: FRONTEND_PATH %q resolved to %q but %v; skipping", path, root, err)
		return &Server{}
	}

	return &Server{enabled: true, root: root}
}

// verifyIndex checks that the directory contains an index.html, optionally
// renaming an existing index.htm (or first .html) file to index.html. It
// returns an error if no suitable index file is present.
func verifyIndex(root string) error {
	if _, err := os.Stat(filepath.Join(root, "index.html")); err == nil {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.EqualFold(name, "index.htm") {
			return os.Rename(filepath.Join(root, name), filepath.Join(root, "index.html"))
		}
	}
	return os.ErrNotExist
}

// resolvePath joins cwd with path, then cleans the result. A path that is
// already absolute is returned as-is; an empty path returns an empty string.
func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimRight(path, "/")
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			log.Printf("warning: could not resolve relative FRONTEND_PATH %q: %v", path, err)
			return ""
		}
		path = filepath.Join(wd, path)
	}
	return filepath.Clean(path)
}

// Handler returns an http.Handler that serves the SPA at Prefix.
//
// The mux passes the full request path (including the /frontend/ prefix) to
// this handler, so the handler strips the prefix itself before serving from the
// SPA directory rooted at s.root. Requests that do not map to a real file
// (e.g. SPA client routes like /frontend/settings) are rewritten to index.html
// so the SPA client router can handle them.
func (s *Server) Handler() http.Handler {
	if !s.enabled {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return &fileServer{root: s.root}
}

// fileServer serves static files from s.root for the /frontend/* subtree. It
// strips the prefix from r.URL.Path itself (the mux does not) and never emits
// the directory 301 redirects that http.FileServer produces at the prefix root.
type fileServer struct {
	root string
}

func (f *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, Prefix)
	rel = strings.TrimPrefix(rel, "/")

	if rel == "" {
		rel = "index.html"
	}

	// Deep links that are not real files are served index.html so the SPA
	// client router can handle them.
	fi, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	if err != nil || fi.IsDir() {
		rel = "index.html"
		fi, err = os.Stat(filepath.Join(f.root, rel))
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}

	fp := filepath.Join(f.root, filepath.FromSlash(rel))
	// Prevent http.ServeFile from issuing a 301 redirect when r.URL.Path differs
	// from the served file path (e.g. /frontend/index.html). We neutralize the
	// path so ServeFile just streams the bytes.
	srv := r.Clone(r.Context())
	srv.URL.Path = "/"
	http.ServeFile(w, srv, fp)
}

// Enabled reports whether the server is active (i.e. a valid root was found).
func (s *Server) Enabled() bool {
	return s.enabled
}

// Root returns the resolved absolute serving root. It is empty when disabled.
func (s *Server) Root() string {
	return s.root
}
