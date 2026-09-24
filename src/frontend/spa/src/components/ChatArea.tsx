import React, { useEffect, useCallback } from "react";
import { MessageBubble } from "./MessageBubble";
import type { ChatMessage, StreamingState } from "../hooks/useStreaming";

interface ChatAreaProps {
  chatHistoryRef: React.RefObject<HTMLDivElement>;
  messages: ChatMessage[];
  streamingState: StreamingState;
  scopeLabel: string;
  modelLabel: string;
  modelResponse: string | null;
  inputRef: React.RefObject<HTMLTextAreaElement>;
  inputValue: string;
  setInputValue: (value: string) => void;
  placeholder: string;
  isLoading: boolean;
  onSend: (e: React.FormEvent) => void;
  onStop: () => void;
}

export const ChatArea: React.FC<ChatAreaProps> = ({
  chatHistoryRef,
  messages,
  streamingState,
  scopeLabel,
  modelLabel,
  modelResponse,
  inputRef,
  inputValue,
  setInputValue,
  placeholder,
  isLoading,
  onSend,
  onStop,
}) => {
  // Check if we're currently streaming with content
  const hasStreamingContent =
    streamingState.isStreaming && streamingState.fullAnswer.length > 0;

  // Auto-grow the textarea as the user adds newlines, then cap its height.
  const resizeTextarea = useCallback(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = String(Math.min(el.scrollHeight, 200));
  }, [inputRef]);

  useEffect(() => {
    resizeTextarea();
  }, [resizeTextarea, inputValue]);

  // Debug: log streaming state changes
  useEffect(() => {
    console.log("ChatArea streaming state:", {
      isStreaming: streamingState.isStreaming,
      fullAnswerLength: streamingState.fullAnswer.length,
      hasContent: hasStreamingContent,
      currentChunk: streamingState.currentChunk,
    });
  }, [streamingState.isStreaming, streamingState.fullAnswer, hasStreamingContent]);

  // Debug: log messages array
  useEffect(() => {
    console.log("ChatArea messages:", messages.length, messages.map(m => ({
      role: m.role,
      hasContent: !!m.content,
      contentLength: m.content?.length || 0,
    })));
  }, [messages]);

  return (
    <div className="chat-area">
      {/* Scope label */}
      {scopeLabel && (
        <div className="chat-scope-label">{scopeLabel}</div>

      )}
      {/* Model label */}
      {modelLabel && (
        <div className="chat-model-label">{modelLabel}</div>
      )}

      {/* Model command response */}
      {modelResponse && (
        <div className="chat-model-response">{modelResponse}</div>
      )}

      {/* Chat history */}
      <div ref={chatHistoryRef} className="chat-history">
        {messages.map((msg, idx) => {
          if (msg.synthetic) {
            return null;
          }
          const isLatest = idx === messages.length - 1;
          return (
            <MessageBubble
              key={`${msg.role}-${idx}`}
              role={msg.role}
              content={msg.content || ""}
              isStreaming={isLatest && hasStreamingContent && msg.role === "assistant"}
              error={msg.error}
              sources={msg.sources}
              confluenceLinks={msg.confluence_links}
            />
          );
        })}

        {/* Empty state */}
        {messages.filter(m => !m.synthetic).length === 0 && !hasStreamingContent && (
          <div className="chat-empty">
            <div className="chat-empty-icon">🤖</div>
            <div className="chat-empty-text">
              Select a domain to get started, or ask me anything!
            </div>
          </div>
        )}

        {/* Streaming content - render inline during streaming */}
        {hasStreamingContent && (
          <MessageBubble
            role="assistant"
            content={streamingState.fullAnswer}
            isStreaming={true}
          />
        )}
      </div>

      {/* Input area */}
      <div className="chat-input-area">
        <form className="chat-input-form" onSubmit={onSend}>
          <textarea
            ref={inputRef}
            className="chat-input chat-input-textarea"
            rows={1}
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            placeholder={placeholder}
            disabled={isLoading}
            onKeyDown={(e) => {
              // Alt + Enter inserts a newline (and keeps it displayed),
              // mirroring the behavior of a normal text editor.
              if (e.key === "Enter" && e.altKey) {
                e.preventDefault();
                const target = e.target as HTMLTextAreaElement;
                const start = target.selectionStart ?? 0;
                const end = target.selectionEnd ?? 0;
                const next =
                  target.value.slice(0, start) + "\n" + target.value.slice(end);
                setInputValue(next);
                // Restore caret right after the inserted newline.
                window.requestAnimationFrame(() => {
                  target.selectionStart = target.selectionEnd = start + 1;
                });
              } else if (e.key === "Enter" && !e.shiftKey) {
                // Plain Enter: submit instead of inserting a blank line
                // (textarea, unlike input[type=text], inserts a newline by default).
                e.preventDefault();
                e.currentTarget.closest("form")?.requestSubmit();
              }
            }}
          />
          {isLoading ? (
            <button className="chat-input-stop" onClick={onStop} type="button">
              <svg
                className="chat-input-stop-icon"
                viewBox="0 0 24 24"
                fill="currentColor"
              >
                <rect x="6" y="6" width="12" height="12" rx="2" />
              </svg>
            </button>
          ) : (
            <button
              className="chat-input-send"
              disabled={!inputValue.trim()}
              type="submit"
            >
              <svg
                className="chat-input-send-icon"
                viewBox="0 0 24 24"
                fill="currentColor"
              >
                <path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z" />
              </svg>
            </button>
          )}
        </form>
      </div>
    </div>
  );
};
