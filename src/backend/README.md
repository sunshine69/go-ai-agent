# SupersonicIQ Backend

FastAPI backend for SupersonicIQ — the AI-powered knowledge assistant for Sonic Healthcare.

## Setup

1. Create and activate virtual environment:
```bash
python -m venv venv
source venv/bin/activate  # On Windows: venv\Scripts\activate
```

2. Install dependencies:
```bash
pip install -r requirements.txt
```

3. Create .env file:
```bash
cp .env.example .env
# Edit .env with your configuration
```

4. Run migrations (if using database):
```bash
alembic upgrade head
```

5. Start the server:
```bash
uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
```

## LLM Configuration

The backend uses **LiteLLM** to support multiple LLM providers including local OpenAI-compatible servers. The LLM configuration is set in the `.env` file.

### Local LLM (OpenAI-compatible server)

For local LLMs like `qwopus-mtp` running at `http://192.168.20.23/v1`:

```env
LLM_PROVIDER=litellm
LLM_MODEL=qwopus-mtp
LLM_API_KEY=dummy-key           # not needed for local servers, but litellm requires it
LLM_TEMPERATURE=0.1
LLM_BASE_URL=http://192.168.20.23/v1
```

**Important:** The `LLM_BASE_URL` is critical — it points the LiteLLM client to your local LLM server instead of the default `api.openai.com`. Without `LLM_BASE_URL`, the request would be sent to OpenAI's servers and fail with an invalid API key error.

### Ollama

```env
LLM_PROVIDER=litellm
LLM_BASE_URL=http://localhost:11434/v1
LLM_MODEL=llama3.2
LLM_API_KEY=dummy-key
```

### vLLM

```env
LLM_PROVIDER=litellm
LLM_BASE_URL=http://localhost:8000/v1
LLM_MODEL=mistralai/Mistral-7B-Instruct-v0.3
LLM_API_KEY=dummy-key
```

### Azure OpenAI

```env
LLM_PROVIDER=azure
LLM_MODEL=gpt-4-0613
LLM_API_KEY=your-azure-key
```

### OpenRouter

```env
LLM_PROVIDER=litellm
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_MODEL=openai/gpt-3.5-turbo
LLM_API_KEY=your-openrouter-key
```

## MCP Tools for Context Retrieval

When a user asks a question, the system searches for relevant context using MCP tools before asking the LLM. It queries these sources in order:

1. **Confluence** — Search Confluence pages
2. **Documents** — Search local documents
3. **Forms** — Search forms directory
4. **Skills** — Search skills directory
5. **Processes** — Search processes directory

**Without Confluence configured:** If Confluence credentials are not set in `.env`, the Confluence MCP tool will return an error, which is silently skipped. The system will still search the local sources (Documents, Forms, Skills, Processes) and return a "Direct Answer" from the LLM if no local context is found.

To get Sonic Healthcare-specific answers, configure at least one of:
- Confluence credentials (`CONFLUENCE_API_TOKEN`, `CONFLUENCE_USERNAME`) in `.env`, OR
- Local document files in `./resources/documents/`

## API Endpoints

### Authentication
- `POST /api/auth/register` - Register a new user
- `POST /api/auth/login` - Login
- `GET /api/auth/me` - Get current user

### Conversations
- `GET /api/conversations` - List all conversations
- `POST /api/conversations` - Create a new conversation
- `GET /api/conversations/{id}` - Get conversation details
- `DELETE /api/conversations/{id}` - Delete a conversation

### Messages
- `POST /api/messages` - Send a message and get AI response
- `GET /api/conversations/{id}/messages` - Get conversation messages

### Documents
- `GET /api/documents` - List available documents
- `GET /api/documents/{category}` - List documents in a category
- `GET /api/documents/{id}/content` - Get document content

### Forms
- `GET /api/forms` - List all forms
- `GET /api/forms/search?q={keyword}` - Search forms

### Skills & Processes
- `GET /api/skills` - List all skills categories
- `GET /api/skills/{category}` - List skills in a category
- `GET /api/skills/search?q={keyword}` - Search skills
- `GET /api/processes` - List all processes
- `GET /api/processes/{id}` - Get process steps
- `GET /api/processes/{id}/owner` - Get process owner
- `GET /api/processes/search?q={keyword}` - Search processes

### Confluence
- `GET /api/confluence/search?q={keyword}` - Search Confluence pages
- `GET /api/confluence/pages/{id}` - Get Confluence page
