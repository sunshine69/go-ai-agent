import React, { useState, useEffect, useRef } from "react";
import { Domain } from "./types";
import { useStreaming, ChatMessage } from "./hooks/useStreaming";
import { useAuth } from "./hooks/useAuth";
import { Sidebar } from "./components/Sidebar";
import { DomainPills } from "./components/DomainPill";
import { SubCategoryPills } from "./components/SubCategoryPill";
import { ChatArea } from "./components/ChatArea";
import { Login } from "./components/Login";
import { AuthError, fetchWithToken, deleteJSON, setSetting, getSettings, getMCPStatus, connectMCP, disconnectMCP, getMCPWorkdir, setMCPWorkdir, getRAGWorkdir, setRAGWorkdir, getSystemPrompt, setSystemPrompt } from "./utils/api";

// Base URL for the AI backend — read from .env file
const API_BASE =
  (import.meta.env.VITE_BACKEND_URL as string) || "";

interface Conversation {
  created_at: string;
  updated_at: string;
  id: string;
  title: string;
}

// Extended ChatMessage with additional UI fields
interface Message extends ChatMessage {
  // synthetic: true for placeholder intro message on domain selection
  synthetic?: boolean;
  sources?: string[];
  confluence_links?: Array<{ title: string; url: string }>;
}

