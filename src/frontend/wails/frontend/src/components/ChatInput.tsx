import React, { useState, useRef, useEffect } from "react";

interface ChatInputProps {
  placeholder: string;
  onSend: (message: string) => void;
  disabled: boolean;
}

export const ChatInput: React.FC<ChatInputProps> = ({
  placeholder,
  onSend,
  disabled,
}) => {
  const [value, setValue] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input when placeholder changes (scope change)
  useEffect(() => {
    inputRef.current?.focus();
  }, [placeholder]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = value.trim();
    if (!trimmed) return;
    onSend(trimmed);
    setValue("");
  };

  return (
    <form className="chat-input" onSubmit={handleSubmit}>
      <label htmlFor="chat-input" className="chat-input-label" hidden>
        {placeholder}
      </label>
      <input
        id="chat-input"
        type="text"
        ref={inputRef}
        placeholder={placeholder}
        className="chat-input-field"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        disabled={disabled}
        autoComplete="off"
      />
      <button
        type="submit"
        className="chat-input-button"
        disabled={disabled}
        aria-label="Send message"
      >
        <span aria-hidden="true">→</span>
      </button>
    </form>
  );
};
