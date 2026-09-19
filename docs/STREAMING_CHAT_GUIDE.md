# Streaming Chat Implementation for SuperSonicAI

## Overview

This document describes the implementation of streaming chat functionality in the SuperSonicAI backend, enabling real-time responses from AI models via Server-Sent Events (SSE).

## Core Components

### 1. Endpoint: `/api/chat/stream`

- **Method**: POST
- **Content-Type**: application/json
- **Response Type**: text/event-stream (Server-Sent Events)
- **Purpose**: Accepts conversation IDs and messages, builds context using MCP + RAG, sends to AI server with streaming enabled

### 2. Request Format

```json
{
  "message": "string",
  "conversation_id": "optional string", 
  "domain": "optional string",
  "sub_category": "optional string"
}
```

### 3. SSE Event Types

| Event | Description | Data |
|-------|-------------|------|
| `context` | Initial context and conversation ID | `{conversation_id, sources}` |
| `message` | Token chunk from AI response | `{"content": "token"}` |
| `done` | Final message with full response | `{conversation_id, message, sources, citations}` |
| `error` | Error information | `{"error": "description"}` |

## Implementation Details

### Backend Handler (`messages_stream.go`)

The handler implements the streaming endpoint with these key features:

1. **Context Building**: Uses ContextBuilder to fetch MCP + RAG context
2. **Conversation Management**: Handles new or existing conversations
3. **Streaming AI Communication**: Connects to OpenAI-compatible API with stream=true
4. **Error Handling**: Graceful failure recovery and partial response preservation

### Key Functions

```go
// Main handler for streaming chat requests
func (m *messageStreamHandler) handleStreamChat(w http.ResponseWriter, r *http.Request)

// SSE event helpers
func formatSourcesForSSE(sources []string) string
func formatCitationsForSSE(citations []confluenceLink) string
func escapeSSE(text string) string

// Request structure
type streamChatRequest struct {
    Message        string            `json:"message"`
    ConversationID *string           `json:"conversation_id,omitempty"`
    Domain         string            `json:"domain,omitempty"`
    SubCategory    string            `json:"sub_category,omitempty"`
    History        []llm.ChatMessage `json:"history,omitempty"`
}
```

## Frontend Integration

### JavaScript Implementation Example

```javascript
class StreamingChatClient {
  async chat(message, conversationId = null) {
    // Create SSE connection with JSON payload
    const response = await fetch('/api/chat/stream', {
      method: 'POST',
      headers: { 
        'Content-Type': 'application/json' 
      },
      body: JSON.stringify({
        message,
        conversation_id: conversationId
      })
    });

    if (!response.ok || !response.body) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    
    let fullResponse = '';
    let convID = null;
    
    while (true) {
      const { done, value } = await reader.read();
      
      if (done) break;
      
      const chunk = decoder.decode(value);
      const lines = chunk.split('\n');
      
      for (const line of lines) {
        if (!line.startsWith('event:')) continue;
        
        const [_, eventType] = line.split(': ');
        
        // Parse event data (simplified)
        if (eventType === 'context') {
          convID = JSON.parse(lines[lines.indexOf(line)+1]).data.conversation_id;
        } else if (eventType === 'message') {
          const tokenData = JSON.parse(lines[lines.indexOf(line)+1]);
          fullResponse += tokenData.content;
          
          // Update UI with new tokens
          this.updateTypingArea(fullResponse);
        } else if (eventType === 'done') {
          const finalData = JSON.parse(lines[lines.indexOf(line)+1]);
          return {
            conversationId: convID,
            message: finalData.message,
            sources: finalData.sources,
            citations: finalData.citations
          };
        }
      }
    }
  }
}
```

### UI Implementation Considerations

1. **Loading States**
   - Show "AI is thinking" during initial context building (0-2 seconds)
   - Display typing cursor after first token arrives
   - Add progress indicator for longer responses

2. **Citation Handling**
   ```javascript
   function renderResponseWithCitations(response) {
     // Extract citations from response data
     const citationMap = {};
     
     if (response.citations && response.citations.length > 0) {
       response.citations.forEach((citation, index) => {
         citationMap[index + 1] = citation;
       });
       
       return `
         <div class="ai-response">
           ${escapeHtml(response.message)}
           <hr>
           <h4>Sources:</h4>
           <ul>
             ${response.sources.map(source => 
               `<li>${source}</li>`
             ).join('')}
           </ul>
         </div>
       `;
     }
   }
   ```

