import React, { useState } from "react";
import { SafeMarkdown } from "./SafeMarkdown";

interface MessageBubbleProps {
  role: "user" | "assistant";
  content: string;
  sources?: string[];
  confluenceLinks?: Array<{ title: string; url: string }>;
  isStreaming?: boolean;
  error?: string;
}

export const MessageBubble: React.FC<MessageBubbleProps> = ({
  role,
  content,
  sources,
  confluenceLinks,
  isStreaming,
  error,
  // isUser is computed below
}) => {
  const isUser = role === "user";
  const [showSources, setShowSources] = useState(false);
  const [showConfluence, setShowConfluence] = useState(false);

  // For assistant messages with markdown content (non-streaming)
  if (role === "assistant" && !isStreaming) {
    return (
      <div className={`message-bubble ${isUser ? "user" : "assistant"}`}>
        {/* Render HTML from marked markdown */}
        <SafeMarkdown 
          content={content || ""} 
          className="message-content"
        />

        {/* Error indicator */}
        {error && (
          <div className="message-error">{error}</div>
        )}

        {/* RAG sources */}
        {sources && sources.length > 0 && (
          <div className="message-sources">
            <button
              className="sources-toggle"
              onClick={() => setShowSources(!showSources)}
              aria-expanded={showSources}
            >
              <span className="sources-toggle-icon">📄</span>
              <span className="sources-toggle-text">
                RAG document sources ({sources.length})
              </span>
              <span className="sources-toggle-arrow">{showSources ? "▲" : "▼"}</span>
            </button>
            {showSources && (
              <ul className="sources-list">
                {sources.map((src, idx) => (
                  <li key={idx} className="source-item">
                    {src}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}

        {/* Confluence links */}
        {confluenceLinks && confluenceLinks.length > 0 && (
          <div className="message-confluence">
            <button
              className="confluence-toggle"
              onClick={() => setShowConfluence(!showConfluence)}
              aria-expanded={showConfluence}
            >
              <span className="confluence-toggle-icon">🔗</span>
              <span className="confluence-toggle-text">
                Confluence sources ({confluenceLinks.length})
              </span>
              <span className="sources-toggle-arrow">{showConfluence ? "▲" : "▼"}</span>
            </button>
            {showConfluence && (
              <ul className="confluence-list">
                {confluenceLinks.map((link, idx) => (
                  <li key={idx} className="confluence-item">
                    <a
                      href={link.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="confluence-link"
                    >
                      {link.title}
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
    );
  }

  // For streaming content and user messages, display as formatted markdown
  return (
    <div className={`message-bubble ${isUser ? "user" : "assistant"}`}>
      {/* Render HTML from marked markdown with live updates during streaming */}
      {role === "assistant" && isStreaming ? (
        <SafeMarkdown 
          content={content || ""} 
          className="message-content"
        />
      ) : (
        <div className="message-content">
          {isStreaming && role === "user" && (
            <span className="streaming-cursor">|</span>
          )}
          {content}
          {isStreaming && role !== "user" && (
            <span className="streaming-cursor">|</span>
          )}
        </div>
      )}

      {/* Error indicator */}
      {error && (
        <div className="message-error">{error}</div>
      )}

      {/* RAG sources */}
      {sources && sources.length > 0 && (
        <div className="message-sources">
          <button
            className="sources-toggle"
            onClick={() => setShowSources(!showSources)}
            aria-expanded={showSources}
          >
            <span className="sources-toggle-icon">📄</span>
            <span className="sources-toggle-text">
              RAG document sources ({sources.length})
            </span>
            <span className="sources-toggle-arrow">{showSources ? "▲" : "▼"}</span>
          </button>
          {showSources && (
            <ul className="sources-list">
              {sources.map((src, idx) => (
                <li key={idx} className="source-item">
                  {src}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {/* Confluence links */}
      {confluenceLinks && confluenceLinks.length > 0 && (
        <div className="message-confluence">
          <button
            className="confluence-toggle"
            onClick={() => setShowConfluence(!showConfluence)}
            aria-expanded={showConfluence}
          >
            <span className="confluence-toggle-icon">🔗</span>
            <span className="confluence-toggle-text">
              Confluence sources ({confluenceLinks.length})
            </span>
            <span className="sources-toggle-arrow">{showConfluence ? "▲" : "▼"}</span>
          </button>
          {showConfluence && (
            <ul className="confluence-list">
              {confluenceLinks.map((link, idx) => (
                <li key={idx} className="confluence-item">
                  <a
                    href={link.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="confluence-link"
                  >
                    {link.title}
                  </a>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
};
