# Plan: From Server-Side Retrieval to MCP Tool-Use (Function Calling) + Multi-Turn Conversation

**Status:** Draft for review — not yet implemented.
**Scope:** Go backend (`src/backend-go`) + frontend (`src/frontend/spa`) minimal changes.
**Goal:** The model decides *which* MCP tools to call and *with what arguments*, across multiple conversation turns, instead of the server pre-fetching context by fixed keywords and pasting it into the prompt.

---

## 1. The Baseline (current behavior) — and why we're changing it

### 1.1 Current flow (single turn)

```
User asks question
      │
SPA  POST /api/messages/stream   (useStreaming.ts)
      │
backend handleStreamChat / proxyLLMStream  (routers/messages_stream.go)
      │
ContextBuilder.BuildContext(msg, domain, subCategory)   (context/context_builder.go)
      │   • getSearchTerms(): keyword list [msg-words, subcategory-kw, domain-kw]
      │   • toolsForDomain(): build tool tasks
      │          confluence_search|keyword=term, documents_search|keyword=term,
      │          forms_search|keyword=term, process_search|keyword=term
      │   • Run all MCP tools concurrently (server chooses keyword, "always")
      │   • Run RAG search concurrently (query = msg + first term)
      │   • Assemble everything into ONE big `contextText` string
      │
contextText injected as a user message:
   "Here is additional context from the knowledge base:\n\n{contextText}\n\nPlease answer: {msg}"
      │
POST {LLMBASEURL}/chat/completions?stream=true   (OpenAI-compatible)
      │   model gets [system, ...history, user-with-context], NO tools
stream tokens → SSE → SPA
```

### 1.2 The fundamental limitation

| Aspect | Current | Desired |
|---|---|---|
| **Who picks the tools** | The server (fixed config `SubCategory.Tools` + wildcard keyword per term) | **The model** |
| **Who decides when** | Every turn, always, for every configured tool | Model calls only what's needed, as needed |
| **Model sees the tools** | **No.** `tools` are never sent to the model. | **Yes.** Full tool schema in the request. |
| **Decision reasoning** | Code (`getSearchTerms` / `toolsToCall`) | Model's own judgment (and prior turns) |
| **Argument selection** | Hardcoded `keyword=term` (and `*`) | Model supplies real arguments (`query`, `document_id`, `id`, …) |
| **Multi-turn** | History is sent, but retrieval logic is stateless per-turn; model can never trigger a tool call. | Model sees full tool-call history and can call tools on turn 2, 3, … to fill gaps. |

The `ContextBuilder` is currently the **sole agent** in the pipeline. It is a *knowledge-grounding* design: "gather facts, hand them to the model, ask the model to paraphrase."

### 1.3 Evidence in the code (important — a scaffold already exists)

- `mcpclient/mcpclient.go`: the MCP client **already implements the full MCP protocol** — `tools/list`, `tools/call`, `resources/list`, `resources/read` (JSON-RPC 2.0 over stdio *and* streamable HTTP). It also has `ToOpenAITools()` which converts MCP tool descriptors into OpenAI `tools` specs.
- **`ToOpenAITools()` is defined but never called.** This is a dead scaffold — the code was likely written toward this feature and never finished.
- `llm/llm.go` `CompletionRequest` has `Model`, `Messages`, `Temperature`, `MaxTokens`, `Stream` — **no `Tools` field.** The model is structurally incapable of receiving a tools list today.
- Multi-turn works only for *prompt continuity*: `handleStreamChat` appends `conv.Messages` to `msgs`, but there is no mechanism to store/replay model-emitted `tool_calls` / `tool_results`.

> **Takeaway:** We are not building a new MCP transport. We are flipping the one line the server controls — instead of *running* the tools, the server *hands them to the model* and acts on the model's choices.

---

## 2. Target behavior