3. **Error Recovery**
   ```javascript
   class RetryableStreamingChat {
     constructor(maxRetries = 2) {
       this.maxRetries = maxRetries;
     }
     
     async chatWithRetry(message, conversationId = null, attempts = 0) {
       try {
         return await this.streamingChat(message, conversationId);
       } catch (error) {
         if (attempts < this.maxRetries) {
           // Backoff before retry
           await new Promise(r => setTimeout(r, Math.pow(2, attempts) * 1000));
           return this.chatWithRetry(message, conversationId, attempts + 1);
         }
         throw error;
       }
     }
   }
   ```

## Testing Strategy

### Unit Tests

```go
// Test streaming with new conversation
func TestStreamChat_NewConversation(t *testing.T) {
    req := streamChatRequest{
        Message: "Test question",
        ConversationID: nil,
    }
    
    // Mock response writer and test SSE events
}

// Test streaming with existing conversation
func TestStreamChat_ExistingConversation(t *testing.T) {
    conv := createNewConversation()
    req := streamChatRequest{
        Message: "Follow-up question",
        ConversationID: &conv.ID,
    }
}
```

### Integration Tests

1. **End-to-End Flow**
   ```bash
   # Start backend with test config
   CONVERSATIONS_STORE_PATH=/tmp/test_conv.json \
   go run main.go
   
   # Test streaming endpoint
   curl -X POST http://localhost:8000/api/chat/stream \
     -H "Content-Type: application/json" \
     -d '{"message": "What is healthcare?"}' \
     --no-buffer
   ```

2. **Performance Metrics**
   - Time to first token (TTF1): < 3 seconds target
   - Token delivery rate: > 5 tokens/second
   - Memory usage during streaming: < 100MB

### Manual Testing Checklist

- [ ] New conversation creation works correctly
- [ ] Existing conversation ID is properly handled
- [ ] Context building returns expected results
- [ ] Streaming tokens arrive in correct order
- [ ] Error scenarios are properly caught and reported
- [ ] Sources/citations are accurately attributed
- [ ] Conversation persistence across sessions

## Deployment Configuration

### Nginx Settings

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
    
    # Pass through SSE headers
    proxy_set_header Connection '';
    proxy_http_version 1.1;
}
```

### Environment Variables

```bash
# Conversation persistence
CONVERSATIONS_STORE_PATH=/var/lib/supersonic/conversations.json

# AI service configuration  
AI_SERVICE_URL=http://localhost:11434/v1/chat/completions
AI_SERVICE_API_KEY=your-api-key-here

# Context building limits
MAX_CONTEXT_LENGTH=8000
MAX_RAG_RESULTS=5
```

## Monitoring and Observability

### Metrics to Track

```prometheus
# Streaming chat metrics
supersonic_stream_requests_total{endpoint="/api/chat/stream"}
supersonic_stream_tokens_per_second{quantile="0.95"}
supersonic_stream_error_rate{error_type="context|ai|network"}
supersonic_stream_memory_usage_bytes
```

### Alerting Rules

```yaml
alert: StreamingChatLatencyHigh
  expr: histogram_quantile(0.95, supersonic_stream_first_token_seconds_bucket) > 5
  for: 5m
  
alert: StreamingErrorRateHigh  
  expr: rate(supersonic_stream_errors_total[5m]) / rate(supersonic_stream_requests_total[5m]) > 0.1
```

## Future Enhancements

### Phase 2 Features

1. **Advanced Stream Controls**
   - Token filtering (stop words, profanity)
   - Streaming progress indicators
   - Response editing during generation

2. **Performance Optimizations**
   - Context caching between related queries
   - Parallel RAG retrieval from multiple sources
   - Pre-computed domain-specific prompts

3. **Enhanced UX**
   - Multi-modal response streaming (text + images)
   - Real-time source highlighting
   - Streaming state persistence across page reloads

## References

- [Server-Sent Events Specification](https://html.spec.whatwg.org/multipage/server-sent-events.html)
- OpenAI API: [Streaming Responses](https://platform.openai.com/docs/guides/text-generation/streaming)
- SSE Best Practices:
  - Use proper Content-Type headers
  - Implement reconnection logic in clients
  - Handle network interruptions gracefully

---
*Implementation completed for SuperSonicAI v1.0*