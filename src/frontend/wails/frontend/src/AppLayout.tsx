import React, { useState } from "react";
import "./style.css";
import { Markdown } from "./components/Markdown";

// Base URL for the AI backend, read from the .env file (VITE_BACKEND_URL).
// Mirrors the pattern used in hooks/useApi.ts so all API calls resolve to the
// configured backend location instead of a hard-coded localhost address.
const API_BASE = `${import.meta.env.VITE_BACKEND_URL || "http://localhost:8000"}/api`;

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
  const [selectedDomain, setSelectedDomain] = useState<Domain | null>(null);
  const [subCategoryKey, setSubCategoryKey] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  
  // State for conversations data (domains + list)
  const [conversationsData, setConversationsData] = useState({
    domains: [] as Domain[],
    list: [] as Array<{ id: string; title: string }>
  });

  // --- Conversation history state -----------------------------------------
  // Id of the active conversation. Empty string => no active conversation yet
  // (the user is still on domain selection). A real id means the messages array
  // below is a live mirror of what the backend has persisted for that turn.
  const [conversationId, setConversationId] = useState<string>("");
  // Loading state
  const [isLoading, setIsLoading] = useState(false);

  // Abort controller for stopping an in-flight generation (Streamlit "stop" button)
  const abortRef = React.useRef<AbortController | null>(null);

  // Load domains + conversation list on startup
  React.useEffect(() => {
    if (conversationsData.domains.length === 0) {
      fetch(API_BASE + '/domains')
        .then(res => res.json())
        .then(data => {
          setConversationsData(prev => ({
            domains: data.domains || [],
            list: prev.list
          }));
        })
        .catch(() => {});
    }
  }, [conversationsData.domains.length]);

  React.useEffect(() => {
    fetch(API_BASE + '/conversations')
      .then(res => res.json())
      .then(data => setConversationsData(prev => ({
        domains: prev.domains,
        list: Array.isArray(data) ? data : []
      })))
      .catch(() => {});
  }, []);

  // Handle domain selection
  const handleDomainSelection = (domain: Domain) => {
    setSelectedDomain(domain);
    
    // Reset sub-category key when selecting a new domain
    setSubCategoryKey(null);
    
    // Simulate assistant response like the Streamlit version does. This is a
    // display-only intro — tagged synthetic so it is excluded from the history
    // payload sent to the LLM on the next request.
    setMessages(prev => [
      ...prev,
      { 
        role: 'assistant', 
        synthetic: true,
        content: `Great! I'm ready to help with ${domain.display_name}. What would you like to know?` 
      }
    ]);
  };

  // New conversation handler - reset everything and start fresh
  const handleNewConversation = () => {
    setSelectedDomain(null);
    setSubCategoryKey(null);
    setMessages([]);
    setConversationId("");
    // keep the sidebar conversation list in sync (the one just deleted is gone)
    setConversationsData(prev => ({
      domains: prev.domains,
      list: prev.list.filter(c => c.id !== conversationId)
    }));
  };

  // Load an existing conversation into the chat pane
  const handleConversationSelect = (id: string) => {
    fetch(`${API_BASE}/conversations/${id}`)
      .then(res => res.json())
      .then(data => {
        const messages = (data.messages || [])
          .filter((m: any) => m.role !== "user")
          .map((m: any) => ({
            role: m.role,
            content: m.content,
          }));
        setMessages(messages);
        setConversationId(id);
      })
      .catch(() => {});
  };

  // Refresh the sidebar conversation list
  const refreshConversationList = () => {
    fetch(API_BASE + '/conversations')
      .then(res => res.json())
      .then(data => setConversationsData(prev => ({
        domains: prev.domains,
        list: Array.isArray(data) ? data : []
      })))
      .catch(() => {});
  };

  // Delete a single conversation from the backend + sidebar list
  const handleConversationDelete = (id: string) => {
    fetch(`${API_BASE}/conversations/${id}`, { method: 'DELETE' })
      .then(refreshConversationList)
      .catch(() => {});
    if (id === conversationId) {
      setMessages([]);
      setConversationId("");
    }
  };

  // Delete every conversation in one shot
  const handleClearAllConversations = () => {
    fetch(API_BASE + '/conversations', { method: 'DELETE' })
      .then(refreshConversationList)
      .catch(() => {});
    setMessages([]);
    setConversationId("");
  };

  // Change selection button clicked (go back to domain selection)
  const handleChangeSelection = () => {
    setSelectedDomain(null);
    setSubCategoryKey(null);
  };
  
  // Handle sub-category selection
  const handleSubCategorySelection = (domain: Domain, key: string) => {
    setSelectedDomain(domain);
    setSubCategoryKey(key);
    
    // Simulate assistant response like the Streamlit version does
    setMessages(prev => [
      ...prev,
      { 
        role: 'assistant', 
        content: `Perfect! Let me help you with ${domain.sub_categories?.[key]?.display_name || key}. What would you like to know?` 
      }
    ]);
  };

  // Quick action - jump directly to a domain
  const handleQuickAction = (domain: Domain) => {
    setSelectedDomain(domain);
    setSubCategoryKey(null);
    setMessages([]);
  };

  // Handle sending message to backend API
  const handleSendMessage = async (message: string) => {
    // Build the prior-turn history that the backend needs to remember context.
    // Skip synthetic intro messages (domain selection) and the optimistic user
    // turn just pushed below. Each history entry mirrors the backend's stored
    // turns so the next request can reconstruct the same context.
    const history = messages
      .filter((m) => !m.synthetic && m.role !== 'user')
      .map((m) => ({ role: m.role, content: m.content }));

    // Build the request payload. conversation_id "" => backend creates a new
    // conversation; a real id => append to that conversation (cross-turn memory).
    const data: {
      message: string;
      conversation_id: string;
      history?: any[];
      domain?: string;
      sub_category?: string;
    } = { message, conversation_id: conversationId, history };

    if (selectedDomain?.key) {
      data.domain = selectedDomain.key;

      // Get display name of current sub-category
      const subCategoryInfo = selectedDomain.sub_categories?.[subCategoryKey || ''];
      if (subCategoryInfo && subCategoryInfo.display_name) {
        data.sub_category = subCategoryInfo.display_name;
      }
    }

    // Optimistically show the user's message immediately (before the model responds)
    setMessages(prev => [
      ...prev,
      { role: 'user', content: message }
    ]);

    setIsLoading(true);

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const response = await fetch(API_BASE + '/messages', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify(data),
        signal: controller.signal
      });

      if (!response.ok) throw new Error(`HTTP ${response.status}`);

      const result = await response.json();

      // Capture (or create) the conversation id from the backend response so
      // subsequent turns stay in the same context.
      if (result.conversation_id) {
        setConversationId(result.conversation_id);
      }

      setMessages(prev => [
        ...prev,
        {
          role: 'assistant',
          content: result.answer || '',
          confluence_links: result.confluence_links || [],
          rag_sources: result.sources || []
        }
      ]);

      // Refresh the sidebar list so new conversations appear
      fetch(API_BASE + '/conversations')
        .then(res => res.json())
        .then(data => setConversationsData(prev => ({
          domains: prev.domains,
          list: Array.isArray(data) ? data : []
        })))
        .catch(() => {});

    } catch (error) {
      // Ignore the abort (stop) signal — a clean "stopped" note is shown below
      if ((error as DOMException).name === 'AbortError') {
        setMessages(prev => [
          ...prev,
          {
            role: 'assistant',
            content: '⏹ Generation stopped.',
            error: 'stopped'
          }
        ]);
      } else {
        const errorMessage = error instanceof Error ? error.message : String(error);
        
        setMessages(prev => [
          ...prev,
          { 
            role: 'assistant', 
            content: `Error: ${errorMessage}`,
            error: errorMessage
          }
        ]);
      }
    } finally {
      setIsLoading(false);
      abortRef.current = null;
    }
  };

  // Stop an in-flight generation (Streamlit "stop" button)
  const handleStop = () => {
    abortRef.current?.abort();
  };

  // Get current sub-category info - handle undefined case properly with fallback
  const selectedDomainInfo = conversationsData.domains.find(d => d.key === selectedDomain?.key);

  return (
    <div className="app-container">
      {/* Sidebar - always visible */}
      <Sidebar 
        selectedDomain={selectedDomain}
        subCategoryKey={subCategoryKey}
        conversations={{
          domains: conversationsData.domains,
          list: conversationsData.list
        }}
        onNewConversation={handleNewConversation}
        onChangeSelection={handleChangeSelection}
        onSelectQuickAction={handleQuickAction}
        onConversationSelect={handleConversationSelect}
        onConversationDelete={handleConversationDelete}
        onClearAllConversations={handleClearAllConversations}
        activeConversationId={conversationId}
      />

      {/* Main content */}
      <main className="main-content">
        {!selectedDomain ? (
          // Domain pills when no domain selected
          <DomainPills 
            domains={conversationsData.domains} 
            onSelection={handleDomainSelection}
          />
        ) : Object.keys(selectedDomainInfo?.sub_categories || {}).length > 0 && !subCategoryKey ? (
          // Sub-category pills after domain selection
          <SubCategoryPills
            domainName={selectedDomain.display_name}
            subCategories={selectedDomainInfo?.sub_categories || {}}
            onBack={handleChangeSelection}
            onSelect={handleSubCategorySelection}
          />
        ) : null}

        {/* Chat input always visible */}
        <ChatInput 
          messages={messages}
          selectedDomain={selectedDomain}
          subCategoryKey={subCategoryKey}
          onSendMessage={handleSendMessage}
          onStop={handleStop}
          isLoading={isLoading}
        />
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

// Chat input component - matching Streamlit chat_input style
const ChatInput = ({ 
  messages, 
  selectedDomain,
  subCategoryKey,
  onSendMessage,
  onStop,
  isLoading 
}: { 
  messages: Message[];
  selectedDomain?: Domain | null;
  subCategoryKey?: string | null;
  onSendMessage: (msg: string) => void;
  onStop?: () => void;
  isLoading: boolean;
}) => {
  const [inputValue, setInputValue] = useState('');

  // Determine placeholder text based on scope
  let placeholder = "Ask me anything...";
  
  if (selectedDomain) {
    const domainName = selectedDomain.display_name;
    
    // Get sub-category label if available
    const subCategoryInfo = selectedDomain.sub_categories?.[subCategoryKey || ''];
    let subCategoryLabel = "";
    if (subCategoryInfo && subCategoryInfo.display_name) {
      subCategoryLabel = ` > ${subCategoryInfo.display_name}`;
    }
    
    placeholder = `Ask about ${domainName}${subCategoryLabel}...`;
  }

  // Show domain label only when both domain and sub-category are selected
  const showLabel = !!selectedDomain && !!subCategoryKey;

  return (
    <div className="chat-container">
      {showLabel ? (
        <div className="domain-label">
          <strong className="domain-label-text">
            {selectedDomain?.icon} {selectedDomain.display_name}
            {' > '}<strong>{subCategoryKey || ''}</strong>
          </strong>
        </div>
      ) : null}

      {/* Stop generation button (top-right, shown while answering) */}
      {isLoading && onStop ? (
        <button
          onClick={onStop}
          className="stop-generation-btn"
          title="Stop generation"
        >
          <svg className="stop-icon" width="13" height="13" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
            <rect x="2.5" y="2.5" width="11" height="11" rx="2.5" />
          </svg>
          <span className="stop-label">Stop</span>
        </button>
      ) : null}

      {/* Chat history */}
      <div className="chat-history">
        {messages.map((msg, idx) => (
          <div key={idx} className={`message ${msg.role}`}>
            <div className="bubble"><Markdown content={msg.content} /></div>
            
            {/* Error message if any */}
            {msg.error && (
              <div className="error-message">⚠️ {msg.error}</div>
            )}
            
            {/* RAG sources (simplified for now - expandable not implemented yet) */}
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

            {/* Confluence links */}
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

            {/* Contribute button (simplified - would link to form URL) */}
            {msg.role === 'assistant' && !msg.error && (
              <button className="btn-contribute">⚠️ Missing information or not what you expected? Please contribute</button>
            )}
          </div>
        ))}
      </div>

      {/* Chat input at the bottom */}
      <form 
        onSubmit={(e) => {
          e.preventDefault();
          if (inputValue.trim() && !isLoading) {
            onSendMessage(inputValue);
            setInputValue('');
          }
        }}
        className="chat-input-form"
      >
        <input
          type="text"
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          placeholder={placeholder}
          disabled={isLoading}
          autoFocus={!isLoading}
          className="chat-input-field"
        />
        <button 
          type="submit" 
          disabled={!inputValue.trim() || isLoading}
          className="send-button"
        >
          Send
        </button>
      </form>
    </div>
  );
};
