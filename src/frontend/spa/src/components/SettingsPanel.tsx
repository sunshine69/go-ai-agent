import React, { useEffect, useState } from "react";
import { getSettings, setSetting, SettingsResponse } from "../utils/api";

interface SettingsPanelProps {
  apiBaseUrl: string;
  /** Clears the active conversation + input + streaming state (used by /clear). */
  onClearConversation: () => void;
}

// Formats a token budget as a human-readable string with thousands separators.
function fmt(n: number): string {
  if (!Number.isFinite(n)) return "0";
  return n.toLocaleString();
}

export const SettingsPanel: React.FC<SettingsPanelProps> = ({
  apiBaseUrl,
  onClearConversation,
}) => {
  const [settings, setSettings] = useState<SettingsResponse | null>(null);
  const [input, setInput] = useState("");
  const [status, setStatus] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  // Load the current settings once on mount.
  useEffect(() => {
    getSettings(apiBaseUrl).then(setSettings).catch(() => undefined);
  }, [apiBaseUrl]);

  const flash = (type: "ok" | "error", text: string) => {
    setStatus({ type, text });
  };

  // Handle a /ctx value: validate it's a positive integer, persist it, then
  // refresh the display. /ctx without an argument shows the current value.
  const handleCtxSubmit = async (rawValue: string) => {
    const value = rawValue.trim();
    if (value === "") {
      // /ctx with no argument just reports the current budget.
      flash("ok", `Context limit: ${fmt(settings?.context_limit ?? 50000)} tokens`);
      return;
    }
    // Backend requires an integer >= 1.
    if (!/^\d+$/.test(value)) {
      flash("error", "Please enter a whole number greater than 0.");
      return;
    }
    const n = Number(value);
    if (n < 1) {
      flash("error", "Please enter a whole number greater than 0.");
      return;
    }
    setBusy(true);
    setStatus(null);
    try {
      const updated = await setSetting(apiBaseUrl, { key: "ctxLimit", value });
      setSettings(updated);
      setInput("");
      flash("ok", `Context limit set to ${fmt(updated.context_limit)} tokens.`);
    } catch {
      flash("error", "Failed to set context limit. Please try again.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="settings-panel">
      <div className="settings-panel-title">Settings</div>
      <div className="settings-panel-hint">
        Use /ctx &lt;number&gt; to set your per-user context size (in tokens)
      </div>

      <div className="settings-panel-field">
        <label className="settings-panel-label" htmlFor="ctx-input">
          Context limit
        </label>
        <div className="settings-panel-input-row">
          <input
            id="ctx-input"
            type="text"
            className="settings-panel-input"
            value={input}
            placeholder="e.g. 100000"
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                if (!busy) handleCtxSubmit(input);
              }
            }}
            aria-label="Context limit in tokens"
          />
          <button
            type="button"
            className="settings-panel-save"
            disabled={busy}
            onClick={() => handleCtxSubmit(input)}
          >
            {busy ? "Saving…" : "Set"}
          </button>
        </div>
        {settings && (
          <div className="settings-panel-current">
            Current: {fmt(settings.context_limit)} tokens
          </div>
        )}
      </div>

      <div className="settings-panel-status">
        <div className="settings-panel-usage">
          <span className="settings-panel-usage-label">Usage:</span>
          <code className="settings-panel-usage-cmd">/ctx {fmt(settings?.context_limit ?? 50000)}</code>
          <span className="settings-panel-usage-note">
            (messages over this limit are condensed to stay within it)
          </span>
        </div>
        <button
          type="button"
          className="settings-panel-clear"
          onClick={onClearConversation}
        >
          /clear
        </button>
      </div>

      {status && (
        <div className={`settings-panel-message ${status.type}`}>
          {status.text}
        </div>
      )}
    </div>
  );
};
