import React, { useState } from "react";
import { Domain } from "../types";

interface SidebarProps {
  selectedDomain: Domain | null;
  selectedSubCategory: string | null;
  conversations: { id: string; title: string }[];
  activeConversationId: string;
  onNewConversation: () => void;
  onChangeSelection: () => void;
  onSelectConversation: (id: string) => void;
  onDeleteConversation: (id: string) => void;
  onClearAllConversations: () => void;
  onQuickAction: (domain: Domain) => void;
  domains: Domain[];
}

export const Sidebar: React.FC<SidebarProps> = ({
  selectedDomain,
  selectedSubCategory,
  conversations,
  activeConversationId,
  onNewConversation,
  onChangeSelection,
  onSelectConversation,
  onDeleteConversation,
  onClearAllConversations,
  onQuickAction,
  domains,
}) => {
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const [showAllConversations, setShowAllConversations] = useState(false);

  const currentScope = selectedDomain
    ? `${selectedDomain.icon} ${selectedDomain.display_name}${
        selectedSubCategory && selectedDomain.sub_categories?.[selectedSubCategory]
          ? ` > ${selectedDomain.sub_categories[selectedSubCategory].display_name}`
          : ""
      }`
    : "";

  return (
    <aside className="sidebar">
      {/* Header */}
      <div className="sidebar-header">
        <div className="sidebar-header-logo">
          <span className="sidebar-header-logo-icon">⚡</span>
          <span className="sidebar-header-logo-text">SupersonicIQ</span>
        </div>
        <button className="sidebar-header-new-btn" onClick={onNewConversation}>
          <svg
            className="sidebar-header-new-icon"
            viewBox="0 0 24 24"
            fill="currentColor"
          >
            <path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6z" />
          </svg>
        </button>
      </div>

      {/* Current scope */}
      {selectedDomain && (
        <div className="sidebar-current-scope">
          <div className="sidebar-current-scope-content">
            <span className="sidebar-current-scope-label">{currentScope}</span>
            <button
              className="sidebar-current-scope-change"
              onClick={onChangeSelection}
            >
              ✕
            </button>
          </div>
        </div>
      )}

      {/* Quick Actions */}
      {domains.length > 0 && (
        <div className="sidebar-section">
          <div className="sidebar-section-title">Quick Actions</div>
          <div className="sidebar-quick-actions">
            {domains.map((domain) => (
              <button
                key={domain.key}
                className="sidebar-quick-action"
                onClick={() => onQuickAction(domain)}
              >
                <span className="sidebar-quick-action-icon">{domain.icon}</span>
                <span className="sidebar-quick-action-label">
                  {domain.display_name}
                </span>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Conversations */}
      <div className="sidebar-section">
        <div className="sidebar-section-header">
          <div className="sidebar-section-title">Conversations</div>
          {conversations.length > 0 && (
            <button
              className="sidebar-section-toggle"
              onClick={() => setShowAllConversations(!showAllConversations)}
            >
              {showAllConversations
                ? "Hide"
                : `Show all (${conversations.length})`}
            </button>
          )}
        </div>
        {showAllConversations ? (
          <div className="sidebar-conversations">
            {conversations.map((conv) => (
              <div
                key={conv.id}
                className={`sidebar-conversation ${
                  conv.id === activeConversationId ? "active" : ""
                }`}
                onClick={() => onSelectConversation(conv.id)}
              >
                <span className="sidebar-conversation-text">
                  {conv.title || "Untitled"}
                </span>
                {conv.id === activeConversationId && (
                  <button
                    className="sidebar-conversation-delete"
                    onClick={(e) => {
                      e.stopPropagation();
                      setDeleteConfirm(conv.id);
                    }}
                  >
                    🗑️
                  </button>
                )}
              </div>
            ))}
          </div>
        ) : (
          conversations.length > 0 && (
            <button
              className="sidebar-conversation sidebar-conversation-active"
              onClick={() =>
                onSelectConversation(activeConversationId || conversations[0].id)
              }
            >
              <span className="sidebar-conversation-text">
                {conversations.find((c) => c.id === activeConversationId)
                  ?.title || "Active"}
              </span>
            </button>
          )
        )}
        {conversations.length === 0 && (
          <div className="sidebar-conversations-empty">
            No conversations yet
          </div>
        )}
      </div>

      {/* Delete confirmation */}
      {deleteConfirm && (
        <div className="sidebar-delete-confirm">
          <p>Are you sure you want to delete this conversation?</p>
          <div className="sidebar-delete-confirm-actions">
            <button
              className="sidebar-delete-confirm-yes"
              onClick={() => {
                onDeleteConversation(deleteConfirm);
                setDeleteConfirm(null);
              }}
            >
              Delete
            </button>
            <button
              className="sidebar-delete-confirm-no"
              onClick={() => setDeleteConfirm(null)}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Clear all */}
      {conversations.length > 1 && (
        <div className="sidebar-clear-all">
          <button className="sidebar-clear-all-btn" onClick={onClearAllConversations}>
            Clear All Conversations
          </button>
        </div>
      )}
    </aside>
  );
};
