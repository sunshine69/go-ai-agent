/**
 * Streaming hook for SSE consumption from the Go backend.
 *
 * The Go backend at /api/messages/stream (proxyLLMStream) sends:
 *   event: context\nid: <ts>\ndata: {"conversation_id":"...","sources":[...]}\n\n
 *   event: message\ndata: {"choices":[{"delta":{"content":"<token>"}}]}\n\n
 *   event: done\ndata: {"conversation_id":"...","sources":[...],"citations":[...]}\n\n
 *
 * The Go backend at /api/chat/stream (handleStreamChat) sends:
 *   event: context\ndata: {"conversation_id":"...","sources":[...]}\n\n
 *   event: message\ndata: {"content":"<token>"}\n\n
 *   event: done\ndata: {"conversation_id":"...","sources":[...],"citations":[...]}\n\n
 */

import { useCallback, useRef, useState } from "react";
import type { ChatRequest, ConfluenceLink } from "../types";

export interface StreamingState {
  conversationId: string;
  sources: string[];
  citations: ConfluenceLink[];
  currentChunk: string;
  fullAnswer: string;
  error: string | null;
  isStreaming: boolean;
}

export interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  // UI-only fields for synthetic messages (like intro messages) and errors
  synthetic?: boolean;
  error?: string;
  // Source fields for rendering RAG sources
  sources?: string[];
  confluence_links?: ConfluenceLink[];
}

const initialStreamingState: StreamingState = {
  conversationId: "",
  sources: [],
  citations: [],
  currentChunk: "",
  fullAnswer: "",
  error: null,
  isStreaming: false,
};

