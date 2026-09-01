import React, { useState, useRef, useEffect } from "react";
import type { Domain } from "../types/api";

interface ChatInputProps {
  messages?: unknown;
  selectedDomain?: Domain | null;
  subCategoryKey?: string | null;
  onSendMessage: (message: string) => void;
  onStop?: () => void;
  isLoading: boolean;
  placeholder?: string;
  inputValue?: string;
}

export const ChatInput: React.FC<ChatInputProps> = ({
  selectedDomain,
  subCategoryKey,
  onSendMessage,
  onStop,
  isLoading,
  placeholder,
}) => {
  const [value, setValue] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input when it becomes enabled (e.g. streaming stops).
  useEffect(() => {
    if (!isLoading && inputRef.current) {
      inputRef.current.focus();
    }
  }, [isLoading]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = value.trim();
    if (!trimmed) return;
    onSendMessage(trimmed);
    setValue("");
  };

  // Scope-aware label shown above the input (mirrors the SPA placeholder logic).
  let scopeLabel = "";
  if (selectedDomain?.icon) {
    scopeLabel = `${selectedDomain.icon} ${selectedDomain.display_name}`;
    if (subCategoryKey) {
      const subCat =
        selectedDomain.sub_categories?.[subCategoryKey];
      if (subCat?.display_name) scopeLabel += ` > ${subCat.display_name}`;
      else scopeLabel += ` > ${subCategoryKey}`;
    }
  }

  const showStop = isLoading && onStop;

  return (
    <form
      className="chat-input"
      onSubmit={handleSubmit}
      role="form"
      aria-label="Chat input"
    >
      {scopeLabel && (
        <div className="scope-label">
          <span className="scope-label-text">{scopeLabel}</span>
        </div>
      )}

      {showStop && (
        <button
          type="button"
          className="stop-generation-btn"
          onClick={onStop}
          title="Stop generation"
        >
          <span aria-hidden>⏹ Stop</span>
        </button>
      )}

      <label htmlFor="chat-input" className="chat-input-label" hidden>
        {placeholder}
      </label>
      <input
        id="chat-input"
        type="text"
        ref={inputRef}
        placeholder={placeholder || "Ask me anything..."}
        className="chat-input-field"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        disabled={isLoading}
        autoComplete="off"
      />
      <button
        type="submit"
        className="chat-input-button"
        disabled={!value.trim() || isLoading}
        aria-label="Send message"
      >
        <span aria-hidden>→</span>
      </button>
    </form>
  );
};
