import React from "react";
import { marked } from "marked";

// Configure marked: allow HTML, sanitize, proper line breaks
marked.setOptions({
  gfm: true,
  breaks: true,
});

interface MarkdownProps {
  content: string;
  className?: string;
  // When true, render the raw text with a blinking cursor instead of parsing
  // markdown. Partial (unterminated) markdown renders malformed HTML when
  // parsed, so the SPA shows streamed text as plain text during streaming.
  isStreaming?: boolean;
}

export const Markdown: React.FC<MarkdownProps> = ({
  content,
  className = "",
  isStreaming = false,
}) => {
  if (isStreaming) {
    return (
      <div className={`markdown ${className}`}>
        <span className="streaming-cursor">|</span>
        {content}
        <span className="streaming-cursor">|</span>
      </div>
    );
  }

  const html = marked.parse(content) as string;

  return (
    <div
      className={`markdown ${className}`}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
};

export default Markdown;
