import React, { useState, useMemo } from "react";
import { Domain } from "../types";
import { AuthUser } from "../utils/token";
import { deleteConversations } from "../utils/api";
import { ChangePassword } from "./ChangePassword";
import { SettingsPanel } from "./SettingsPanel";
import { UserManagement } from "./UserManagement";

interface Conversation {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
}

interface SidebarProps {
  apiBaseUrl: string;
  selectedDomain: Domain | null;
  selectedSubCategory: string | null;
  conversations: Conversation[];
  activeConversationId: string;
  sidebarOpen: boolean;
  onCloseSidebar: () => void;
  onNewConversation: () => void;
  onChangeSelection: () => void;
  onSelectConversation: (id: string) => void;
  onDeleteConversation: (id: string) => void;
  onClearAllConversations: () => void;
  onQuickAction: (domain: Domain) => void;
  domains: Domain[];
  user: AuthUser | null;
  onLogout: () => Promise<void> | void;
  onClearConversation: () => void;
}

// Format an ISO/RFC3339 timestamp into a compact single-line, human-readable
// label. Falls back to "Unknown date" when the value is missing or unparyseable
// (e.g. "Mon 3 Jan 2025, 14:05" in local time).
function formatConvDateTime(iso: string): string {
  if (!iso) return "Unknown date";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "Unknown date";
  const dateStr = d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
  const timeStr = d.toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
  return `${dateStr}, ${timeStr}`;
}

