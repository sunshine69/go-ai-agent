/**
 * API client for SupersonicIQ — mirrors src/frontend/spa.
 *
 * All calls resolve to a same-origin /api base by default (empty string) so the
 * Wails frontend and the SPA talk to the backend identically. Set VITE_BACKEND_URL
 * only for standalone dev (npm run dev), e.g. http://127.0.0.1:8000.
 *
 * Conversation endpoints return a bare array (matching the backend's
 * GET /api/conversations), and sends use SSE streaming at /api/messages/stream
 * via hooks/useStreaming — exactly like the SPA. Keeping this identical to the
 * SPA is deliberate: any future auth header added here can be reused verbatim.
 */

import { useCallback, useState } from "react";
import { Conversation, Domain } from "../types/api";
import { useStreaming } from "./useStreaming";

const API_BASE = (import.meta.env.VITE_BACKEND_URL as string) || "";

export function useApi() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [lastError, setLastError] = useState<string | null>(null);

  // Streaming hook, same as the SPA — same origin because API_BASE is "" by default.
  const { state: streaming, streamMessage, reset, abort } = useStreaming(API_BASE);

  const refreshConversations = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/api/conversations`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Conversation[] = await res.json();
      setConversations(Array.isArray(data) ? data : []);
    } catch (e: any) {
      setLastError(`Failed to load conversations: ${e.message}`);
    }
  }, []);

  const loadDomains = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/api/domains`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: { domains: Domain[] } = await res.json();
      setDomains(data.domains || []);
    } catch (e: any) {
      setLastError(`Failed to load domains: ${e.message}`);
    }
  }, [refreshConversations]);

  // Create a conversation and return its id (mirrors SPA handleNewConversation).
  const createConversation = useCallback(async (): Promise<string> => {
    try {
      const res = await fetch(`${API_BASE}/api/conversations`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      const id: string = (data && data.id) || "";
      setActiveId(id);
      await refreshConversations();
      return id;
    } catch (e: any) {
      setLastError(`Failed to create conversation: ${e.message}`);
      return "";
    }
  }, [refreshConversations]);

  // Load an existing conversation by id (mirrors SPA handleConversationSelect).
  const loadConversation = useCallback(
    async (id: string): Promise<any> => {
      try {
        const res = await fetch(`${API_BASE}/api/conversations/${id}`);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        const messages = (data.messages || [])
          .filter((m: any) => m.role === "user" || m.role === "assistant")
          .map((m: any) => ({
            role: m.role,
            content: m.content,
            sources: m.sources || [],
            confluence_links: m.confluence_links || [],
          }));
        setActiveId(id);
        // Return the mapped messages so AppLayout can set them into state
        // (mirrors the SPA's handleConversationSelect).
        return messages;
      } catch (e: any) {
        setLastError(`Failed to load conversation: ${e.message}`);
        return [];
      }
    },
    [refreshConversations]
  );

  // Delete a single conversation (mirrors SPA handleConversationDelete).
  const deleteConversation = useCallback(
    async (id: string): Promise<void> => {
      try {
        await fetch(`${API_BASE}/api/conversations/${id}`, {
          method: "DELETE",
        });
        if (id === activeId) setActiveId("");
        await refreshConversations();
      } catch (e: any) {
        setLastError(`Failed to delete conversation: ${e.message}`);
      }
    },
    [activeId, refreshConversations]
  );

  // Delete every conversation (mirrors SPA handleClearAllConversations).
  const clearConversations = useCallback(async (): Promise<void> => {
    try {
      await fetch(`${API_BASE}/api/conversations`, { method: "DELETE" });
      setActiveId("");
      setConversations([]);
    } catch (e: any) {
      setLastError(`Failed to clear conversations: ${e.message}`);
    }
  }, []);

  return {
    domains,
    conversations,
    activeId,
    setActiveId,
    lastError,
    // Exposed so the component can pass the current conversation id into streamMessage.
    streaming,
    streamMessage,
    reset,
    abort,
    // CRUD
    loadDomains,
    refreshConversations,
    createConversation,
    loadConversation,
    deleteConversation,
    clearConversations,
  };
}
