import React from "react";

interface MessageBubbleProps {
  role: "user" | "assistant";
  content: string;
  sources?: string[];
  confluenceLinks?: Array<{ title: string; url: string }>;
  key?: string;
}

export const MessageBubble: React.FC<MessageBubbleProps> = ({
  role,
  content,
  sources,
  confluenceLinks,
  key,
}) => {
  const isUser = role === "user";

  return (
    <div
      key={key}
      className={`message-bubble ${isUser ? "user" : "assistant"}`}
    >
      <div className="message-content">{content}</div>

      {/* RAG sources */}
      {sources && sources.length > 0 && (
        <div className="message-sources">
          <button className="sources-toggle" aria-expanded="false">
            <span className="sources-toggle-icon">📄</span>
            <span className="sources-toggle-text">
              RAG document sources ({sources.length})
            </span>
          </button>
          <ul className="sources-list" aria-hidden="true">
            {sources.map((src, idx) => (
              <li key={idx} className="source-item">
                {src}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Confluence links */}
      {confluenceLinks && confluenceLinks.length > 0 && (
        <div className="message-confluence">
          <button
            className="confluence-toggle"
            aria-expanded="false"
          >
            <span className="confluence-toggle-icon">🔗</span>
            <span className="confluence-toggle-text">
              Confluence sources ({confluenceLinks.length})
            </span>
          </button>
          <ul className="confluence-list" aria-hidden="true">
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
        </div>
      )}
    </div>
  );
};
