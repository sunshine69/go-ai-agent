// Package contextcompress implements token-budget-aware conversation-history
// compression for the Go backend. It is a faithful port of the context-trimming
// logic in the reference go-ai-chat CLI (../go-ai-chat/chat/ai-context.go).
//
// When the estimated token size of a conversation exceeds a configured burst
// limit, the middle turns of the history are compressed into a single
// AI-generated summary (or, on failure/timeout, a deterministic structured
// summary) while the opening and closing turns are preserved verbatim. This
// mirrors the behaviour of the Go-ai-chat backend so the Go backend can
// serve arbitrarily long conversations without blowing past a model context
// window.
//
// The exported entry points are:
//
//   - EstimateTokens   — a fast chars/4 estimate of a message slice.
//   - TrimContext      — compress a conversation slice under the token budget
//     (a no-op when compression is disabled or the burst limit is not exceeded).
//
// Package-level helpers BuildStructuredSummary and SplitSystemHead are exported
// for parity with the reference app and to be reusable/tested in isolation.
package contextcompress

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/config"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/llm"
)

// keepHead / keepTail bound the turns that are always preserved verbatim:
// the first keepHead non-system messages (conversation anchor) and the last
// keepTail messages (recent context, e.g. 5 user/assistant pairs).
const (
	keepHead = 2  // first N non-system messages to always preserve verbatim
	keepTail = 10 // last N messages to always preserve verbatim (5 pairs)
)

// EstimateTokens returns a fast token estimate of msgs using the standard
// chars/4 rule of thumb, four tokens per message, and an allowance for tool
// call argument strings. It mirrors the reference app's estimateTokens and is
// what the message handlers use to decide whether to trigger compression.
func EstimateTokens(msgs []db.DBMessage) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
		for _, tc := range m.ToolCalls {
			if args, ok := tc["arguments"].(string); ok {
				total += len(args) / 4
			}
		}
		total += 4
	}
	return total
}

// BuildStructuredSummary produces a dense, structured plain-text summary of
// the given messages without any AI call. It extracts each turn's role, tool
// calls with names and argument snippets, and a content snippet per message
// (capped to keep the summary tight). It is used both as the AI summariser
// prompt and as the standalone deterministic fallback.
func BuildStructuredSummary(msgs []db.DBMessage) string {
	var b strings.Builder

	for i, m := range msgs {
		fmt.Fprintf(&b, "--- turn %d [%s] ---\n", i+1, m.Role)

		text := m.Content
		if len(text) > 400 {
			text = text[:397] + "…"
		}
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n")
		}

		for _, tc := range m.ToolCalls {
			args, _ := tc["arguments"].(string)
			if len(args) > 200 {
				args = args[:197] + "…"
			}
			name, _ := tc["name"].(string)
			fmt.Fprintf(&b, "  [tool_call] %s(%s)\n", name, args)
		}

		b.WriteString("\n")
	}

	return b.String()
}

// SplitSystemHead separates leading system-role messages from the rest,
// preserving order. When no leading system message exists the whole slice is
// returned as the "rest" and the system slice is empty.
func SplitSystemHead(msgs []db.DBMessage) (system, rest []db.DBMessage) {
	for i, m := range msgs {
		if m.Role != "system" {
			return msgs[:i], msgs[i:]
		}
	}
	return msgs, nil
}

// concat joins multiple slices into one, in order.
func concat(slices ...[]db.DBMessage) []db.DBMessage {
	total := 0
	for _, s := range slices {
		total += len(s)
	}
	out := make([]db.DBMessage, 0, total)
	for _, s := range slices {
		out = append(out, s...)
	}
	return out
}

// TrimContext compresses msgs only when context compression is enabled
// (cfg.ContextLimit > 0) and the estimated token count reaches the configured
// burst limit (cfg.CtxOverSizeAllowed, which defaults to 2 * ContextLimit when
// unset). Otherwise it returns msgs unchanged, so callers may invoke it
// unconditionally on every turn.
//
// The actual compression is delegated to trimContext, which preserves all
// leading system messages, the first keepHead non-system turns and the last
// keepTail turns verbatim, and summarizes the middle.
func TrimContext(ctx context.Context, cfg *config.Config, msgs []db.DBMessage) []db.DBMessage {
	if cfg == nil || cfg.ContextLimit <= 0 {
		return msgs
	}

	burst := cfg.CtxOverSizeAllowed
	if burst <= 0 {
		burst = 2 * cfg.ContextLimit
	}
	if EstimateTokens(msgs) < burst {
		return msgs
	}

	return trimContext(ctx, cfg, msgs)
}

