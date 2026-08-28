import React from "react";
import { Domain } from "../types/api";

interface SidebarProps {
  domains: Domain[];
  onNewConversation: () => void;
  onQuickAction: (domain: Domain) => void;
  onConversationSelect: (id: string) => void;
  currentDomain: Domain | null;
  currentSubCategory?: { display_name: string; icon?: string } | null;
  conversations: Array<{ id: string; title: string }>;
  lastQuestion: string | null;
}

export const Sidebar: React.FC<SidebarProps> = ({
  domains,
  onNewConversation,
  onQuickAction,
  onConversationSelect,
  currentDomain,
  currentSubCategory,
  conversations,
  lastQuestion,
}) => {
  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <h1>SupersonicIQ</h1>
      </div>

      <div className="sidebar-section">
        <button
          className="sidebar-button new-conversation"
          onClick={onNewConversation}
        >
          🆕 New Conversation
        </button>
      </div>

      <div className="sidebar-divider" aria-hidden="true" />

      {/* Current scope indicator */}
      {currentDomain && (
        <div className="sidebar-current">
          <h2 className="sidebar-current-title">Current Scope</h2>
          <div className="sidebar-current-label">
            <span className="label-icon">{currentDomain.icon}</span>
            <span className="label-text">
              {currentDomain.display_name}
              {currentSubCategory && (
                <>
                  {" > "}
                  <span className="sub-category-label">
                    {currentSubCategory.icon || ''} {currentSubCategory.display_name}
                  </span>
                </>
              )}
            </span>
          </div>
          <button
            className="sidebar-button change-selection"
            onClick={() => {
              onQuickAction(domains[0]); // reset to first domain
            }}
          >
            Change selection
          </button>
        </div>
      )}

      <div className="sidebar-divider" aria-hidden="true" />

      {/* Quick Actions */}
      <div className="sidebar-section quick-actions">
        <h2 className="quick-actions-title">Quick Actions</h2>
        <div className="quick-actions-list">
          {domains.map((domain) => (
            <button
              key={domain.key}
              className="quick-action-button"
              onClick={() => onQuickAction(domain)}
            >
              <span className="action-icon">{domain.icon}</span>
              <span className="action-text">{domain.display_name}</span>
            </button>
          ))}
        </div>
      </div>

      <div className="sidebar-divider" aria-hidden="true" />

      {/* Conversations list */}
      <div className="sidebar-section conversations">
        <h2 className="conversations-title">Conversations</h2>
        <div className="conversations-list">
          {conversations.map((conv) => (
            <button
              key={conv.id}
              onClick={() => onConversationSelect(conv.id)}
            >
              {conv.title || "Untitled"}
            </button>
          ))}
          {conversations.length === 0 && (
            <p className="conversations-empty">No conversations yet</p>
          )}
        </div>
      </div>

      {/* Last question prompt */}
      {lastQuestion && (
        <div className="sidebar-last-question">
          <h3>Last Question</h3>
          <p>
            <strong>"{lastQuestion}"</strong> — tap to continue
          </p>
        </div>
      )}
    </aside>
  );
};