export const Sidebar: React.FC<SidebarProps> = ({
  apiBaseUrl,
  selectedDomain,
  selectedSubCategory,
  conversations,
  activeConversationId,
  sidebarOpen,
  onCloseSidebar,
  onNewConversation,
  onChangeSelection,
  onSelectConversation,
  onDeleteConversation,
  onClearAllConversations,
  onQuickAction,
  domains,
  user,
  onLogout,
  onClearConversation,
}) => {
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const [showUserPanel, setShowUserPanel] = useState(false);

  // Search / sort UI for the conversation list.
  const [showAllConversations, setShowAllConversations] = useState(false);
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<"updated" | "created" | "title">("updated");
  const [selectedConvs, setSelectedConvs] = useState<Record<string, boolean>>({});
  const [isDeleting, setIsDeleting] = useState(false);
  const [deleteMsg, setDeleteMsg] = useState("");

  const currentScope = selectedDomain
    ? `${selectedDomain.icon} ${selectedDomain.display_name}${
        selectedSubCategory && selectedDomain.sub_categories?.[selectedSubCategory]
          ? ` > ${selectedDomain.sub_categories[selectedSubCategory].display_name}`
          : ""
      }`
    : "";

  // Search filtering + sort, computed each render. Search matches against the
  // title and the raw id so users can find a conv by typing part of either.
  const filteredConvs = useMemo(() => {
    const term = search.trim().toLowerCase();
    const base = term
      ? conversations.filter((c) =>
          [c.title, c.id].some((f) => f.toLowerCase().includes(term))
        )
      : [...conversations];

    if (sort === "title") {
      return base.sort((a, b) =>
        (a.title || "Untitled").localeCompare(b.title || "Untitled")
      );
    }
    // "updated" (default) and "created" both use descending (newest first),
    // matching the backend ORDER BY updated_at DESC.
    const key = sort === "created" ? "created_at" : "updated_at";
    return base.sort((a, b) => {
      const ta = new Date(a[key]).getTime();
      const tb = new Date(b[key]).getTime();
      return tb - ta;
    });
  }, [conversations, search, sort]);

  const selectedIds = useMemo(
    () => Object.keys(selectedConvs).filter((id) => selectedConvs[id]),
    [selectedConvs]
  );
  const canMultiDelete = selectedIds.length > 0;

  const toggleSelect = (id: string) => {
    setSelectedConvs((prev) => ({ ...prev, [id]: !prev[id] }));
  };
  const toggleSelectAll = () => {
    const allSelected = filteredConvs.every((c) => selectedConvs[c.id]);
    const next: Record<string, boolean> = {};
    for (const c of filteredConvs) next[c.id] = !allSelected;
    setSelectedConvs((prev) => ({ ...prev, ...next }));
  };
  const clearSelection = () => setSelectedConvs({});

  const handleMultiDelete = async () => {
    if (selectedIds.length === 0) return;
    const n = selectedIds.length;
    setDeleteMsg(`Deleting ${n} conversation${n === 1 ? "" : "s"}…`);
    setIsDeleting(true);
    try {
      await deleteConversations(apiBaseUrl, selectedIds);
      // Refresh the list and drop the deleted entries from state.
      for (const id of selectedIds) onDeleteConversation(id);
      setSelectedConvs({});
    } catch {
      // Errors are surfaced by the component; swallow here and keep the
      // selection so the user can retry.
    } finally {
      setIsDeleting(false);
      setDeleteMsg("");
    }
  };

  return (
    <aside className={`sidebar ${sidebarOpen ? "is-open" : ""}`}>
      {/* Header */}
      <div className="sidebar-header">
        <div className="sidebar-header-logo">
          <span className="sidebar-header-logo-icon">⚡</span>
          <span className="sidebar-header-logo-text">SupersonicIQ</span>
        </div>
        <button
          className="sidebar-header-close-btn"
          onClick={onCloseSidebar}
          aria-label="Close menu"
          type="button"
        >
          <svg
            className="sidebar-header-close-icon"
            viewBox="0 0 24 24"
            fill="currentColor"
          >
            <path d="M18.3 5.71L12 12.01l-6.3-6.3-1.42 1.42L10.59 13.4l-6.3 6.3 1.42 1.42L12 14.83l6.3 6.3 1.42-1.42-6.3-6.3 6.3-6.3z" />
          </svg>
        </button>
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
        {showAllConversations && (
          <>
            {/* Search + sort toolbar */}
            <div className="sidebar-conversation-controls">
              {/* Search */}
              <div className="sidebar-conversation-search">
                <svg
                  className="sidebar-conversation-search-icon"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  aria-hidden="true"
                >
                  <circle cx="11" cy="11" r="7" />
                  <path d="M21 21l-4.3-4.3" />
                </svg>
                <input
                  className="sidebar-conversation-search-input"
                  type="text"
                  placeholder="Search conversations…"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  aria-label="Search conversations"
                />
                {search && (
                  <button
                    className="sidebar-conversation-search-clear"
                    onClick={() => setSearch("")}
                    aria-label="Clear search"
                    type="button"
                  >
                    ✕
                  </button>
                )}
              </div>

              {/* Sort */}
              <div className="sidebar-conversation-sort">
                <label
                  className="sidebar-conversation-sort-label"
                  htmlFor="conv-sort-select"
                >
                  Sort
                </label>
                <select
                  id="conv-sort-select"
                  className="sidebar-conversation-sort-select"
                  value={sort}
                  onChange={(e) =>
                    setSort(e.target.value as "updated" | "created" | "title")
                  }
                >
                  <option value="updated">Last updated</option>
                  <option value="created">Created</option>
                  <option value="title">Name</option>
                </select>
              </div>
            </div>

            {/* Selection toolbar + list (multi-select delete + counts) */}
            <div className="sidebar-conversation-list-header">
              <label className="sidebar-conversation-select-all">
                <input
                  type="checkbox"
                  checked={filteredConvs.length > 0 && filteredConvs.every((c) => selectedConvs[c.id])}
                  onChange={toggleSelectAll}
                  aria-label="Select all shown"
                />
              </label>
              <div className="sidebar-conversation-list-count">
                {filteredConvs.length} of {conversations.length}
                {search ? " matched" : ""}
              </div>
              {canMultiDelete && !isDeleting && (
                <button
                  className="sidebar-conversation-multi-delete"
                  onClick={handleMultiDelete}
                  title={`Delete ${selectedIds.length} selected conversation${selectedIds.length === 1 ? "" : "s"}`}
                >
                  Delete ({selectedIds.length})
                </button>
              )}
              {isDeleting && <span className="sidebar-conversation-deleting">{deleteMsg}</span>}
              {!canMultiDelete && !isDeleting && selectedIds.length > 0 && (
                <button
                  className="sidebar-conversation-clear-selection"
                  onClick={clearSelection}
                >
                  Clear selection
                </button>
              )}
            </div>

            {filteredConvs.length === 0 && (
              <div className="sidebar-conversations-empty">
                {conversations.length === 0
                  ? "No conversations yet"
                  : "No conversations match your search"}
              </div>
            )}

            <div className="sidebar-conversations">
              {filteredConvs.map((conv) => {
                const isActive = conv.id === activeConversationId;
                const isSel = !!selectedConvs[conv.id];
                const dateTime = formatConvDateTime(
                  sort === "created" ? conv.created_at : conv.updated_at
                );
                return (
                  <div
                    key={conv.id}
                    className={`sidebar-conversation${isActive ? " active" : ""}${isSel ? " selected" : ""}`}
                  >
                    {/* Select checkbox */}
                    <label className="sidebar-conversation-select" onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        checked={isSel}
                        onChange={() => toggleSelect(conv.id)}
                        aria-label={`Select ${conv.title || "conversation"}`}
                      />
                    </label>

                    {/* Body: click to open (also clears selection if in multi-select mode) */}
                    <button
                      className="sidebar-conversation-body"
                      onClick={() => {
                        if (selectedIds.length > 0) clearSelection();
                        onSelectConversation(conv.id);
                      }}
                      type="button"
                    >
                      <span className="sidebar-conversation-text">
                        {conv.title || "Untitled"}
                      </span>
                      <span className="sidebar-conversation-datetime" title={dateTime}>
                        {dateTime}
                      </span>
                    </button>

                    {/* Single-delete button (only when not in multi-select mode) */}
                    {!canMultiDelete && (
                      <button
                        className="sidebar-conversation-delete"
                        onClick={(e) => {
                          e.stopPropagation();
                          setDeleteConfirm(conv.id);
                        }}
                        aria-label={`Delete ${conv.title || "conversation"}`}
                        type="button"
                      >
                        🗑️
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
          </>
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

      {/* User menu toggle */}
      {user && (
        <div className="sidebar-user-menu-toggle">
          <button
            className="sidebar-user-menu-toggle-btn"
            onClick={() => setShowUserPanel((v) => !v)}
          >
            {showUserPanel ? "▲" : "▼"} User Settings
          </button>
        </div>
      )}

      {/* User settings panel (change password for all; admin panel for admins) */}
      {showUserPanel && user && (
        <div className="sidebar-user-panel">
          <SettingsPanel apiBaseUrl={apiBaseUrl} onClearConversation={onClearConversation} />
          <ChangePassword apiBaseUrl={apiBaseUrl} />
          {user.is_admin && <UserManagement apiBaseUrl={apiBaseUrl} />}
        </div>
      )}

      {/* Footer: user identity + logout */}
      {user && (
        <div className="sidebar-footer">
          <div className="sidebar-footer-info">
            <span className="sidebar-footer-icon">👤</span>
            <span className="sidebar-footer-name">{user.login_name}</span>
          </div>
          <button className="sidebar-footer-logout" onClick={() => onLogout()}>
            Sign out
          </button>
        </div>
      )}
    </aside>
  );
};
