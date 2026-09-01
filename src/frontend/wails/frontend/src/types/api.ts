/**
 * API + streaming types for SupersonicIQ.
 *
 * These mirror src/frontend/spa/src/types.ts exactly so the Wails frontend and
 * the SPA consume the backend the same way (same-origin /api, SSE streaming at
 * /api/messages/stream). Handy when adding authentication later: both apps will
 * build identical requests, so a shared auth header is easy to bolt on.
 */

export interface SubCategory {
  display_name: string;
  icon?: string;
  keywords?: string[];
}

export interface Domain {
  key: string;
  display_name: string;
  icon: string;
  sub_categories?: Record<string, SubCategory>;
}

export interface Conversation {
  id: string;
  title: string;
  messages?: Message[];
}

export interface Message {
  role: "user" | "assistant";
  content: string;
  conversation_id?: string;
  rag_sources?: string[];
  confluence_links?: ConfluenceLink[];
}

export interface ConfluenceLink {
  title: string;
  url: string;
}

export interface ChatRequest {
  message: string;
  conversation_id?: string;
  history?: ChatMessage[];
  domain?: string;
  sub_category?: string;
}

export interface ChatMessage {
  role: "user" | "assistant";
  content: string;
}

// Event types for streaming responses
export interface ContextEvent {
  conversation_id: string;
  sources: string[];
}

export interface DoneEvent {
  conversation_id: string;
  sources: string[];
  citations: ConfluenceLink[];
}

export interface ErrorEvent {
  error: string;
}
