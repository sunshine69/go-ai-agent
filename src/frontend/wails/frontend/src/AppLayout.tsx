import React, { useState, useEffect, useRef } from "react";
import "./style.css";
import { Markdown } from "./components/Markdown";
import { useApi } from "./hooks/useApi";
import { getSettings, setSetting, getMcpdir, setMcpdir, McpdirResponse } from "./utils/settings";

// Same-origin base for /api/* calls (empty string serves in production
// through Wails; set VITE_BACKEND_URL only for standalone dev via env).
const API_BASE = (import.meta.env.VITE_BACKEND_URL as string) || "";

// Types matching backend API response shape
interface SubCategory {
  display_name: string;
  icon?: string;
}

interface Domain {
  key: string;
  display_name: string;
  icon: string;
  sub_categories?: Record<string, SubCategory>;
}

interface Message {
  role: 'user' | 'assistant';
  content: string;
  confluence_links?: Array<{ title: string; url: string }>;
  rag_sources?: string[];
  error?: string;
  // synthetic: true for the placeholder intro message shown on domain
  // selection. These are display-only and must NOT be sent to the LLM as
  // conversation history.
  synthetic?: boolean;
}

// Main App component - complete layout matching Streamlit UI
export default function App() {
  // Wire to the SPA-parity API client. This replaces the old blocking
  // /messages fetch with SSE streaming at /api/messages/stream, exactly like
  // the SPA (see src/frontend/spa/src/App.tsx).
  const {
    domains,
    conversations,
    activeId,
    setActiveId,
    streaming,
    abort,
    streamMessage,
    reset,
    refreshConversations,
    createConversation,
    loadConversation,
    deleteConversation,
    clearConversations,
    loadDomains,
  } = useApi();

  const [selectedDomain, setSelectedDomain] = useState<Domain | null>(null);
  const [subCategoryKey, setSubCategoryKey] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);

  // Ref to hold latest streaming state (fixes closure bug)
  const streamingStateRef = useRef(streaming);
  streamingStateRef.current = streaming;

  // Ref to track when a new streaming session has started.
  // This lets the completion effect know if a real session ended.
  const streamingStartedRef = useRef(false);

  // Auto-scroll chat to the bottom whenever messages or streaming chunk change.
  const chatHistoryRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [inputValue, setInputValue] = useState('');

  // --- Fetch domains on mount (mirrors SPA mount effect) ---
  useEffect(() => {
    loadDomains();
  }, []);

  // --- Fetch settings on mount (mirrors SPA mount effect) ---
  useEffect(() => {
    getSettings(API_BASE)
      .then(setSettingsState)
      .catch(() => undefined);
  }, []);

  // --- Fetch MCP working dir on mount (mirrors SPA mount effect) ---
  useEffect(() => {
    getMcpdir(API_BASE)
      .then(setMcpdirState)
      .catch(() => undefined);
  }, []);
  // --- Fetch conversations on mount (mirrors SPA mount effect) ---
  useEffect(() => {
    refreshConversations();
  }, []);

  useEffect(() => {
    if (chatHistoryRef.current) {
      chatHistoryRef.current.scrollTop = chatHistoryRef.current.scrollHeight;
    }
  }, [messages, streamingStateRef.current.currentChunk]);

  // Focus the input when streaming finishes (mirrors SPA).
  useEffect(() => {
    if (!streamingStateRef.current.isStreaming && inputRef.current && inputValue) {
      inputRef.current.focus();
    }
  }, [streamingStateRef.current.isStreaming, inputValue]);

  // Listen for streaming completion: append the accumulated answer (or error)
  // to the message list so the selected conversation is mirrored.
  useEffect(() => {
    if (!streamingStartedRef.current) return;
    if (streaming.isStreaming) return;

    streamingStartedRef.current = false;
    const latest = streamingStateRef.current;

    if (latest.conversationId) setActiveId(latest.conversationId);

    if (latest.fullAnswer) {
      const assistantMessage: Message = {
        role: 'assistant',
        content: latest.fullAnswer,
        confluence_links: latest.citations || [],
        rag_sources: latest.sources || [],
      };
      setMessages((prev) => [...prev, assistantMessage]);
      refreshConversations();
    } else if (latest.error) {
      setMessages((prev) => [
        ...prev,
        { role: 'assistant', content: latest.error ?? '', error: 'streaming_error' },
      ]);
    }
  }, [streaming.isStreaming]);

  // --- Handlers --------------------------------------------------------

  const handleNewConversation = async () => {
    await createConversation();
    setSelectedDomain(null);
    setSubCategoryKey(null);
    setActiveId('');
    setMessages([]);
    reset();
    streamingStartedRef.current = false;
    refreshConversations();
  };

  const handleDomainSelection = (domain: Domain) => {
    setSelectedDomain(domain);
    setSubCategoryKey(null);
    setMessages((prev) => [
      ...prev,
      {
        role: 'assistant',
        synthetic: true,
        content: `Great! I'm ready to help with ${domain.display_name}. What would you like to know?`,
      },
    ]);
  };

  const handleSubCategorySelection = (domain: Domain, key: string) => {
    setSelectedDomain(domain);
    setSubCategoryKey(key);
    const subCat = domain.sub_categories?.[key];
    setMessages((prev) => [
      ...prev,
      {
        role: 'assistant',
        synthetic: true,
        content: `Perfect! Let me help you with ${subCat?.display_name || key}. What would you like to know?`,
      },
    ]);
  };

  const handleChangeSelection = () => {
    setSelectedDomain(null);
    setSubCategoryKey(null);
  };

  const handleQuickAction = (domain: Domain) => {
    setSelectedDomain(domain);
    setSubCategoryKey(null);
    setMessages([]);
  };

  const handleConversationSelect = async (id: string) => {
    setActiveId(id);
    // Load stored messages so the selected conversation actually renders
    // (mirrors the SPA's handleConversationSelect).
    const data = await loadConversation(id);
    const msgs = (Array.isArray(data) ? data : [])
      .filter((m: any) => m.role === 'user' || m.role === 'assistant')
      .map((m: any) => ({
        role: m.role,
        content: m.content,
      }));
    setMessages(msgs);
  };

  const handleConversationDelete = async (id: string) => {
    await deleteConversation(id);
    if (id === activeId) {
      setMessages([]);
    }
    refreshConversations();
  };

  const handleClearAllConversations = async () => {
    await clearConversations();
    setMessages([]);
    setActiveId('');
  };

  const handleStop = () => {
    abort();
    streamingStartedRef.current = false;
    setMessages((prev) => [
      ...prev,
      { role: 'assistant', content: '⏹ Generation stopped.', error: 'stopped' },
    ]);
  };


  const handleSend = async (text: string) => {
    if (!text.trim()) return;

    // Optimistic UI: show the user's message immediately.
    setMessages((prev) => [...prev, { role: 'user', content: text }]);
    setInputValue('');

    // Build history for cross-turn context (exclude synthetic + the optimistic user turn)
    const history = messages
      .filter((m) => !m.synthetic && m.role === 'assistant')
      .map((m) => ({ role: m.role, content: m.content }));

    // Determine sub-category for the request
    let subCategoryValue: string | undefined;
    if (selectedDomain?.key && subCategoryKey) {
      const subCat = selectedDomain.sub_categories?.[subCategoryKey];
      subCategoryValue = subCat?.display_name || subCategoryKey;
    }

    // Build the request payload. conversation_id "" => new conversation;
    // a real id => append to that conversation (cross-turn memory).
    const payload: Parameters<typeof streamMessage>[0] = {
      message: text,
      conversation_id: activeId,
      history,
    };
    if (selectedDomain?.key) payload.domain = selectedDomain.key;
    if (subCategoryValue) payload.sub_category = subCategoryValue;

    // Mark that we started streaming so the completion effect can detect it.
    streamingStartedRef.current = true;

    // Start streaming — do not await completion; the useEffect handles it.
    streamMessage(payload).catch(() => {});
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (inputValue.trim() && !streaming.isStreaming) {
      const text = inputValue.trim();

      // Intercept slash-commands before they ever reach the model. The backend
      // treats "/ctx ..." as a real message unless the frontend consumes it
      // here, which would leak the raw command to the LLM.
      if (text.startsWith("/")) {
        handleSlashCommand(text);
        setInputValue("");
        return;
      }

      handleSend(text);
    }
  };

  // Handles a slash-command typed in the input. Supported:
  //   /ctx [number]  set (with an argument) or report the per-user context limit
  //   /clear         start a fresh conversation (clears messages + input)
  //   /help          list the available commands
  // Any other /command is ignored (left as a no-op).
  const handleSlashCommand = (command: string) => {
    const parts = command.split(/\s+/).filter(Boolean);
    const name = parts[0];
    const arg = parts.slice(1).join(" ");

    switch (name) {
      case "/clear":
        handleClearAllConversations();
        break;
      case "/ctx":
        if (arg === "") {
          flashSettings(
            "ok",
            `Context limit: ${fmt(settings?.context_limit ?? 50000)} tokens`
          );
        } else {
          void applyContextLimit(arg);
        }
        break;
      case "/mcpdir":
        if (arg === "") {
          flashSettings(
            "ok",
            `MCP working dir: ${mcpdir?.value || "(unset; uses server default)"}`
          );
        } else {
          void applyMcpdir(arg);
        }
        break;
      case "/help":
        flashSettings("ok", "Available commands: /ctx [number], /mcpdir <path>, /clear, /help");
        break;
      default:
        // Unknown command — ignore silently.
        break;
    }
  };

  // Formats a token budget as a human-readable string.
  const fmt = (n: number): string =>
    Number.isFinite(n) ? n.toLocaleString() : "0";

  const [mcpdir, setMcpdirState] = useState<McpdirResponse | null>(null);

  const [settings, setSettingsState] = useState<{ context_limit: number } | null>(
    null
  );

  const [settingsMessage, setSettingsMessage] = useState<{
    type: "ok" | "error";
    text: string;
  } | null>(null);

  const applyContextLimit = async (value: string) => {
    if (!/^\d+$/.test(value)) {
      flashSettings("error", "Please enter a whole number greater than 0.");
      return;
    }
    const n = Number(value);
    if (n < 1) {
      flashSettings("error", "Please enter a whole number greater than 0.");
      return;
    }
    try {
      const updated = await setSetting(API_BASE, { key: "ctxLimit", value });
      setSettingsState(updated);
      flashSettings("ok", `Context limit set to ${updated.context_limit} tokens.`);
    } catch {
      flashSettings("error", "Failed to set context limit. Please try again.");
    }
  };

  // Applies the per-user MCP working-directory selector stored via /mcpdir
  // (mirrors applyContextLimit, backed by /api/mcpdir).
  const applyMcpdir = async (value: string) => {
    const trimmed = value.trim();
    if (trimmed === "") {
      flashSettings("error", "Please enter a non-empty directory path.");
      return;
    }
    // Reject any parent-directory traversal component ("..").
    const segs = trimmed.split(/[\\/]+/);
    if (segs.includes("..")) {
      flashSettings("error", "MCP working dir must not contain '..'.");
      return;
    }
    try {
      const updated = await setMcpdir(API_BASE, { value: trimmed });
      setMcpdirState(updated);
      flashSettings(
        "ok",
        updated.value
          ? `MCP working dir set to ${updated.value}.`
          : "MCP working dir cleared (using server default)."
      );
    } catch (e) {
      flashSettings("error", `Failed to set MCP working dir: ${e?.message || e}`);
    }
  };

  const flashSettings = (
    type: "ok" | "error",
    text: string
  ) => {
    setSettingsMessage({ type, text });
    // Auto-dismiss after 4s.
    window.setTimeout(() =>
      setSettingsMessage((prev) => (prev?.text === text ? null : prev)),
      4000
    );
  };

  // Determine placeholder and scope label (mirrors the SPA logic).
  let placeholder = 'Ask me anything...';
  let scopeLabel = '';
  if (selectedDomain) {
    const domainName = selectedDomain.display_name;
    if (subCategoryKey) {
      const subCat = selectedDomain.sub_categories?.[subCategoryKey];
      placeholder = `Ask about ${domainName} > ${subCat?.display_name || subCategoryKey}...`;
      scopeLabel = `${selectedDomain.icon} ${domainName} > ${subCat?.display_name || subCategoryKey}`;
    } else {
      placeholder = `Ask about ${domainName}...`;
    }
  }

  // Determine sub-category info for pill selection fallback.
  const selectedDomainInfo = domains.find((d) => d.key === selectedDomain?.key);

  return (
    <div className="app-container">
      {/* Sidebar - always visible */}
      <Sidebar
        selectedDomain={selectedDomain}
        subCategoryKey={subCategoryKey}
        conversations={{
          domains,
          list: conversations.map((c) => ({ id: c.id, title: c.title })),
        }}
        onNewConversation={handleNewConversation}
        onChangeSelection={handleChangeSelection}
        onSelectQuickAction={handleQuickAction}
        onConversationSelect={handleConversationSelect}
        onConversationDelete={handleConversationDelete}
        onClearAllConversations={handleClearAllConversations}
        activeConversationId={activeId}
      />

      {/* Main content */}
      <main className="main-content">
        {!selectedDomain && domains.length > 0 ? (
          // Domain pills when no domain selected
          <DomainPills domains={domains} onSelection={handleDomainSelection} />
        ) : (selectedDomainInfo?.sub_categories &&
            Object.keys(selectedDomainInfo.sub_categories).length > 0 &&
            !subCategoryKey) ? (
          // Sub-category pills after domain selection
          <SubCategoryPills
            domainName={selectedDomain?.display_name ?? ''}
            subCategories={selectedDomainInfo.sub_categories || {}}
            onBack={handleChangeSelection}
            onSelect={handleSubCategorySelection}
          />
        ) : null}

        {/* Chat area: message history + streaming + input */}
        <div className="chat-area">
          {scopeLabel && (
            <div className="chat-scope-label">{scopeLabel}</div>
          )}

          <div ref={chatHistoryRef} className="chat-history">
            {messages.map((msg, idx) => {
              if (msg.synthetic) return null;
              const isLatest = idx === messages.length - 1;
              const hasStreamingContent =
                streaming.isStreaming && streaming.fullAnswer.length > 0;
              const isStreamingMsg =
                isLatest && hasStreamingContent && msg.role === 'assistant';
              return (
                <div key={`${msg.role}-${idx}`} className={`message ${msg.role}`}>
                  <div className="bubble"><Markdown content={msg.content} /></div>
                  {msg.error && <div className="error-message">{msg.error}</div>}
                  {msg.rag_sources && msg.rag_sources.length > 0 && (
                    <details className="rag-sources">
                      <summary>RAG document sources ({msg.rag_sources.length})</summary>
                      <ul>
                        {msg.rag_sources.map((src, sidx) => (
                          <li key={sidx}>{src}</li>
                        ))}
                      </ul>
                    </details>
                  )}
                  {msg.confluence_links && msg.confluence_links.length > 0 && (
                    <details className="confluence-links">
                      <summary>Confluence sources ({msg.confluence_links.length})</summary>
                      <ul>
                        {msg.confluence_links.map((link, lidx) => (
                          <li key={lidx}>
                            <a href={link.url} target="_blank" rel="noopener noreferrer">
                              {link.title}
                            </a>
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                  {/* Live streaming content rendered inline while streaming */}
                  {isStreamingMsg && (
                    <div className="bubble">
                      <Markdown content={streaming.fullAnswer} />
                    </div>
                  )}
                </div>
              );
            })}

            {/* Empty state */}
            {messages.filter((m) => !m.synthetic).length === 0 &&
              !streaming.isStreaming && (
                <div className="chat-empty">
                  <div className="chat-empty-icon">🤖</div>
                  <div className="chat-empty-text">
                    Select a domain to get started, or ask me anything!
                  </div>
                </div>
              )}
          </div>

          {/* Input area */}
          <div className="chat-input-area">
            <form className="chat-input-form" onSubmit={handleSubmit}>
              <input
                ref={inputRef}
                className="chat-input-field"
                type="text"
                value={inputValue}
                onChange={(e) => setInputValue(e.target.value)}
                placeholder={placeholder}
                disabled={streaming.isStreaming}
                autoComplete="off"
              />
              {streaming.isStreaming ? (
                <button
                  className="chat-input-stop"
                  onClick={handleStop}
                  type="button"
                  title="Stop generation"
                >
                  <span aria-hidden>⏹</span>
                </button>
              ) : (
                <button
                  className="chat-input-send"
                  disabled={!inputValue.trim()}
                  type="submit"
                >
                  <span aria-hidden>→</span>
                </button>
              )}
            </form>
            {settingsMessage && (
              <div
                className={`settings-message ${
                  settingsMessage.type === "ok" ? "ok" : "error"
                }`}
                role="status"
              >
                {settingsMessage.text}
              </div>
            )}
          </div>
        </div>
      </main>
    </div>
  );
}

// Sidebar component - matching Streamlit sidebar exactly
const Sidebar = ({ 
  selectedDomain,
  subCategoryKey,
  conversations, 
  onNewConversation, 
  onChangeSelection,
  onSelectQuickAction,
  onConversationSelect,
  onConversationDelete,
  onClearAllConversations,
  activeConversationId
}: { 
  selectedDomain: Domain | null;
  subCategoryKey: string | null;
  conversations: {
    domains: Domain[];
    list: Array<{ id: string; title: string }>;
  };
  onNewConversation: () => void;
  onChangeSelection: () => void;
  onSelectQuickAction: (domain: Domain) => void;
  onConversationSelect: (id: string) => void;
  onConversationDelete: (id: string) => void;
  onClearAllConversations: () => void;
  activeConversationId: string;
}) => {
  // Get current sub-category info if available
  const selectedDomainInfo = conversations.domains.find(d => d.key === selectedDomain?.key);
  
  // Get current label
  let currentLabel = "";
  if (selectedDomain && selectedDomainInfo) {
    currentLabel = `${selectedDomain.icon} ${selectedDomain.display_name}`;
    if (subCategoryKey && selectedDomainInfo.sub_categories?.[subCategoryKey]) {
      currentLabel += ` > ${selectedDomainInfo.sub_categories[subCategoryKey].display_name}`;
    }
  }
  
  return (
    <div className="sidebar">
      <h2 className="sidebar-title">SupersonicIQ</h2>
      
      <button 
        onClick={onNewConversation}
        className="new-conversation-btn"
      >
        <span>🆕 New Conversation</span>
      </button>

      {selectedDomain ? (
        <div className="divider" />
      ) : null}

      {currentLabel ? (
        <div className="current-selection">
          <strong className="current-selection-label">{currentLabel}</strong>
          <button 
            onClick={onChangeSelection}
            className="change-selection-btn"
          >
            Change selection
          </button>
        </div>
      ) : null}

      {selectedDomain ? (
        <div className="divider" />
      ) : null}

      {/* Quick Actions - matching Streamlit expander style */}
      <details open className="quick-actions-section">
        <summary className="section-header">
          <span className="expander-icon">▸</span>
          <span className="section-title">Quick Actions</span>
        </summary>
        {conversations.domains.map(domain => (
          <button 
            key={domain.key}
            onClick={() => onSelectQuickAction(domain)}
            className={`quick-action-btn ${selectedDomain?.key === domain.key ? 'active' : ''}`}
          >
            {domain.icon} {domain.display_name}
          </button>
        ))}
      </details>

      {selectedDomain ? (
        <div className="divider" />
      ) : null}

      {/* Conversations - matching Streamlit expander style */}
      <details open className="conversations-section">
        <summary className="section-header">
          <span className="expander-icon">▸</span>
          <span className="section-title">Conversations</span>
        </summary>
        {conversations.list.length > 0 ? (
          conversations.list.map(conv => (
            <div
              key={conv.id}
              className={`conversation-row ${conv.id === activeConversationId ? 'active' : ''}`}
            >
              <button
                className="conversation-btn"
                onClick={() => onConversationSelect(conv.id)}
              >
                {conv.title || 'Untitled'}
              </button>
              <button
                className="conversation-trash"
                title="Delete conversation"
                onClick={(e) => { e.stopPropagation(); onConversationDelete(conv.id); }}
              >
                🗑️
              </button>
            </div>
          ))
        ) : (
          <div className="conversation-empty">No conversations yet</div>
        )}

        {conversations.list.length > 0 ? (
          <button
            className="clear-all-conversations"
            onClick={onClearAllConversations}
          >
            🧹 Clear all
          </button>
        ) : null}
      </details>
    </div>
  );
};

// Domain pills component - matching Streamlit st.pills style
const DomainPills = ({ domains, onSelection }: { domains: Domain[]; onSelection: (domain: Domain) => void }) => {
  if (!domains.length) return null;
  
  return (
    <div className="domain-pills">
      <h2 className="welcome-heading">Welcome to SupersonicIQ</h2>
      <p className="welcome-text">I can help you find information quickly. Select an area to get started:</p>
      
      <div className="pill-container">
        <p className="pill-label">Select area:</p>
        <div className="pill-group" role="radiogroup">
          {domains.map(domain => (
            <button
              key={domain.key}
              onClick={() => onSelection(domain)}
              className="pill"
              role="radio"
              aria-checked={false}
            >
              <span className="pill-icon">{domain.icon}</span>
              {domain.display_name}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
};

// Sub-category pills component
const SubCategoryPills = ({ 
  domainName, 
  subCategories, 
  onBack,
  onSelect
}: { 
  domainName: string; 
  subCategories: Record<string, SubCategory>;
  onBack: () => void;
  onSelect: (domain: Domain, key: string) => void;
}) => {
  if (!Object.keys(subCategories).length) return null;

  // We need to extract the current domain from somewhere - in real implementation
  // this would be passed as a prop. For now we'll just use a dummy.
  
  const handleSelect = (key: string) => {
    onSelect({
      key: 'dummy',
      display_name: domainName,
      icon: ''
    } as Domain, key);
  };

  return (
    <div className="subcat-pills">
      <h2 className="welcome-heading">How can I help with {domainName}?</h2>
      
      <div className="pill-container">
        <p className="pill-label">Select category:</p>
        <div className="pill-group" role="radiogroup">
          {Object.entries(subCategories).map(([key, info]) => (
            <button
              key={key}
              onClick={() => handleSelect(key)}
              className="pill"
              role="radio"
              aria-checked={false}
            >
              <span className="pill-icon">{info.icon}</span>
              {info.display_name}
            </button>
          ))}
        </div>
      </div>

      <button onClick={onBack} className="back-btn">
        ← Back to domains
      </button>
    </div>
  );
};

