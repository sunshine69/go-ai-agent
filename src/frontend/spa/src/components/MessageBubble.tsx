import React, { useState } from "react";
import { markdownToHtml } from "../utils/markdown";

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
}) => {
  const [showSources, setShowSources] = useState(false);
  const [showConfluence, setShowConfluence] = useState(false);
  const isUser = role === "user";

  // Command feedback (e.g. /ctx) is rendered as a retained, styled assistant
  // message with `error` prefixed "command_result:<type>". This distinguishes
  // it from streaming errors and lets it sit visually in the main chat flow.
  const cmdType = error?.startsWith("command_result:")
    ? error.replace("command_result:", "")
    : "";
  const isCommandResult = cmdType !== "";

  // For assistant messages with markdown content
  if (role === "assistant" && !isStreaming) {
    return (
      <div
        className={
          `message-bubble ${isUser ? "user" : "assistant"}` +
          (isCommandResult ? ` cmd-${cmdType}` : "")
        }
      >
        {/* Render HTML from marked markdown */}
        <div 
          className="message-content"
          dangerouslySetInnerHTML={{ __html: markdownToHtml(content || "") }}
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
              <span className="confluence-toggle-arrow">{showConfluence ? "▲" : "▼"}</span>
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

  // For streaming content and user messages, display as raw text
  return (
    <div
      className={
        `message-bubble ${isUser ? "user" : "assistant"}` +
        (isCommandResult ? ` cmd-${cmdType}` : "")
      }
    >
      <div className="message-content">
        {isStreaming && (
          <span className="streaming-cursor">|</span>
        )}
        {content}
        {isStreaming && (
          <span className="streaming-cursor">|</span>
        )}
      </div>

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
            <span className="confluence-toggle-arrow">{showConfluence ? "▲" : "▼"}</span>
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
