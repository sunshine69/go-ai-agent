# Loading Content into SupersonicIQ — Step-by-Step Guide

This covers the three ways to get information into SupersonicIQ, and how to wire a
Confluence space into a domain. See [architecture.md](architecture.md) for the
overall system design.

## Decision tree — which path do I use?

| Content type | Path | Where it lives |
|---|---|---|
| Confluence pages | **A. Confluence (live)** | Nothing to load — already searchable |
| Forms, skills, processes, FAQs (structured, short) | **B. Local structured documents** | `resources/documents/<category>/` |
| Policies, handbooks, PDFs, long-form docs | **C. RAG (vector search)** | `resources/rag_documents/<category>/` |

If unsure, start with B (simplest) and move to C only for large/unstructured files
where keyword search isn't precise enough.

---

## A. Confluence content (live — no ingestion step)

Confluence isn't ingested/copied anywhere — `confluence_search` / `confluence_read_page`
/ `confluence_search_by_label` query the live wiki on every request (see
[confluence-mcp.go](../mcp-server/confluence-mcp.go)). There's nothing to "load", but
two things you *can* configure:

1. **Auth** — already set in `src/backend/.env`: `CONFLUENCE_BASE_URL`,
   `CONFLUENCE_API_TOKEN`, `CONFLUENCE_USERNAME`, `CONFLUENCE_AUTH_MODE` (this
   instance is Data Center → `bearer`).
2. **Scoping a domain to specific spaces** — `confluence_search` now accepts an
   optional `space` parameter (comma-separated space keys) that gets turned into a
   CQL `space in (...)` filter. This is wired up automatically per domain via
   `confluence_spaces` in [domain_config.py](../src/backend/app/domain_config.py) —
   see the "Adding a new domain" section below.

