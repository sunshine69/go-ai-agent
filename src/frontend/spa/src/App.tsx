import React, { useState, useEffect, useRef } from "react";
import { Domain } from "./types";
import { useStreaming, ChatMessage } from "./hooks/useStreaming";
import { useAuth } from "./hooks/useAuth";
import { Sidebar } from "./components/Sidebar";
import { DomainPills } from "./components/DomainPill";
import { SubCategoryPills } from "./components/SubCategoryPill";
import { ChatArea } from "./components/ChatArea";
import { Login } from "./components/Login";
import { AuthError, fetchWithToken, deleteJSON } from "./utils/api";

// Base URL for the AI backend — read from .env file
const API_BASE =
  (import.meta.env.VITE_BACKEND_URL as string) || "";

interface Conversation {
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
  const inputRef = useRef<HTMLInputElement>(null);
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

  // --- Auto-scroll chat to bottom when messages change or streaming updates ---
  useEffect(() => {
    if (chatHistoryRef.current) {
      chatHistoryRef.current.scrollTop = chatHistoryRef.current.scrollHeight;
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
    if (inputValue.trim() && !streamingState.isStreaming) {
      handleSend(inputValue.trim());
    }
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
      {/* Sidebar */}
      <Sidebar
        apiBaseUrl={API_BASE}
        selectedDomain={selectedDomain}
        selectedSubCategory={selectedSubCategory}
        conversations={conversations}
        activeConversationId={currentConversationId}
        onNewConversation={handleNewConversation}
        onChangeSelection={handleChangeSelection}
        onSelectConversation={handleConversationSelect}
        onDeleteConversation={handleConversationDelete}
        onClearAllConversations={handleClearAllConversations}
        onQuickAction={handleSelectDomain}
        domains={domains}
        user={user}
        onLogout={() => void logout()}
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
