/**
 * API response types for SupersonicIQ
 */

export interface Domain {
  key: string;
  display_name: string;
  icon: string;
  sub_categories: {
    [key: string]: {
      display_name: string;
      icon: string;
    };
  };
}

export interface SubCategoryInfo {
  display_name: string;
  icon: string;
}

export interface MessageRequest {
  message: string;
  conversation_id?: string;
  domain?: string;
  sub_category?: string;
}

export interface MessageResponse {
  conversation_id: string | null;
  answer: string;
  sources: string[];
  confluence_links: Array<{ title: string; url: string }>;
}

export interface Conversation {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  messages?: Array<{ role: string; content: string }>;
}

export interface Source {
  title: string;
  url?: string;
  page?: number;
}

export interface ConfluenceLink {
  title: string;
  url: string;
}
