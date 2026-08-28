/**
 * API hooks for SupersonicIQ — fetch domains, send messages, list conversations
 */

import { useCallback, useState } from "react";
import { Domain, MessageResponse, Conversation } from "../types/api";

const BASE_URL = import.meta.env.VITE_BACKEND_URL || "http://localhost:8000";

const API_BASE = `${BASE_URL}/api`;

export function useApi() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [lastError, setLastError] = useState<string | null>(null);

  const loadDomains = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/domains`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: { domains: Domain[] } = await res.json();
      setDomains(data.domains || []);
    } catch (e: any) {
      setLastError(`Failed to load domains: ${e.message}`);
    }
  }, []);

  const sendMessage = useCallback(
    async (
      prompt: string,
      conversationId?: string,
      domain?: string,
      subCategory?: string
    ): Promise<MessageResponse | null> => {
      try {
        const res = await fetch(`${API_BASE}/messages`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ message: prompt, conversation_id: conversationId, domain, sub_category: subCategory }),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return await res.json() as MessageResponse;
      } catch (e: any) {
        setLastError(`Failed to send message: ${e.message}`);
        return null;
      }
    },
    []
  );

  const loadConversations = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/conversations`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: { conversations: Conversation[] } = await res.json();
      setConversations(data.conversations || []);
    } catch (e: any) {
      setLastError(`Failed to load conversations: ${e.message}`);
    }
  }, []);

  return {
    domains,
    conversations,
    lastError,
    loadDomains,
    sendMessage,
    loadConversations,
  };
}