export default function App() {
  // --- State ---
  const [selectedDomain, setSelectedDomain] = useState<Domain | null>(null);
  const [selectedSubCategory, setSelectedSubCategory] = useState<string | null>(
    null
  );
  const [messages, setMessages] = useState<Message[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [currentConversationId, setCurrentConversationId] = useState("");
  const [domains, setDomains] = useState<Domain[]>([]);
  // Whether the slide-in sidebar is open (mobile only; ignored on desktop
  // where the sidebar is always visible inline).
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const toggleSidebar = () => setSidebarOpen((v) => !v);
  const closeSidebar = () => setSidebarOpen(false);

  // --- Auth ---
  const { initialized, user, login, logout } = useAuth(API_BASE, () => setNeedLogin(true));
  const [needLogin, setNeedLogin] = useState(false);
  // Resolve whether the app content should render. Until `initialized` we show
  // nothing so the user never sees the SPA behind a stale/invalid session.
  const showAuthedUI = initialized && (user !== null);
  // Tracks the previous authed state across renders so we can detect the
  // authed -> unauthed transition on logout.
  const prevAuthedRef = useRef(showAuthedUI);
  // --- Detect authed -> unauthed transitions ---
  // `logout()` sets `user` to null, which flips `showAuthedUI` to false and
  // causes App to render <Login> instead of the app. App is NOT unmounted during
  // this swap — it stays mounted with all its workspace state (messages, the
  // active conversation id, selected domain/category) intact. That is exactly
  // why the "new session" (+) button clears the screen (it calls
  // setMessages([]) etc.) but logout did not: logout never touched any of it.
  //
  // This effect detects the transition and clears the current workspace so the
  // next login starts on a blank screen.
  useEffect(() => {
    if (!initialized) return;
    // Always record the current authed state BEFORE any early return,
    // otherwise logging in never updates the ref and logout can't be detected.
    const currentAuthed = showAuthedUI;
    const prevAuthed = prevAuthedRef.current;
    prevAuthedRef.current = currentAuthed;
    if (showAuthedUI) return;

    const wasAuthed = prevAuthed;
    const justLoggedOut = wasAuthed; // we are past the return, so !showAuthedUI
    // (updated above)

    if (justLoggedOut) {
      setSelectedDomain(null);
      setSelectedSubCategory(null);
      setCurrentConversationId("");
      setMessages([]);
      setNeedLogin(false);
      resetStreaming();
    }
  }, [showAuthedUI, initialized]);

  // Load the current per-user context limit once we're authed.
  useEffect(() => {
    if (!showAuthedUI) return;
    getSettings(API_BASE)
      .then(setSettingsState)
      .catch(() => undefined);
  }, [showAuthedUI]);

  // Load the MCP connection status once we're authed (read-only mirror of the
  // /ctx effect below). /mcp can still refresh it on demand.
  useEffect(() => {
    if (!showAuthedUI) return;
    getMCPStatus(API_BASE)
      .then(setMCPState)
      .catch(() => undefined);
  }, [showAuthedUI]);

  // Load the stored MCP working-directory once we're authed, so /mcpdir [path]
  // and a subsequent /mcp know the current value. /mcpdir can refresh it on
  // demand. Mirrors the MCP status effect above.
  useEffect(() => {
    if (!showAuthedUI) return;
    getMCPWorkdir(API_BASE)
      .then((w) => setMCPDirState(w.value))
      .catch(() => undefined);
  }, [showAuthedUI]);

  // Load the stored RAG working-directory once we're authed, so /ragdir and
  // a later message know the current value. /ragdir can refresh it on demand.
  // Mirrors the MCP workdir effect above.
  useEffect(() => {
    if (!showAuthedUI) return;
    getRAGWorkdir(API_BASE)
      .then((w) => setRAGDirState(w.value))
      .catch(() => undefined);
  }, [showAuthedUI]);

  // Load the stored per-user custom system prompt once we're authed, so /sys
  // can report it and a later message knows whether a custom prompt is active.
  // Stored per-user via GET/POST /api/system. Omitting the /sys argument clears
  // the stored value so the backend falls back to the code default.
  useEffect(() => {
    if (!showAuthedUI) return;
    getSystemPrompt(API_BASE)
      .then((p) => setSystemPromptState(p.system_prompt))
      .catch(() => undefined);
  }, [showAuthedUI]);

  // --- Streaming hook ---
  const {
    state: streamingState,
    streamMessage,
    reset: resetStreaming,
    abort: abortStreaming,
  } = useStreaming(API_BASE);

  // Ref to hold latest streaming state (fixes closure bug)
  const streamingStateRef = useRef(streamingState);
  streamingStateRef.current = streamingState;

  // Ref to track when a new streaming session has started
  // This helps us know when to check for streaming completion
  const streamingStartedRef = useRef(false);

  // --- Refs ---
  const chatHistoryRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [inputValue, setInputValue] = useState("");

  // --- Fetch domains on mount ---
  useEffect(() => {
    if (!showAuthedUI) return;
    fetchWithToken(API_BASE, {
      url: "/api/domains",
      method: "GET",
    })
      .then((res) => res.json())
      .then((data: { domains: Domain[] }) => {
        setDomains(data.domains || []);
      })
      .catch((err) => {
        if (err instanceof AuthError) setNeedLogin(true);
      });
  }, [showAuthedUI]);

  // --- Fetch conversations on mount ---
  useEffect(() => {
    if (showAuthedUI) refreshConversations();
  }, [showAuthedUI]);

  // --- "Tail mode" (auto-stick to bottom) ---
  // While streaming we normally pin the scroll container to its bottom so the
  // latest streamed text is always visible. If the user manually scrolls up
  // (wheel / page-up / arrow up / drag the scrollbar) we *exit* tail mode: we
  // stop force-scrolling so they can read the freely. When they scroll back to
  // the bottom we *re-enter* tail mode and follow the stream again. Streaming
  // keeps flowing the whole time — only the scroll behavior changes.
  const stickyAtBottomRef = useRef(true);
  const isAtBottom = (el: HTMLElement) =>
    el.scrollHeight - el.clientHeight - el.scrollTop <= 5;

  // Keep the tail flag in sync with the user's actual scroll position.
  // IMPORTANT: the dependency is [showAuthedUI], not [].
  // The chat element does not exist on the app's first render (the login
  // screen is shown, so chatHistoryRef.current is null). With [] this effect
  // runs once, bails out because the element is null, and never re-runs — so the
  // scroll listener is attached to nothing and stickyAtBottomRef stays stuck at
  // its initial `true`. That makes the auto-scroll effect below force-scroll to
  // the bottom on every chunk, so the user can never scroll up mid-stream.
  // Depending on showAuthedUI lets the effect (re)run after the app becomes
  // visible, at which point the element is attached and the listener succeeds.
  useEffect(() => {
    const el = chatHistoryRef.current;
    if (!el) return;
    const onScroll = () => {
      stickyAtBottomRef.current = isAtBottom(el);
    };
    el.addEventListener("scroll", onScroll);
    return () => el.removeEventListener("scroll", onScroll);
  }, [showAuthedUI]);

  // --- Auto-scroll chat to bottom when messages change or streaming updates ---
  // Only force-scroll when we are in tail mode. Otherwise leave the user's
  // scroll position untouched so they can page up/down to read.
  useEffect(() => {
    const el = chatHistoryRef.current;
    if (!el) return;
    if (stickyAtBottomRef.current) {
      el.scrollTop = el.scrollHeight;
    } else {
      // Content may have grown to reach the bottom; re-check tail state.
      stickyAtBottomRef.current = isAtBottom(el);
    }
  }, [messages, streamingState.currentChunk]);

  // --- Set focus on input when streaming finishes ---
  useEffect(() => {
    if (!streamingState.isStreaming && inputRef.current && inputValue) {
      inputRef.current.focus();
    }
  }, [streamingState.isStreaming, inputValue]);

  // --- Listen for streaming completion ---
  useEffect(() => {
    // Only process completion if we were streaming and now stopped
    if (streamingStartedRef.current && !streamingState.isStreaming) {
      const latest = streamingStateRef.current;

      // Capture conversation ID
      if (latest.conversationId) {
        setCurrentConversationId(latest.conversationId);
      }

      if (latest.fullAnswer) {
        // Streaming completed with content — add to messages
        const assistantMessage: Message = {
          role: "assistant",
          content: latest.fullAnswer,
          sources: latest.sources,
          confluence_links: latest.citations,
        };
        setMessages((prev) => [...prev, assistantMessage]);
        refreshConversations();
      } else if (latest.error) {
        // Streaming completed with error — add error message
        setMessages((prev) => [
          ...prev,
          {
            role: "assistant",
            content: latest.error || "Unknown error",
            error: "streaming_error",
          },
        ]);
      }

      streamingStartedRef.current = false;
    }
  }, [streamingState.isStreaming]);

  // --- Handlers ---
  const handleNewConversation = async () => {
    // Create a new conversation in the backend
    try {
      const res = await fetchWithToken(API_BASE, {
        url: "/api/conversations",
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      if (res.ok) {
        const data = await res.json();
        setCurrentConversationId(data.id);
      }
    } catch {
      // ignore
    }
    setSelectedDomain(null);
    setSelectedSubCategory(null);
    setMessages([]);
    resetStreaming();
    streamingStartedRef.current = false;
    refreshConversations();
  };

  const handleSelectDomain = (domain: Domain) => {
    setSelectedDomain(domain);
    setSelectedSubCategory(null);
    setMessages((prev) => [
      ...prev,
      {
        role: "assistant",
        synthetic: true,
        content: `Great! I'm ready to help with ${domain.display_name}. What would you like to know?`,
      },
    ]);
  };

  const handleSelectSubCategory = (domain: Domain, key: string) => {
    setSelectedDomain(domain);
    setSelectedSubCategory(key);
    const subCat = domain.sub_categories?.[key];
    setMessages((prev) => [
      ...prev,
      {
        role: "assistant",
        synthetic: true,
        content: `Perfect! Let me help you with ${subCat?.display_name || key}. What would you like to know?`,
      },
    ]);
  };

  const handleChangeSelection = () => {
    setSelectedDomain(null);
    setSelectedSubCategory(null);
  };

  const handleConversationSelect = async (id: string) => {
    try {
      const res = await fetchWithToken(API_BASE, {
        url: `/api/conversations/${id}`,
        method: "GET",
      });
      if (!res.ok) return;
      const data = await res.json();
      const msgs = (data.messages || [])
        .filter(
          (m: any) => m.role === "user" || m.role === "assistant"
        )
        .map((m: any) => ({
          role: m.role,
          content: m.content,
          sources: m.sources || [],
          confluence_links: m.confluence_links || [],
        }));
      setMessages(msgs);
      setCurrentConversationId(id);
    } catch {
      // ignore
    }
  };

  const handleConversationDelete = async (id: string) => {
    // A single delete can still 404 here even when the item was already
    // removed: the multi-delete path (Sidebar.handleMultiDelete) bulk-deletes
    // the selected ids and then calls onDeleteConversation(id) for each, so
    // the follow-up DELETE to /api/conversations/{id} returns 404. That 404 is
    // the desired end-state (gone on the server), so ignore it and always run
    // refreshConversations() below so the list drops the deleted entries.
    try {
      await deleteJSON(API_BASE, `/api/conversations/${id}`);
      if (id === currentConversationId) {
        setCurrentConversationId("");
        setMessages([]);
      }
    } catch {
    }
    refreshConversations();
  };

  const handleClearAllConversations = async () => {
    try {
      await deleteJSON(API_BASE, "/api/conversations");
      setCurrentConversationId("");
      setMessages([]);
      refreshConversations();
    } catch {}
  };

  // Clears the active conversation (used by the /clear command and Settings
  // panel): drops the current conversation, wipes messages and input, and
  // starts on a blank screen.
  const onClearConversation = () => {
    // Clear only the on-screen messages + input. Intentionally does NOT change
    // the conversation ID: the backend keeps serving the same conversation's
    // (and hence the same AI) context. A genuinely new conversation is created
    // via the + button, not via /clear.
    setMessages([]);
    setInputValue("");
  };

  const refreshConversations = async () => {
    try {
      const res = await fetchWithToken(API_BASE, {
        url: "/api/conversations",
        method: "GET",
      });
      if (res.ok) {
        const data: Conversation[] = await res.json();
        setConversations(data || []);
      }
    } catch {}
  };

  const handleSend = async (text: string) => {
    if (!text.trim()) return;

    // Optimistic UI: show user message immediately
    setMessages((prev) => [
      ...prev,
      {
        role: "user",
        content: text,
      },
    ]);
    setInputValue("");

    // Build history for cross-turn context (exclude synthetic and the optimistic user turn)
    const history = messages
      .filter((m) => !m.synthetic && m.role === "assistant")
      .map((m) => ({ role: m.role, content: m.content }));

    // Determine sub-category for the request
    let subCategoryValue: string | undefined;
    if (selectedDomain?.key && selectedSubCategory) {
      const subCat = selectedDomain.sub_categories?.[selectedSubCategory];
      subCategoryValue = subCat?.display_name || selectedSubCategory;
    }

    // Build payload
    const payload: Parameters<typeof streamMessage>[0] = {
      message: text,
      conversation_id: currentConversationId,
      history,
    };
    if (selectedDomain?.key) {
      payload.domain = selectedDomain.key;
    }
    if (subCategoryValue) {
      payload.sub_category = subCategoryValue;
    }

    // Mark that we started streaming (so the useEffect can detect completion)
    streamingStartedRef.current = true;

    // Start streaming — don't use await for the completion logic,
    // let the useEffect handle it when isStreaming changes
    streamMessage(payload).catch(() => {
      // Don't propagate errors here — the useEffect will catch them
    });
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const text = inputValue.trim();
    if (!text || streamingState.isStreaming) return;

    // Intercept slash-commands before they ever reach the model. The backend
    // treats "/ctx ..." as a real message unless the frontend consumes it here,
    // which would leak the raw command to the LLM.
    if (text.startsWith("/")) {
      handleSlashCommand(text);
      setInputValue("");
      return;
    }

    handleSend(text);
  };

  // Handles a slash-command typed in the input. Supported:
  //   /ctx [number]  set (with an argument) or report the per-user context limit
  //   /clear         start a fresh conversation (clears messages + input)
  //   /sys [text]    set (with an argument) or report the per-user custom system prompt
  // Any other /command is ignored (left as a no-op, like the SPA's behaviour).
  const handleSlashCommand = (command: string) => {
    const parts = command.split(/\s+/).filter(Boolean);
    const name = parts[0];
    const arg = parts.slice(1).join(" ");

    switch (name) {
      case "/clear":
        onClearConversation();
        break;
      case "/ctx":
        if (arg === "") {
          appendFeedback(
            "ok",
            `Context limit: ${fmt(settings?.context_limit ?? 50000)} tokens`
          );
        } else {
          void applyContextLimit(arg);
        }
        break;
      case "/mcp":
        // arg is the spec (http(s) URL or stdio command) or "" for status-only.
        void handleMCP(arg);
        break;
      case "/mcpdir":
        // arg is the MCP working-directory to use for the default stdio MCP
        // server; omit the arg to report the current value.
        void handleMCPDir(arg);
        break;
      case "/ragdir":
        // arg is the raw RAG directory value; omit the arg to report the
        // current value. Stored per-user via POST /api/ragdir.
        void handleRAGDir(arg);
        break;
      case "/sys":
        // arg is the custom system prompt text; omit the arg to report the
        // current value. Stored per-user via POST /api/system.
        void handleSystemPrompt(arg);
        break;
      case "/help":
        appendFeedback("ok", COMMAND_HELP_TEXT);
        break;
      default:
        appendFeedback(
          "error",
          `Unknown command: /${name}. Type /help for a list of commands.`
        );
        break;
    }
  };

  // Handles the /mcp slash-command. Like the reference CLI, it reports the live
  // connection (whether an MCP server is attached, its launch spec, the tools the
  // model may call). It also supports connect/disconnect via a spec prompt:
  //
  //   /mcp            Show status.
  //   /mcp off        Disconnect the current server.
  //   /mcp <spec>     Connect. <spec> is an http(s):// URL (Streamable HTTP) or a
  //                   whitespace-separated stdio command (e.g. `node ./server.js`).
  //
  // This uses POST /api/mcp, the runtime connect/disconnect endpoint the backend
  // adds beside the read-only GET /api/mcp status endpoint.
  const handleMCP = async (spec?: string) => {
    // Fast path: an explicit spec means "connect"; "off" means "disconnect".
    const trimmed = (spec ?? "").trim();
    if (trimmed === "off") {
      let status: Awaited<ReturnType<typeof disconnectMCP>>;
      try {
        status = await disconnectMCP(API_BASE);
      } catch {
        appendFeedback(
          "error",
          "Failed to disconnect MCP server. (It may already be disconnected.)"
        );
        return;
      }
      setMCPState(status);
      appendFeedback("ok", "MCP server disconnected.");
      return;
    }
    if (trimmed) {
      let status: Awaited<ReturnType<typeof connectMCP>>;
      try {
        status = await connectMCP(API_BASE, trimmed);
      } catch (e) {
        appendFeedback(
          "error",
          "Failed to connect MCP: " + (e instanceof Error ? e.message : String(e))
        );
        return;
      }
      setMCPState(status);
      if (!status.connected) {
        appendFeedback("error", "MCP connection failed.");
        return;
      }
      const lines = [
        `MCP connected: ${status.spec ?? "(unknown spec)"}`,
        `${status.tools?.length ?? 0} tool(s) available`,
      ];
      for (const tool of status.tools ?? []) {
        lines.push(`  • ${tool.name}` + (tool.description ? ` — ${tool.description}` : ""));
      }
      appendFeedback("ok", lines.join("\n"));
      return;
    }

    // No spec: show the read-only status.
    let status: Awaited<ReturnType<typeof getMCPStatus>>;
    try {
      status = await getMCPStatus(API_BASE);
    } catch {
      appendFeedback("error", "Failed to read MCP status.");
      return;
    }
    setMCPState(status);

    if (!status.connected) {
      appendFeedback("ok", "No MCP server connected. Try /mcp <spec>, e.g. /mcp http://localhost:8080/mcp.");
      return;
    }

    const lines = [
      `MCP connected: ${status.spec ?? "(unknown spec)"}`,
      `${status.tools?.length ?? 0} tool(s) available`,
    ];
    for (const tool of status.tools ?? []) {
      lines.push(`  • ${tool.name}` + (tool.description ? ` — ${tool.description}` : ""));
    }
    appendFeedback("ok", lines.join("\n"));
  };

  // Handles the /mcpdir slash-command. The /mcpdir command sets the working
  // directory the default (stdio) MCP server launches in, mirroring the
  // reference CLI's MCP_WORKDIR knob. It's stored per-user via POST /api/mcpdir
  // and, on the backend, is applied automatically by POST /api/mcp when the
  // connect request omits an explicit workdir — so a later `/mcp mcp.exe` runs
  // in this directory. Omit the argument to report the current value.
  //
  //   /mcpdir [path]   set the MCP working-directory (empty resets it).
  const handleMCPDir = async (value?: string) => {
    const v = (value ?? "").trim();
    try {
      const updated = v === ""
        ? await setMCPWorkdir(API_BASE, "")
        : await setMCPWorkdir(API_BASE, v);
      setMCPDirState(updated.value);
      if (updated.value === "") {
        appendFeedback("ok", "MCP working directory cleared — default stdio MCP will run in the backend cwd.");
      } else {
        appendFeedback("ok", `MCP working directory set to: ${updated.value}`);
      }
    } catch (e) {
      appendFeedback(
        "error",
        "Failed to set MCP working directory: " + (e instanceof Error ? e.message : String(e))
      );
    }
  };

  // Handles the /ragdir slash-command. Like the reference CLI's RAG_DIR knob,
  // it points the RAG pipeline at a directory of documents to embed. It's
  // stored per-user via POST /api/ragdir and used automatically by the backend
  // when generating a store. Omit the argument to report the current value.
  //
  //   /ragdir [path]   set the RAG working-directory (empty resets it).
  const handleRAGDir = async (value?: string) => {
    const v = (value ?? "").trim();
    try {
      const updated = v === ""
        ? await setRAGWorkdir(API_BASE, "")
        : await setRAGWorkdir(API_BASE, v);
      setRAGDirState(updated.value);
      if (updated.value === "") {
        appendFeedback("ok", "RAG working directory cleared.");
      } else {
        appendFeedback("ok", `RAG working directory set to: ${updated.value}`);
      }
    } catch (e) {
      appendFeedback(
        "error",
        "Failed to set RAG working directory: " + (e instanceof Error ? e.message : String(e))
      );
    }
  };
  // Handles the /sys slash-command. The backend stores a per-user custom system
  // prompt via POST /api/system and uses it as the first message for every chat.
  // Three modes, distinguished by the argument:
  //
  //   /sys              report the current (effective) system prompt
  //   /sys default      reset to the code default (clear the stored value)
  //   /sys [text]       set the custom system prompt (empty resets it)
  const handleSystemPrompt = async (value?: string) => {
    const v = (value ?? "").trim();

    // Bare /sys with no argument: just report the current effective prompt.
    if (v === "") {
      try {
        const current = await getSystemPrompt(API_BASE);
        setSystemPromptState(current.system_prompt);
        appendFeedback(
          "ok",
          current.system_prompt === ""
            ? "No custom system prompt set (using the default)."
            : `Current system prompt:\n${current.system_prompt}`
        );
        return;
      } catch (e) {
        appendFeedback(
          "error",
          "Failed to read system prompt: " + (e instanceof Error ? e.message : String(e))
        );
        return;
      }
    }

    // Explicit "default" (case-insensitive) resets to the code default; any
    // other non-empty value is stored verbatim.
    const isReset = v === "default";
    try {
      const updated = await setSystemPrompt(API_BASE, v);
      setSystemPromptState(updated.system_prompt);
      if (isReset || updated.system_prompt === "") {
        appendFeedback("ok", "Custom system prompt reset to the default.");
      } else {
        appendFeedback("ok", `System prompt set to:\n${updated.system_prompt}`);
      }
    } catch (e) {
      appendFeedback(
        "error",
        "Failed to set system prompt: " + (e instanceof Error ? e.message : String(e))
      );
    }
  };
  // Formats a token budget as a human-readable string.
  const fmt = (n: number): string =>
    Number.isFinite(n) ? n.toLocaleString() : "0";

  // The text rendered by the /help command — a short catalog of every
  // supported slash command with a note on its arguments. Kept as a constant so
  // both /help and its tests can share the exact wording.
  const COMMAND_HELP_TEXT =
    "Available commands:\n" +
    "  /clear    Clear the conversation.\n" +
    "  /ctx [N]  Show or set the context limit (tokens). If N is omitted, " +
    "prints the current limit.\n" +
    "  /mcp      Show the MCP connection status and list the tools the model " +
    "may call.\n" +
    "  /mcpdir [path]  Set the working-directory the default stdio MCP server " +
    "runs in. Later /mcp <spec> uses it. Omit path to show the current value.\n" +
    "  /sys [text]  Set or report the per-user custom system prompt. If text is omitted, " +
    "prints the current prompt.\n" +
    "  /help     Show this help message.";

  const [settings, setSettingsState] = useState<{ context_limit: number } | null>(
    null
  );

  // Tracks the live MCP connection status so the /mcp command can report it.
  // Loaded once from GET /api/mcp (read-only). The value is only written back
  // by /mcp when the user queries again; it renders its result directly via
  // appendFeedback (no separate read needed).
  const [, setMCPState] = useState<{ connected: boolean; spec?: string; tools: Array<{ name: string; description: string }> } | null>(
    null
  );

  // Tracks the stored MCP working-directory so /mcpdir and a later /mcp know
  // the current value. Loaded once from GET /api/mcpdir; refreshed by /mcpdir.
  const [, setMCPDirState] = useState<string | null>(
    null
  );

  // Tracks the stored RAG working-directory so /ragdir and a later message
  // know the current value. Loaded once from GET /api/ragdir; refreshed by
  // /ragdir.
  const [, setRAGDirState] = useState<string | null>(
    null
  );
  // Tracks the stored per-user custom system prompt so /sys can report it.
  // Loaded once from GET /api/system; refreshed by /sys.
  const [, setSystemPromptState] = useState<string | null>(
    null
  );



  const applyContextLimit = async (value: string) => {
    if (!/^\d+$/.test(value)) {
      appendFeedback("error", "Please enter a whole number greater than 0.");
      return;
    }
    const n = Number(value);
    if (n < 1) {
      appendFeedback("error", "Please enter a whole number greater than 0.");
      return;
    }
    try {
      const updated = await setSetting(API_BASE, { key: "ctxLimit", value });
      setSettingsState(updated);
      appendFeedback("ok", `Context limit set to ${updated.context_limit} tokens.`);
    } catch {
      appendFeedback("error", "Failed to set context limit. Please try again.");
    }
  };

  // The /ctx (and /clear) command feedback is shown as a retained message in
  // the main chat window rather than a toast that auto-dismisses after 4s. This
  // lets the user scroll up, copy the value, and see the result persist across
  // the session. The message is appended as an assistant message tagged with
  // `error: "command_result"` so MessageBubble renders it in a distinct style.
  const appendFeedback = (
    type: "ok" | "error",
    text: string
  ) => {
    setMessages((prev) => [
      ...prev,
      {
        role: "assistant",
        content: text,
        error: `command_result:${type}`,
      },
    ]);
  };

  const handleStop = () => {
    // Abort the in-flight SSE request
    abortStreaming();
    streamingStartedRef.current = false;
    setMessages((prev) => [
      ...prev,
      {
        role: "assistant",
        content: "⏹ Generation stopped.",
        error: "stopped",
      },
    ]);
  };

  // Determine placeholder and scope label
  let placeholder = "Ask me anything...";
  let scopeLabel = "";
  if (selectedDomain) {
    const domainName = selectedDomain.display_name;
    if (selectedSubCategory) {
      const subCat = selectedDomain.sub_categories?.[selectedSubCategory];
      placeholder = `Ask about ${domainName} > ${subCat?.display_name || selectedSubCategory}...`;
      scopeLabel = `${selectedDomain.icon} ${domainName} > ${subCat?.display_name || selectedSubCategory}`;
    } else {
      placeholder = `Ask about ${domainName}...`;
    }
  }

  // Not ready to render app content (session still being resolved from storage)
  if (!initialized) {
    return (
      <div className="auth-loading">Loading…</div>
    );
  }

  // Not authenticated (or the session was rejected) — show the login screen.
  if (!showAuthedUI || needLogin) {
    return <Login login={login} />;
  }

  return (
    <div className="app-container">
      {/* Mobile menu toggle — only visible on small screens (see index.css) */}
      <button
        className="mobile-menu-btn"
        onClick={toggleSidebar}
        aria-label="Toggle menu"
        type="button"
      >
        <svg viewBox="0 0 24 24" fill="currentColor">
          <path d="M3 6h18v2H3zM3 11h18v2H3zM3 16h18v2H3z" />
        </svg>
      </button>

      {/* Backdrop shown behind the open sidebar on mobile */}
      {sidebarOpen && (
        <div className="sidebar-backdrop" onClick={closeSidebar} />
      )}

      {/* Sidebar */}
      <Sidebar
        apiBaseUrl={API_BASE}
        selectedDomain={selectedDomain}
        selectedSubCategory={selectedSubCategory}
        conversations={conversations}
        activeConversationId={currentConversationId}
        sidebarOpen={sidebarOpen}
        onCloseSidebar={closeSidebar}
        onNewConversation={handleNewConversation}
        onChangeSelection={handleChangeSelection}
        onSelectConversation={handleConversationSelect}
        onDeleteConversation={handleConversationDelete}
        onClearAllConversations={handleClearAllConversations}
        onQuickAction={handleSelectDomain}
        domains={domains}
        user={user}
        onLogout={() => void logout()}
        onClearConversation={onClearConversation}
      />

      {/* Main content */}
      <main className="main-content">
        {/* Domain pills */}
        {!selectedDomain && domains.length > 0 && (
          <DomainPills
            domains={domains}
            onSelection={handleSelectDomain}
          />
        )}

        {/* Sub-category pills */}
        {selectedDomain &&
          Object.keys(selectedDomain.sub_categories || {}).length > 0 &&
          !selectedSubCategory && (
            <SubCategoryPills
              domain={selectedDomain}
              subCategories={selectedDomain.sub_categories || {}}
              onBack={handleChangeSelection}
              onSelect={handleSelectSubCategory}
            />
          )}

        {/* Chat area */}
        <ChatArea
          chatHistoryRef={chatHistoryRef}
          messages={messages}
          streamingState={streamingState}
          scopeLabel={scopeLabel}
          inputRef={inputRef}
          inputValue={inputValue}
          setInputValue={setInputValue}
          placeholder={placeholder}
          isLoading={streamingState.isStreaming}
          onSend={handleSubmit}
          onStop={handleStop}
        />
      </main>
    </div>
  );
}
