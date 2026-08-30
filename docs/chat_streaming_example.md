# Chat Streaming Implementation Guide

## Overview

The new streaming chat endpoint `/api/chat/stream` provides real-time responses from the AI server using Server-Sent Events (SSE). This document explains how to integrate this endpoint with your frontend application.

## Endpoint Details

**URL:** `POST /api/chat/stream`

**Content-Type:** `text/event-stream`

**Authentication:** None required (CORS enabled)

## Request Format

```json
{
  "message": "Your question here",
  "conversation_id": "optional-existing-id", // If not provided, a new conversation is created
  "domain": "Optional domain filter",         // e.g., "HR", "Pathology"
  "sub_category": "Optional sub-category",    // e.g., "onboarding", "forms"
  "history": [
    {"role": "user", "content": "Previous question"},
    {"role": "assistant", "content": "Previous answer"}
  ]
}
```

## Response Events

The endpoint returns a stream of SSE events:

1. **context** - Initial metadata including conversation ID and sources
   ```json
   {
     "conversation_id": "conv_20231015143000",
     "sources": ["Confluence Search", "RAG Document Search"]
   }
   ```

2. **system** (optional) - System prompt configuration

3. **message** - Individual tokens/chunks of the AI response
   ```json
   {"content": "Based on"}
   {"content": " the HR"}
   {"content": " policies..."}
   ```

4. **done** - Final message with full context and sources
   ```json
   {
     "conversation_id": "conv_20231015143000",
     "message": "Complete response text...",
     "sources": ["HR Handbook", "Confluence Page"],
     "citations": [
       {"title": "Onboarding Guide", "url": "https://confluence.example.com/123"}
     ]
   }
   ```

5. **error** - Error information (if any)
   ```json
   {"error": "Error message details..."}
   ```

## JavaScript Client Example

```javascript
// Store conversation state globally or in your app state manager
let activeConversationId = null;
const chatMessages = [];

async function sendChatMessage(message, domain = '', subCategory = '') {
  try {
    // Prepare request payload
    const requestBody = {
      message: message,
      conversation_id: activeConversationId || undefined, // Only include if existing
      domain: domain,
      sub_category: subCategory,
      history: chatMessages.slice(-10).map(msg => ({
        role: msg.role === 'user' ? 'user' : 'assistant',
        content: msg.content
      }))
    };

    const response = await fetch('http://localhost:8000/api/chat/stream', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(requestBody)
    });

    if (!response.ok) {
      throw new Error(`Server error: ${response.statusText}`);
    }

    // Parse SSE stream
    const reader = response.body.getReader();
    const decoder = new TextDecoder('utf-8');
    let buffer = '';

    while (true) {
      const { value, done } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      
      // Process complete SSE events
      const lines = buffer.split('\n\n');
      buffer = lines.pop(); // Keep incomplete event in buffer

      for (const line of lines) {
        processSSEEvent(line.trim());
      }
    }

  } catch (error) {
    console.error('Stream failed:', error);
    showErrorMessage(error.message);
  }
}

function processSSEEvent(eventText) {
  const lines = eventText.split('\n');
  
  if (!lines[0].startsWith('event: ')) return;
  
  const eventType = lines[0].substring(6); // Remove "event: "
  let data = {};
  
  try {
    data = JSON.parse(lines[1].substring(5)); // Remove "data: "
  } catch (e) {
    console.warn('Failed to parse SSE event:', e);
    return;
  }

  switch (eventType) {
    case 'context':
      activeConversationId = data.conversation_id;
      console.log('New conversation started:', activeConversationId);
      
      // Display sources if available
      if (data.sources && data.sources.length > 0) {
        console.log('Sources:', data.sources.join(', '));
      }
      break;

    case 'message':
      const content = JSON.parse(data.content).content;
      document.getElementById('chat-output').innerHTML += content;
      // Auto-scroll to bottom
      window.scrollTo(0, document.body.scrollHeight);
      break;

    case 'done':
      console.log('Chat completed with ID:', data.conversation_id);
      
      // Store conversation for future reference
      if (data.sources) {
        console.log('Final sources:', data.sources);
      }
      if (data.citations) {
        console.log('Citations:', data.citations);
      }
      break;

    case 'error':
      showErrorMessage(data.error || 'An error occurred');
      break;
  }
}

function showErrorMessage(message) {
  const errorDiv = document.getElementById('chat-errors');
  if (errorDiv) {
    errorDiv.innerHTML += `<div class="error">${message}</div>`;
  } else {
    alert(`Error: ${message}`);
  }
}
```

## Python Client Example