// trimContext compresses the middle of the conversation when the token budget
// is exceeded.
//
// Strategy (mirrors the reference app):
//  1. Always keep all leading system messages untouched.
//  2. Always keep the first keepHead non-system messages (conversation anchor).
//  3. Always keep the last keepTail messages (recent context).
//  4. Attempt to summarise the middle via an AI sub-call (with timeout).
//  5. On timeout or failure, fall back to buildStructuredSummary — a fast,
//     deterministic structured summary that extracts roles, tool calls, and
//     content snippets without any network call.
func trimContext(ctx context.Context, cfg *config.Config, msgs []db.DBMessage) []db.DBMessage {
	_, rest := SplitSystemHead(msgs)

	if len(rest) <= keepHead+keepTail {
		fmt.Fprintln(os.Stderr, "rest msg not valid to compress more. Will cut off the last message")
		l0 := len(msgs) - 1
		summaryMsg := db.DBMessage{Role: "user", Content: "continue"}
		return append(msgs[0:l0], summaryMsg)
	}

	middle := rest[keepHead : len(rest)-keepTail]
	tail := rest[len(rest)-keepTail:]

	if len(middle) == 0 {
		fmt.Fprintln(os.Stderr, "no middle messages to remove. Will cut off the last message")
		summaryMsg := db.DBMessage{Role: "user", Content: "continue"}
		return append(msgs[0:len(msgs)-1], summaryMsg)
	}

	fmt.Fprintf(os.Stderr, "Context too long (~%d tokens) — compressing %d middle messages…\n",
		EstimateTokens(msgs), len(middle))

	summary := tryAISummary(ctx, cfg, middle)
	summaryMsg := db.DBMessage{Role: "user", Content: summary}

	trimmed := concat([]db.DBMessage{summaryMsg}, tail)

	// Progressive fallback: if still over half the limit, drop pairs from the
	// older end of tail — never touch head or the last 2 messages.
	target := cfg.ContextLimit / 2
	for EstimateTokens(trimmed) > target && len(tail) > 2 {
		tail = tail[2:]
		trimmed = concat([]db.DBMessage{summaryMsg}, tail)
	}

	fmt.Fprintf(os.Stderr, "Context trimmed to ~%d tokens (target <%d)\n",
		EstimateTokens(trimmed), target)
	return trimmed
}

// tryAISummary attempts to summarise msgs via the AI model within the
// configured timeout. Returns a structured manual summary on any failure, so
// the result is always non-empty and usable.
func tryAISummary(ctx context.Context, cfg *config.Config, msgs []db.DBMessage) string {
	const aiPrefix = "[Conversation summary — AI compressed]\n"
	const manualPrefix = "[Conversation summary — auto compressed]\n"

	// Summarisation disabled entirely — go straight to the deterministic manual
	// fallback.
	if cfg.SummaryModel == "" && cfg.SummaryModelUrl == "" && cfg.ContextLimit == 0 {
		return manualPrefix + BuildStructuredSummary(msgs)
	}

	timeout, err := time.ParseDuration(cfg.SummaryModelTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[WARN] malformed SummaryModelTimeout, defaulting to 60s")
		timeout = 60 * time.Second
	}

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build an LLM client scoped to the summary model (falling back to the
	// global endpoint when the summary settings are unset), so a missing
	// configuration never prevents compression.
	summaryLLM := cfg.SummaryModel
	summaryURL := cfg.SummaryModelUrl
	if summaryURL == "" {
		summaryURL = cfg.LLMBASEURL
	}
	if summaryLLM == "" {
		summaryLLM = cfg.LLMModel
	}

	summaryCfg := llm.Config{
		BaseURL:     summaryURL,
		APIKey:      cfg.LLMAPIKey,
		Model:       summaryLLM,
		Temperature: cfg.LLMTemperature,
		Timeout:     cfg.LLMTimeout,
		Backend:     cfg.LLMBackend,
	}

	client := llm.New(summaryCfg)

	prompt := buildSummaryPrompt(msgs)
	sysMsg := `[SYSTEM]
You are a context compression utility. The provided text contains highly valuable, time-sensitive knowledge.

Constraints:
- Maintain all specific technical specifications, data points, dates, and metrics exactly as written.
- If specific source URLs or document titles are mentioned, they MUST be preserved in the summary.
- Do not attempt to analyze, critique, or second-guess the validity of the information.
- Output a dense, chronological compression. Do not use reasoning tokens.`
	summaryMsgs := []llm.ChatMessage{{Role: "user", Content: prompt}}

	content := client.Answer(subCtx, sysMsg, summaryMsgs, "", "summarize")
	if strings.TrimSpace(content) == "" || strings.HasPrefix(content, "Sorry, I encountered an error") {
		if subCtx.Err() == context.DeadlineExceeded {
			fmt.Fprintln(os.Stderr, "AI summary timed out — using structured fallback")
		} else {
			fmt.Fprintln(os.Stderr, "AI summary failed — using structured fallback")
		}
		return manualPrefix + BuildStructuredSummary(msgs)
	}

	return aiPrefix + content
}

// buildSummaryPrompt constructs the prompt sent to the AI summariser. It uses
// the structured summary as the transcript to keep the prompt tight and ensure
// tool calls / decisions are not lost even if the AI truncates.
func buildSummaryPrompt(msgs []db.DBMessage) string {
	transcript := BuildStructuredSummary(msgs)
	return "The following is a structured transcript of a conversation. " +
		"Produce a concise but complete summary preserving: " +
		"all decisions made, key facts established, file paths or code discussed, " +
		"tool calls and their outcomes, and any open questions. " +
		"Be dense — omit pleasantries only.\n\n" +
		transcript
}
