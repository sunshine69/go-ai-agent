import React, { useState, useEffect, useRef } from "react";
import { Domain } from "./types";
import { useStreaming, ChatMessage } from "./hooks/useStreaming";
import { useAuth } from "./hooks/useAuth";
import { Sidebar } from "./components/Sidebar";
import { DomainPills } from "./components/DomainPill";
import { SubCategoryPills } from "./components/SubCategoryPill";
import { ChatArea } from "./components/ChatArea";
import { Login } from "./components/Login";
import { AuthError, fetchWithToken, deleteJSON, setSetting, getSettings } from "./utils/api";

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
    try {
      await deleteJSON(API_BASE, `/api/conversations/${id}`);
      if (id === currentConversationId) {
        setCurrentConversationId("");
        setMessages([]);
      }
      refreshConversations();
    } catch {}
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
    "  /help     Show this help message.";

  const [settings, setSettingsState] = useState<{ context_limit: number } | null>(
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
