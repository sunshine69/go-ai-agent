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
  onMicClick: () => void;
  ttsEnabled: boolean;
  onToggleTts: () => void;
  isSpeaking: boolean;
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
  onMicClick,
  ttsEnabled,
  onToggleTts,
  isSpeaking,
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
          {!isLoading && (
            <button
              className="chat-input-mic"
              type="button"
              onClick={onMicClick}
              title="Speak (English only)"
            >
              <svg
                className="chat-input-mic-icon"
                viewBox="0 0 24 24"
                fill="currentColor"
              >
                <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z" />
                <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
                <line x1="12" y1="19" x2="12" y2="23" />
                <line x1="8" y1="23" x2="16" y2="23" />
              </svg>
            </button>
          )}
          <button
            className={`chat-input-tts${isSpeaking && ttsEnabled ? " is-speaking" : ""}`}
            type="button"
            onClick={onToggleTts}
            title={ttsEnabled ? "Turn off text-to-speech" : "Turn on text-to-speech"}
          >
            <svg
              className="chat-input-tts-icon"
              viewBox="0 0 24 24"
              fill="currentColor"
            >
              {ttsEnabled ? (
                <>
                  <path d="M3 9v6h5l7 5V4L8 9H3z" />
                  <path d="M16.5 12c0-1.77-1.02-3.29-2.5-4.03v2.21c.91.44 1.5 1.4 1.5 2.5 0 1.1-.59 2.06-1.5 2.5v2.21c1.48-.74 2.5-2.26 2.5-4.03z" />
                  <path d="M14 3.23v2.06c2.89.74 5 3.37 5 6.71 0 3.34-2.11 5.97-5 6.71v2.06c4.01-.91 7-4.49 7-8.77S18.01 4.14 14 3.23z" />
                </>
              ) : (
                <>
                  <path d="M3 9v6h5l7 5V4L8 9H3z" />
                  <line x1="1" y1="1" x2="23" y2="23" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
                </>
              )}
            </svg>
          </button>
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
