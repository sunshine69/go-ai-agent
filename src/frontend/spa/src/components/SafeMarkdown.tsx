import React from "react";
import { marked } from "marked";
import DOMPurify from "dompurify";

// Configure marked options for safe HTML rendering.
// NOTE: marked v18 removed its own `sanitize` option, so sanitization is
// applied below via DOMPurify to prevent XSS attacks.
marked.setOptions({
  gfm: true,
  breaks: true,
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
    // marked (v18) dropped its own sanitize option, so sanitize via DOMPurify
    const rawHtml = marked.parse(content) as string;
    const html = DOMPurify.sanitize(rawHtml) as string;

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