export function useStreaming(apiBaseUrl: string) {
  const [state, setState] = useState<StreamingState>(initialStreamingState);

  // Ref for the abort controller - stable across renders
  const abortControllerRef = useRef<AbortController | null>(null);

  // Ref for mutable state during streaming - stable across renders
  const streamingStateRef = useRef<StreamingState>({ ...initialStreamingState });

  const reset = useCallback(() => {
    abortControllerRef.current?.abort();
    setState(initialStreamingState);
    streamingStateRef.current = { ...initialStreamingState };
  }, []);

  const abort = useCallback(() => {
    abortControllerRef.current?.abort();
    setState(prev => ({ ...prev, isStreaming: false }));
    streamingStateRef.current = { ...streamingStateRef.current, isStreaming: false };
  }, []);

  const streamMessage = useCallback(
    async (payload: ChatRequest): Promise<StreamingState> => {
      // Abort any previous streaming
      abortControllerRef.current?.abort();
      const controller = new AbortController();
      abortControllerRef.current = controller;

      // Reset streaming state
      const freshState = { ...initialStreamingState, isStreaming: true };
      setState(freshState);
      streamingStateRef.current = { ...freshState };

      try {
        const response = await fetch(
          `${apiBaseUrl}/api/messages/stream`,
          {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              Accept: "text/event-stream",
            },
            body: JSON.stringify(payload),
            signal: controller.signal,
          }
        );

        if (!response.ok) {
          const text = await response.text();
          const errorState = { ...streamingStateRef.current, isStreaming: false, error: `HTTP ${response.status}: ${text}` };
          setState(errorState);
          streamingStateRef.current = errorState;
          return errorState;
        }

        const reader = response.body?.getReader();
        if (!reader) {
          const errorState = { ...streamingStateRef.current, isStreaming: false, error: "No response body" };
          setState(errorState);
          streamingStateRef.current = errorState;
          return errorState;
        }

        const decoder = new TextDecoder();
        let buffer = "";
        let currentEvent = "";
        let totalReceived = 0;

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          const textChunk = decoder.decode(value, { stream: true });
          totalReceived += textChunk.length;

          buffer += textChunk;
          const lines = buffer.split("\n");
          buffer = lines.pop() || "";

          for (const line of lines) {
            const trimmed = line.trim();
            if (!trimmed) continue;

            if (trimmed.startsWith("event: ")) {
              currentEvent = trimmed.slice(7).trim();
            } else if (trimmed.startsWith("id: ")) {
              // Event ID — ignored
            } else if (trimmed.startsWith("data: ")) {
              const dataStr = trimmed.slice(6);

              if (currentEvent === "context") {
                try {
                  const evt = JSON.parse(dataStr);
                  streamingStateRef.current = {
                    ...streamingStateRef.current,
                    conversationId: evt.conversation_id,
                    sources: evt.sources || [],
                  };
                  setState({ ...streamingStateRef.current });
                } catch {
                  // ignore
                }
              } else if (currentEvent === "message") {
                try {
                  const parsed = JSON.parse(dataStr);
                  // Try /api/chat/stream format first: {"content": "..."}
                  let chunk = "";
                  if (parsed.content !== undefined) {
                    chunk = parsed.content;
                  } else if (
                    parsed.choices &&
                    parsed.choices[0]?.delta?.content !== undefined
                  ) {
                    // Try /api/messages/stream format: {"choices":[{"delta":{"content":"..."}}]}
                    chunk = parsed.choices[0].delta.content;
                  }
                  if (chunk) {
                    const newAnswer = streamingStateRef.current.fullAnswer + chunk;
                    streamingStateRef.current = {
                      ...streamingStateRef.current,
                      currentChunk: chunk,
                      fullAnswer: newAnswer,
                    };
                    setState({ ...streamingStateRef.current });
                  }
                } catch {
                  // Raw text — might be a non-JSON chunk
                  if (dataStr && dataStr !== "[DONE]") {
                    const newAnswer = streamingStateRef.current.fullAnswer + dataStr;
                    streamingStateRef.current = {
                      ...streamingStateRef.current,
                      currentChunk: dataStr,
                      fullAnswer: newAnswer,
                    };
                    setState({ ...streamingStateRef.current });
                  }
                }
              } else if (currentEvent === "done") {
                try {
                  const evt = JSON.parse(dataStr);
                  streamingStateRef.current = {
                    ...streamingStateRef.current,
                    conversationId: evt.conversation_id,
                    sources: evt.sources || streamingStateRef.current.sources,
                    citations: evt.citations || evt.confluence_links || [],
                    currentChunk: "",
                    isStreaming: false,
                    error: null,
                  };
                  setState({ ...streamingStateRef.current });
                } catch {
                  streamingStateRef.current = {
                    ...streamingStateRef.current,
                    isStreaming: false,
                    currentChunk: "",
                  };
                  setState({ ...streamingStateRef.current });
                }
              } else if (currentEvent === "error") {
                try {
                  const evt = JSON.parse(dataStr);
                  streamingStateRef.current = {
                    ...streamingStateRef.current,
                    isStreaming: false,
                    error: evt.error,
                  };
                  setState({ ...streamingStateRef.current });
                } catch {
                  streamingStateRef.current = {
                    ...streamingStateRef.current,
                    isStreaming: false,
                    error: dataStr,
                  };
                  setState({ ...streamingStateRef.current });
                }
              }

              currentEvent = "";
            }
          }
        }

        // If we ended without a done event, clean up
        streamingStateRef.current = {
          ...streamingStateRef.current,
          isStreaming: false,
          currentChunk: "",
        };
        setState({ ...streamingStateRef.current });

        return streamingStateRef.current;
      } catch (err: unknown) {
        const error = err as Error;
        if (error.name !== "AbortError") {
          streamingStateRef.current = {
            ...streamingStateRef.current,
            isStreaming: false,
            error: error.message,
          };
          setState({ ...streamingStateRef.current });
        }
        return streamingStateRef.current;
      } finally {
        abortControllerRef.current = null;
      }
    },
    [apiBaseUrl]
  );

  return { state, streamMessage, reset, abort };
}
