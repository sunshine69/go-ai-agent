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
}

export const Markdown: React.FC<MarkdownProps> = ({ content, className = "" }) => {
  const html = marked.parse(content) as string;

  return (
    <div
      className={`markdown ${className}`}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
};

export default Markdown;
