import React from "react";
import { marked } from "marked";

// Configure marked options for safe HTML rendering
marked.setOptions({
  gfm: true,
  breaks: true,
  sanitize: true, // Sanitize HTML to prevent XSS attacks
});

interface MarkdownProps {
  content: string;
  className?: string;
}

/**
 * Renders formatted markdown with proper sanitization.
 * Use this component for assistant messages that contain markdown content.
 */
export const SafeMarkdown: React.FC<MarkdownProps> = ({ content, className = "" }) => {
  try {
    // Convert markdown to HTML
    const html = marked.parse(content) as string;
    
    return (
      <div
        className={`markdown-body ${className}`}
        dangerouslySetInnerHTML={{ __html: html }}
      />
    );
  } catch (error) {
    console.error("Error converting markdown to HTML:", error);
    // Fallback to plain text with line breaks on error
    return (
      <div className={className}>
        {content.split("\n").map((line, i) => (
          <React.Fragment key={i}>
            {line}
            <br />
          </React.Fragment>
        ))}
      </div>
    );
  }
};
