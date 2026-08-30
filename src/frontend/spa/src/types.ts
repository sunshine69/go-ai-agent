/**
 * API types for SupersonicIQ
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

// SSE event types for streaming
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