```python
import requests
import json

class ChatClient:
    def __init__(self, base_url="http://localhost:8000"):
        self.base_url = base_url
        self.conversation_id = None
    
    def send_message(self, message, domain="", sub_category="", history=None):
        """
        Send a chat message and receive streaming response.
        
        Args:
            message (str): User's question
            domain (str): Optional domain filter
            sub_category (str): Optional sub-category filter
            history (list): Previous conversation turns
            
        Returns:
            dict: Final response with conversation details
        """
        url = f"{self.base_url}/api/chat/stream"
        
        payload = {
            "message": message,
            "conversation_id": self.conversation_id,  # None for new conversations
            "domain": domain,
            "sub_category": sub_category,
            "history": history or []
        }
        
        response = requests.post(url, json=payload)
        response.raise_for_status()
        
        full_content = ""
        
        # Process SSE stream manually since requests doesn't handle it natively
        for line in response.iter_lines(decode_unicode=True):
            if not line:
                continue
            
            try:
                parts = line.split('\n')
                event_type = None
                data_str = None
                
                for part in parts:
                    part = part.strip()
                    if part.startswith('event: '):
                        event_type = part[7:]
                    elif part.startswith('data: '):
                        data_str = part[6:]
                
                if not event_type or not data_str:
                    continue
                    
                # Parse JSON data
                try:
                    data = json.loads(data_str)
                except json.JSONDecodeError as e:
                    print(f"Failed to parse SSE data: {e}")
                    continue
                
                # Handle different event types
                if event_type == 'context':
                    self.conversation_id = data.get('conversation_id')
                    print(f"Started new conversation: {self.conversation_id}")
                    
                elif event_type == 'message':
                    content = json.loads(data.get('content', '{}')).get('content', '')
                    full_content += content
                    # Display chunk as it arrives (for real-time streaming)
                    print(content, end='', flush=True)
                    
                elif event_type == 'done':
                    return {
                        "conversation_id": data.get('conversation_id'),
                        "message": data.get('message', ''),
                        "sources": data.get('sources', []),
                        "citations": data.get('citations', [])
                    }
                    
                elif event_type == 'error':
                    error_msg = data.get('error', 'Unknown error')
                    print(f"Error: {error_msg}")
                    return {"error": error_msg}
                    
            except Exception as e:
                print(f"Event processing error: {e}")
        
        return None

# Usage example
if __name__ == "__main__":
    client = ChatClient()
    
    # Start new conversation
    response = client.send_message(
        "What is the procedure for requesting time off?",
        domain="HR",
        sub_category="leave"
    )
    
    if response:
        print(f"\n\nConversation ID: {response.get('conversation_id')}")
        print(f"Sources used: {', '.join(response.get('sources', []))}")
        
        # Continue conversation with same ID
        next_response = client.send_message(
            "Can you explain the approval process?",
            domain="HR",
            sub_category="leave"
        )
```

## Integration Checklist

✅ **Frontend Requirements:**
- Handle SSE stream parsing correctly
- Store and reuse `conversation_id` for multi-turn conversations  
- Display real-time chunks as they arrive
- Show context/sources after response completes
- Handle error events gracefully

✅ **Backend Features Implemented:**
- Conversation ID lifecycle management (create or reuse)
- Context building with MCP + RAG integration
- OpenAI-compatible streaming via `AnswerStream()`
- Source tracking and citation formatting
- SSE event formatting for all message types

✅ **Testing Steps:**
1. Start the backend server: `go run main.go`
2. Send a test POST request to `/api/chat/stream`
3. Verify conversation ID is returned in first 'context' event
4. Check that token chunks appear as 'message' events
5. Confirm full response with sources appears in 'done' event

## Advanced Features

### Custom System Prompts
The system prompt can be customized per request by adding a `system_prompt` field to your JSON payload, though the default two-identity system (GenIQ + Friendly Agent) works well for most cases.

### Domain Filtering
Specify domain/sub-category filters to scope knowledge base searches:
```json
{
  "domain": "Pathology",
  "sub_category": "forms"
}
```

### Conversation History
Include recent conversation history in subsequent requests for better context:

```javascript
const request = {
  message: "What about sick leave?",
  conversation_id: activeConversationId,
  domain: "HR",
  sub_category: "leave",
  history: [
    {role: "user", content: "How do I request time off?"},
    {role: "assistant", content: "To request vacation time, you should..."}
  ]
};
```

## Troubleshooting

### Common Issues:

1. **Connection timeouts**
   - Ensure backend is running on expected port
   - Check firewall settings
   - Verify AI server (OpenAI/llama.cpp) is accessible

2. **Missing conversation ID in response**
   - This indicates a server error occurred during initialization
   - Check browser console for detailed error messages

3. **Incomplete responses**
   - Increase timeout value on client-side fetch request
   - Check backend logs for streaming errors
   - Verify AI server isn't rate-limiting requests

4. **SSE parsing failures**
   - Ensure proper SSE event formatting (event: + data:)
   - Handle partial events that span multiple reads
   - Validate JSON in data payloads before parsing

### Debug Mode:
Enable verbose logging by setting environment variable:
```bash
DEBUG=1 go run main.go
```

## Security Considerations

- CORS is enabled for development; restrict origins in production
- No authentication required currently (security review recommended)
- Conversation IDs are UUIDs but don't include PII
- Source citations should be validated before showing to end users