Current domain → space mapping (from the 40 spaces on this wiki instance — **please
confirm these with the business, they're my best guess from space names alone**):

| Domain | Spaces (key) | Reasoning |
|---|---|---|
| Pathology | `PMP`, `DPAI`, `DHMG` | Programme Management - Pathology, Digital Pathology & AI, DHM Molecular Genetics |
| Radiology | `Imaging` | Sonic Imaging |
| Operations | `TS`, `SSVC`, `SOS` | Sonic IT, Site Services, Infrastructure — broadest guess, worth narrowing |
| HR | *(none yet)* | No dedicated HR/People space found. `BUS` (Business Support) / `BD` (Business Documentation) are candidates but too generic to assume — left unscoped (searches all spaces) until confirmed |

To change the mapping, edit `confluence_spaces` in `domain_config.py` for that domain.
Leave the list empty (`[]`) to search unscoped.

**Verify it's working:** ask a question with that domain selected and check the
backend/MCP server logs (stderr) for lines like:

```
[Confluence] search query="..." space="PMP,DPAI,DHMG" -> N result(s) (total M)
```

If `N` stays 0 for queries you know should match, the space keys are probably wrong —
double check with `confluence_get_spaces`.

---

## B. Local structured documents (forms / procedures / faq / skills / processes)

Used by `documents_list`, `documents_search`, `forms_list`, `forms_search`,
`skills_list`, `skills_search`, `process_get_steps`, `process_get_owner`,
`process_search`.

### Forms / procedures / faq / confluence exports (plain files)

Just drop a file into the matching folder — no registration step needed, the Go
server auto-discovers on startup ([documents-mcp.go](../mcp-server/documents-mcp.go),
`autoDiscover`):

```
resources/documents/
├── forms/          <- .md/.txt/.docx/.pdf/.xlsx/.json/.csv files
├── procedures/
├── faq/
└── confluence/      (saved page exports, same rules)
```

Example (see `resources/documents/forms/hba1c_form.md` for a real one):

```markdown
# Pathology Request Form — <Test Name>

## Purpose
...

## When to Use
...

## Collection Instructions
...
```

- Document ID becomes `<folder-name>-<filename>` (e.g. `forms-hba1c_form.md`).
- Title = filename without extension.
- Tags default to `[filename]` — search matches on title, description, or tags.
- **Restart the MCP server** after adding files (auto-discovery runs at startup, not live).

For richer metadata (custom title/description/tags instead of the filename-derived
defaults), add a `_collections.json` next to the files — see the `DocumentCollection`
struct in `documents-mcp.go` for the exact shape (`name`, `description`, `path`,
`documents: [{id, title, path, category, description, tags, form_type}]`).

### Skills directory

Edit `resources/documents/skills/skills_directory.json` directly. Shape:

```json
{
  "last_updated": "2026-08-18",
  "categories": {
    "<category_key>": {
      "name": "Human-readable category name",
      "skills": [
        {
          "skill_id": "LAB-005",
          "name": "Skill name",
          "level": "Basic|Intermediate|Advanced",
          "description": "...",
          "training_required": "LAB-TR-005",
          "assessor": "Role/person",
          "validity_months": 12
        }
      ]
    }
  }
}
```

### Processes

Edit `resources/documents/skills/processes.json` directly. Shape:

```json
{
  "last_updated": "2026-08-18",
  "processes": [
    {
      "process_id": "PROC-002",
      "name": "Process name",
      "description": "...",
      "owner": "Full name",
      "owner_title": "Job title",
      "owner_email": "email@sonichealthcare.com.au",
      "phases": [
        {"phase": "Phase name", "duration_weeks": 2, "steps": ["Step 1", "Step 2"]}
      ]
    }
  ]
}
```

**Restart the MCP server** after editing either JSON file (loaded once at startup).

**Verify:** call `skills_search`/`process_search`/`documents_search` with a keyword
you just added and confirm it comes back, or check server startup logs for parse
errors.

---

## C. Unstructured documents via RAG (PDFs, long Markdown, policies)

1. Drop the file(s) into `resources/rag_documents/<category>/`, where category is one
   of `policy`, `procedure`, `training`, `reference` (only `policy` has content today).
2. Run the indexer from `src/backend/`:
   ```bash
   cd src/backend
   python -m app.cli.rag_indexer --mode incremental
   # or scope to one category:
   python -m app.cli.rag_indexer --mode incremental --category policy
   ```
   Use `--mode full` to force re-indexing everything, `--mode reset` to wipe and
   rebuild, `--mode dry-run` to preview without writing.
3. Verify via the API (backend must be running):
   ```bash
   curl -X POST http://localhost:8000/api/rag/search -H "Content-Type: application/json" \
     -d '{"query": "safety policy", "limit": 3}'
   curl http://localhost:8000/api/rag/categories
   ```

Config knobs (`src/backend/.env`): `RAG_ENABLED`, `CHROMA_PATH`, `RAG_CHUNK_SIZE`
(1500), `RAG_CHUNK_OVERLAP` (300), `RAG_SEARCH_LIMIT` (5), `RAG_SCORE_THRESHOLD`
(0.25 — raise to be stricter, lower to surface more marginal matches).

---

## Adding a new domain end-to-end

1. **`src/backend/app/domain_config.py`** — add a new top-level entry to `DOMAINS`
   with `display_name`, `icon`, `sub_categories` (each with keywords), a domain-level
   `keywords` list, and `confluence_spaces` (space keys, or `[]` if none confirmed yet).
2. **`src/backend/app/context_builder.py`** — add an `elif domain == "YourDomain":`
   branch in `_get_tools_for_domain` mapping each sub-category to the MCP tool calls
   to run (mirror the existing HR/Pathology/Radiology/Operations branches). The
   `confluence_spaces` scoping is applied automatically afterwards — no need to pass
   `space` manually.
3. **Frontend** — the domain picker in `src/frontend/app.py` reads from `/api/domains`,
   which is served from `DOMAINS` directly, so no frontend code changes needed.
4. **Content** — populate B/C paths above for anything not already covered by
   Confluence for that domain.
5. **Verify** — select the new domain + sub-category in the UI, ask a relevant
   question, and check the backend/MCP logs for the tool calls and result counts.
