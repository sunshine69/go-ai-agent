import re

path = "src/backend-go/internal/routers/routers.go"
with open(path) as f:
    content = f.read()

misplaced = '''// applyUserLLMURL returns the effective LLM base-URL for the request's
// authenticated caller, honouring their stored per-user override (via /url)
// over the configured default. When the user has no override (or a reset value)
// it returns cfg.LLMBASEURL unchanged. It is nil-safe and MUST be applied where
// the final ".../chat/completions" endpoint is built, so a user's /url choice
// actually shapes the server a later message streams from.
func (h *Handlers) applyUserLLMURL(cfg *config.Config, r *http.Request) string {
	if cfg == nil {
		return ""
	}
	base := cfg.LLMBASEURL
	if h.DB == nil || h.DB.Settings == nil {
		return base
	}
	uid, ok := currentUserID(r)
	if !ok {
		return base
	}
	if override := resolveLLMURL(h.DB, uid); override != "" {
		return override
	}
	return base
}
'''

if misplaced not in content:
    print("ERROR: misplaced block not found!")
    raise SystemExit(1)

# Remove the misplaced block
content = content.replace(misplaced, "")

# Insert the block right before ServeMux
marker = "// ServeMux builds the router for the backend:"
insertion = misplaced + "\n" + marker

if marker not in content:
    print("ERROR: ServeMux marker not found!")
    raise SystemExit(1)

content = content.replace(marker, insertion, 1)

with open(path, "w") as f:
    f.write(content)

print("FIXED: moved applyUserLLMURL out of ServeMux")