1. On a user turn, the backend loads the conversation's stored message history (including any prior tool calls/results).
2. It converts every available MCP tool into an OpenAI-compatible `tools` array (name + description + JSON schema for `arguments`) using the existing `ToOpenAITools()`.
3. It sends `{system, history, user, tools, tool_choice: "auto"}` to the model.
4. The model responds with **either**:
   - **A normal text answer**, or
   - **A tool-call turn**: an array of `{id, type:"function", function:{name, arguments(json-string)}}`.
5. **Tool execution loop:** for each requested tool, the backend runs it **through the MCP client** (`Manager.CallTool(name, args)`), appends a `tool_calls` message (the model's request) and a `tool_result` message (the MCP answer, role `tool`), and **re-sends to the model**.
6. Loop until the model stops calling tools and emits its final text answer.
7. That final answer streams back to the SPA, token-by-token, unchanged for the client.
8. **History is saved including tool calls and results**, so the next turn can build on them.

This is the standard OpenAI-style *function-calling / tool-use* turn-taking. It inherently gives multi-turn: history (now including tool exchanges) is passed back each turn, so the model can call tools, inspect results, and call *more* tools before answering.

---

## 3. Design

### 3.1 New / changed components

| Component | File | Change |
|---|---|---|
| **LLM request model** | `internal/llm/llm.go` | Add `Tool` + `ToolChoice` types + `Tools`/`ToolChoice` fields on `CompletionRequest` (OpenAI-compatible JSON). |
| **Tool schema provider** | `internal/tools/toollist.go` (new) | Given the live `Manager`, return `[tool, …]` for what the model may call. Reuses `mcpclient.Tools()`. |
| **Tool executor** | `internal/tools/toolrunner.go` (new) | Given `tool` (name) + args (json string), call `Manager.CallTool(...)`, sanitize result into a JSON string or `"error: …"`. |
| **Tool-use controller** | `internal/tooluse/controller.go` (new, core) | Orchestrates the loop: load history → build tools → send → if `tool_calls`, execute + append → re-send → until final text. Returns the assistant text (or errors). Reusable by both stream paths. |
| **Streaming handlers** | `internal/routers/messages_stream.go` | Route `handleStreamChat` / `proxyLLMStream` through the new `ToolUseController` instead of the old `ContextBuilder`+text-injection for the RAG/tools path. (See §6.) |
| **Non-stream handler** | `internal/routers/messages.go` | Same routing. |
| **Frontend** | `src/frontend/spa/src/hooks/useStreaming.ts` | **No functional change.** MCP tool calls happen server-side; the client still only streams tokens. No UI to change. |

### 3.2 OpenAI-compatible request shape (what the model now receives)

```jsonc
{
  "model": "<LLM_MODEL>",
  "messages": [
    { "role": "system",  "content": "<GenIQ/Friendly prompt>" },
    { "role": "user",    "content": "What is the expense cap?" },
    { "role": "assistant", "content": null,
      "tool_calls": [
        { "id": "call_1", "type": "function",
          "function": { "name": "documents_search",
                        "arguments": "{\"keyword\": \"expense\", \"limit\": 5 }" } ] },
    { "role": "tool",    "tool_call_id": "call_1",
      "content": "[{\"document_id\":\"12\",\"title\":\"Cap Policy\",\"text\":\"...\"}]" }
  ],
  "tools": [
    { "type": "function", "function": {
        "name": "documents_search",
        "description": "Search the documents corpus…",
        "parameters": { "type": "object", "properties": {
                            "keyword": {"type":"string"},
                            "limit":   {"type":"integer"} },
                          "required": ["keyword"] } } },
    // … all other MCP tools
  ],
  "tool_choice": "auto",
  "temperature": 0.1,
  "stream": false
}
```

> **Note on local servers:** `tool_choice` accepts `"auto"` in OpenAI and llama.cpp/ollama. A **fallback mode** (see §4) strips `tools`/`tool_choice` and just sends `{messages}` if the server errors on unknown fields.

### 3.3 Tool arguments marshaling

MCP tool args arrive as a **JSON string** (that's how OpenAI emits them). `toolrunner.go`:

```go
func (t *ToolRunner) run(toolName, argsJSON string) (string, error) {
    var args map[string]any
    if err := json.Unmarshal([]byte(strings.TrimSpace(argsJSON)), &args); err != nil || args == nil {
        args = map[string]any{}
    }
    return t.manager.CallTool(toolName, args)
}
```

The backend's existing tool args (`{query:, keyword:, space:, document_id:, id:, …}`) come straight from the model — no server-injected keywords. This is the behavioral core of the change.

### 3.4 Tool result shape (role "tool")

```json
{ "role": "tool", "tool_call_id": "call_1",
  "content": "[{\"document_id\":\"12\",\"title\":\"Cap Policy\",\"text\":\"…\"] }
```

For tool-call turns we persist the **raw model text** exactly as a normal assistant turn (preserving the current `accContent`/persistence behavior and the SPA's expected shape), and only *additionally* keep a serialized `tool_calls` array for the model. For `tool_result` turns we store the `content` as `role:"tool"`.

### 3.5 Conversation / history storage impact

- `conv.Messages` gains an **optional** `tool_calls` field (JSON-encoded), populated only on assistant turns that requested tools.
- New **tool_result rows** (`role:"tool"`, linking `tool_call_id`) must be persisted for round-trip fidelity.
- **Persistence must not choke on the old rows.** On load, skip any row whose role is `"tool"` (older conversations have none).
- **Scope decision (see §6):** the controller needs the *persisted* messages of the current conversation, but historically it built history from a fresh empty conversation. For the tool-use path the conversation is created up front by the caller — so the controller can read `conv.Messages` directly.
- **`persistMessagesBatch`**: add the new rows to its `role` allowlist (`"user"|"assistant"|"tool"`).
- **Frontend**: does not parse tool messages. It ignores them and reads `event: message` tokens for display — same as now.

### 3.6 Error handling

- A tool that errors is returned to the model as `content: "error: <reason>"` with `isError` semantics — the model can recover, refine args, or call another tool; it decides.
- If `Manager` is nil / MCP disabled, skip `tools`/`tool_choice` (fall back to plain text mode) so plain questions still work.
- If the *server* rejects `tools` (fallback mode), retry once with `{messages}` only.
- Loop guard: max tool-calls per turn (e.g. 5) to prevent runaway loops; throw a clear error past that.

---

## 4. Rollout strategy — **hybrid first (zero new model capability risk)**

The model may be a local server with **incomplete tool-call support**. To avoid shipping a half-working feature, ship the *new controller* **behind a capability probe**, so behavior can flip automatically:

**Phase A — Hybrid (safe baseline).**
- Keep `ContextBuilder` as the current default.
- Add the `ToolUseController`.
- **Probe:** before each turn, send a tiny request with **one** tool. If the server returns `tool_calls`, we know tool-use works → switch to tool-use mode. If it errors, stay in hybrid mode.
- Config knobs (env or YAML), so operators can force either way without a deploy:
  - `FEATURE_TOOL_USE: false|auto|true` — `false` = hybrid always; `true` = tool-use always; `auto` = probe.
  - `MODEL_MAX_TOOL_CALLS: 5`

**Phase B — Tool-use as default (once confirmed working with the live server).**
- Flip `FEATURE_TOOL_USE` default to `auto`/`true`.
- Phase A controller becomes the default `useTool()`; `useHybrid()` becomes a compatibility fallback.
- Remove the probe from hot path after a warm confirmation, or keep it cheap.

**Phase C — Rich features.**
- Multi-turn tool use fully enabled (see below).
- Streaming tool calls to client (optional; not required).

> This means the plan can be delivered and validated with the current model **today**, and graduate to full tool-use once the server supports it.

---

## 5. Multi-turn conversation — concrete design

Today, history is a flat list of user/assistant messages. **Tool use is only truly multi-turn if tool calls and their results survive across turns.** Concretely:

1. **Turn 1** — user: "What's my 2024 expense cap?"
   - model → `tool_calls:[{documents_search, {"keyword":"expense","year":"2024"}}]`
   - server → executes tool, appends `tool_result`
   - model → final text answer
   - **persisted:** user + assistant(tool_calls) + tool_result + assistant(answer)
2. **Turn 2** — user: "Now apply it to my September trip."
   - server loads the full history above (including the earlier tool_calls/results)
   - model now *sees* that `documents_search` already returned the cap policy, and can:
     - call the **same tool again** with new args (`trip:true`, `month:"September"`), or
     - call a **different** tool, or
     - reason about the prior result without another call.
   - server loops again as needed, then answers.

**Design guarantees:**
- Tool results from turn N are visible to the model on turn N+1 (they're in history).
- The model can **chain** tools within a single turn (call A → inspect → call B → answer) *and* across turns.
- Context window pressure is a concern (tool results add tokens). Mitigations: (a) `RAG_SCORE_THRESHOLD`-style cap on tool-result size, (b) the `truncate` helper already exists, (c) consider summarizing long tool results. **Flagged for a follow-up design note.**

---

## 6. Key open decisions (need your input)

1. **Which stream endpoint is canonical?** We have `handleStreamChat` (POST `/api/chat/stream`) and `proxyLLMStream` (POST `/api/messages/stream`). Do we rewrite **both**, or port one over and deprecate the other? *(Recommend: both, to keep parity.)*
2. **Where does history come from for the controller?** Historically history was assembled from a fresh/empty conversation. For tool-use we *need* the real stored history. Is it safe to create the conversation up front for the tool-use path? *(Recommend: yes — and this also fixes the earlier "two conversations" concern.)*
3. **Streaming tool calls to the client?** Optional/Phase C. The MCP results can arrive server-side without the SPA knowing. *(Recommend: no for Phase A/B.)*
4. **Scope of `ContextBuilder`?** Keep it as the hybrid path, or remove it once tool-use is default? *(Recommend: keep as fallback until fully verified.)*
5. **Tool result visibility for citations/sources?** Today the SPA shows `sources` from RAG. Tool-use changes the source metadata flow. *(Recommend: keep passing `sources`/`citations` where meaningful; add MCP tools to the sources list by tool name.)*

---

## 7. Proposed build order

1. `internal/llm/llm.go` — add `Tool`/`ToolChoice` + `Tools`/`ToolChoice` fields.
2. `internal/tools/toollist.go` — `Provider` (list live tools); `internal/tools/toolrunner.go` — `Runner` (execute + sanitize).
3. `internal/tooluse/controller.go` — `Controller.useTool(history, user)` loop + hybrid probe.
4. Wire behind `FEATURE_TOOL_USE` probe in `messages_stream.go` (both endpoints).
5. Persistence: `tool_calls` on assistant rows, `tool_result` rows, skip `role:"tool"` on old loads, extend `persistMessagesBatch` allowlist.
6. Phase A tests: `useTool()` runs a real tool via MCP client end-to-end, including the fallback path.
7. Phase B: flip default; remove probe; add streaming-if-confirmed.
8. Phase C: multi-turn chaining + optional client-side tool-call streaming.

---

## 8. What is deliberately OUT of scope (for now)

- Frontend UI for displaying tool calls/results (server-side only; client unchanged).
- Streaming `tool_calls` to the client.
- MCP resource subscriptions beyond read.
- Reworking `ContextBuilder`/RAG — it remains the hybrid baseline.

---

## 9. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Local server lacks full tool-call support | Capability probe + `FEATURE_TOOL_USE` switch (Phase A) |
| Tool-call loop runaway | `MODEL_MAX_TOOL_CALLS` guard |
| Tool args in wrong shape | `toolrunner` unmarshals + defaults to `{}` on failure |
| History/tool-call persistence on old data | `role` allowlist; skip `role:"tool"` rows from old DBs |
| Context window bloat from tool results | size caps / `truncate` |
| Source/citation metadata churn | keep passing `sources`/`citations`; add tool names |

---

**End of draft. Awaiting your review and decisions on §6 before coding.**
