# Streaming Chat Implementation Guide for SuperSonicIQ

## Overview

This implementation adds support for streaming chat responses using Server-Sent Events (SSE) to the SuperSonicAI backend. The endpoint `/api/chat/stream` accepts conversation IDs from the frontend, builds context (MCP + RAG), sends requests to an OpenAI-compatible AI server with stream options enabled, and relays responses back as SSE events.

## Architecture

### Core Components

1. **Frontend Client**
   - Initiates streaming chat sessions
   - Sends messages with optional conversation ID
   - Receives real-time token streams via SSE
   - Manages UI state during streaming (loading indicators, etc.)

2. **Backend Handler** (`messages_stream.go`)
   - `handleStreamChat`: Main endpoint handler for POST /api/chat/stream
   - Context Builder Integration: Uses `ContextBuilder` to fetch MCP + RAG context
   - AI Server Communication: Connects with OpenAI-compatible API using streaming mode

3. **AI Service**
   - Accepts stream=true parameter in requests
   - Returns token-by-token responses via SSE events

4. **Conversation Store**
   - Persists chat history and metadata
   - Maintains conversation state between sessions

### Data Flow

```
Frontend → Backend Handler (SSE) → Context Builder → AI Server → SSE Events → Frontend
                              ↓              ↓                   ↑          ↓
                    Conversation Store    Knowledge Base     Token Stream  UI Update
```

## Implementation Details

### Endpoint Definition

```go
// POST /api/chat/stream with OpenAI-compatible streaming.
func (m *messageStreamHandler) handleStreamChat(w http.ResponseWriter, r *http.Request)
```

### Request Structure

```json
{
  "message": "string",
  "conversation_id": "optional string", 
  "domain": "optional string",
  "sub_category": "optional string"
}
```

### SSE Event Types

| Event Type | Description | Data Format |
|------------|-------------|-------------|
| `context` | Initial context info and conversation ID | `{conversation_id, sources}` |
| `message` | Token chunk from AI response | `{"content": "token"}` |
| `done` | Final message with full response and citations | `{conversation_id, message, sources, citations}` |
| `error` | Error information | `{"error": "description"}` |

### Key Features

1. **Real-time Response Streaming**
   - Token-by-token delivery via SSE
   - Immediate user feedback during AI generation

2. **Context-Aware Responses**
   - Automatic MCP + RAG context building
   - Domain-specific filtering when provided
   - Source attribution for knowledge base references

3. **Conversation Persistence**
   - New or existing conversation handling
   - User/assistant message tracking
   - Conversation title auto-generation from first user message

4. **Error Handling**
   - Graceful failure during streaming
   - Partial response preservation on error
   - Detailed error messages in SSE format

## Frontend Integration

### Client Implementation Example

```javascript
class StreamingChatClient {
  async chat(message, conversationId = null) {
    const eventSource = new EventSource(
      `/api/chat/stream?message=${encodeURIComponent(message)}${conversationId ? `&conversation_id=${conversationId}` : ''}`,
      { headers: { 'Content-Type': 'application/json' } }
    );

    return new Promise((resolve, reject) => {
      let fullResponse = '';
      
      eventSource.onmessage = (event) => {
        const data = JSON.parse(event.data);
        
        switch (event.type) {
          case 'context':
            this.conversationId = data.conversation_id;
            break;
            
          case 'message':
            // Update UI with token
            fullResponse += data.content;
            break;
            
          case 'done':
            eventSource.close();
            resolve({
              conversationId: data.conversation_id,
              message: data.message,
              sources: data.sources,
              citations: data.citations
            });
            break;
            
          case 'error':
            eventSource.close();
            reject(new Error(data.error));
            break;
        }
      };

      eventSource.onerror = (err) => {
        eventSource.close();
        reject(err);
      };
    });
  }
}
```

### UI Considerations

1. **Loading States**
   - Show "AI is thinking" indicator during initial context building
   - Display typing cursor or progress bar during streaming

2. **Error Recovery**
   - Retry mechanism for failed requests
   - Fallback to non-streaming mode if SSE fails

3. **Citation Handling**
   - Inline citation display with source links
   - Expandable reference list at end of responses

## Testing Strategy

### Unit Tests

- Context building integration
- Conversation ID handling (new/existing)
- Error scenario simulation
- Source attribution validation

### Integration Tests

1. **End-to-End Flow**
   ```bash
   # Start backend with test configuration
   CONVERSATIONS_STORE_PATH=/tmp/test_conversations.json \
   go run main.go
   
   # Test streaming endpoint
   curl -X POST http://localhost:8000/api/chat/stream \
     -H "Content-Type: application/json" \
     -d '{"message": "Test query", "conversation_id": null}'
   ```

2. **Performance Metrics**
   - Time to first token (TTF1)
   - End-to-end response time
   - Memory usage during streaming

### Manual Testing Checklist

- [ ] New conversation creation works correctly
- [ ] Existing conversation ID is properly handled
- [ ] Context building returns expected results
- [ ] Streaming tokens arrive in correct order
- [ ] Error scenarios are properly caught and reported
- [ ] Sources/citations are accurately attributed
- [ ] Conversation persistence across sessions

## Deployment Considerations

### Server Configuration

1. **Nginx/Gateway Settings**
   ```nginx
   location /api/chat/stream {
     proxy_pass http://backend;
     
     # Disable buffering to enable streaming
     proxy_buffering off;
     proxy_cache off;
     
     # Extended timeouts for long-running generation
     proxy_connect_timeout 300s;
     proxy_send_timeout 300s;
     proxy_read_timeout 300s;
   }
   ```

2. **CORS Headers**
   ```go
   w.Header().Set("Access-Control-Allow-Origin", "*")
   w.Header().Set("Access-Control-Expose-Headers", "Content-Type, X-Accel-Buffering")
   ```

### Monitoring

1. **Metrics to Track**
   - Streaming request rate per endpoint
   - Average token delivery latency
   - Error rates by event type
   - Memory usage during long sessions

2. **Alerting Thresholds**
   > 5 seconds for first token → investigate context building
   > > 30% error rate on done events → check AI service health
   
## Future Enhancements

1. **Advanced Streaming Features**
   - Support for structured output streaming (JSON mode)
   - Multi-turn conversation state preservation
   - Real-time source highlighting during response generation

2. **Performance Optimizations**
   - Context caching between related queries
   - Parallel RAG retrieval from multiple sources
   - Pre-computed domain-specific prompts

3. **User Experience Improvements**
   - Streaming progress indicators (estimated completion time)
   - Response editing/feedback collection during streaming
   - Multi-modal response streaming (text + images)

## References

- [Server-Sent Events Specification](https://html.spec.whatwg.org/multipage/server-sent-events.html)
- OpenAI API: [Streaming Responses](https://platform.openai.com/docs/guides/text-generation/streaming)
- SSE Client Libraries:
  - JavaScript: `EventSource` (native browser API)
  - Python: `requests` with stream parameter
  - Go: `net/http` client with chunked transfer encoding

---
*Generated for SuperSonicAI Project v1.0*