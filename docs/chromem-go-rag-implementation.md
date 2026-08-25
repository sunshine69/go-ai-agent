# chromem-go for RAG — Implementation Reference

> Self-contained reference for building the Go equivalent of the Python
> `src/backend/app/rag.py` RAG store on top of `chromem-go`.
>
> Goal: port `RAGStore` (search / add_documents / get_categories /
> get_stats / delete_collection) and the three endpoints in
> `src/backend/app/routers/rag.py` to Go.
>
> **Do not re-fetch upstream docs** unless necessary — everything an
> implementation needs is below. The definitive source is
> `github.com/philippgille/chromem-go` (used at `v0.7.0`).

---

## 0. Quick summary (read this first)

1. **Package path / import alias:** `github.com/philippgille/chromem-go`,
   import as `chromem` throughout this doc.
2. **Add it to the root `go.mod`** (module `supersoniciq/frontend/wails`,
   Go 1.26.5). This is the main backend module, *not* `mcp-server`.
3. **Local embeddings:** there is **no built-in** for the Python
   `all-MiniLM-L6-v2` sentence-transformer model. Implement a custom
   `EmbeddingFunc`. See §3.
4. **Cosine space is implicit**, not a collection metadata setting (as in
   Python's `hnsw:space: cosine`). Achieved by supplying embeddings that are
   **normalized to unit length**. See §4.
5. **`Result.Similarity` is already cosine similarity in [-1, 1]** — the
   Python `1 - distance/2` conversion is **not** needed here. Compare the
   field directly against the threshold.
6. **Metadata must be `map[string]string`** (ints/floats must be stringified).
   This affects `page_number` and `chunk_size`.
7. **No `Get`/`All()` method exists on `Collection`.** Use `Count()` for
   totals and `QueryWithOptions` with a large `NResults` to enumerate for
   categories / stats. See §8.

---

## 1. Install

```bash
cd <repo-root>          # where the root go.mod lives
go get github.com/philippgille/chromem-go@v0.7.0
```

`chromem-go` is pure Go + SQLite persistence. No CGO required for the
default storage.

---

## 2. Core types & signatures

### `EmbeddingFunc`
```go
// A function that creates embeddings for a given text.
// MUST return a normalized (unit-length) vector for cosine semantics.
type EmbeddingFunc func(ctx context.Context, text string) ([]float32, error)
```

### `DB` (persistent store)
```go
func NewPersistentDB(path string, compress bool) (*DB, error)
```
- `path` empty → defaults to `"./chromem-go"`.
- Persistence covers collections + documents + metadata. It does **not**
  persist the `EmbeddingFunc` — you must pass the same `EmbeddingFunc` when
  re-opening a DB and adding to existing collections.

### `DB` collection management
```go
func (db *DB) GetOrCreateCollection(name string, metadata map[string]string, embeddingFunc EmbeddingFunc) (*Collection, error)
func (db *DB) DeleteCollection(name string) error
func (db *DB) Reset() error                    // removes all collections (persistent DB also wipes dir)
```

### `Collection`
```go
// Add documents in a single batch. embeddings is [][]float32, one per doc.
func (c *Collection) Add(ctx context.Context, ids []string, embeddings [][]float32, metadatas []map[string]string, contents []string) error

// Alternative: build documents first (embeddings optional), embed concurrently.
func (c *Collection) AddDocuments(ctx context.Context, documents []Document, concurrency int) error
func (c *Collection) Count() int
func (c *Collection) Delete(_ context.Context, where, whereDocument map[string]string, ids ...string) error
func (c *Collection) GetByID(ctx context.Context, id string) (Document, error)
func (c *Collection) QueryWithOptions(ctx context.Context, options QueryOptions) ([]Result, error)
```

> For batch precomputed embeddings (the Python `add()` path), **use `Add`**.
> Use `NewDocument` + `AddDocuments` only if you let chromem-go embed.

### `Document`
```go
type Document struct {
    ID        string
    Metadata  map[string]string
    Embedding []float32
    Content   string
}

func NewDocument(ctx context.Context, id string, metadata map[string]string, embedding []float32, content string, embeddingFunc EmbeddingFunc) (Document, error)
```

### `QueryOptions`
```go
type QueryOptions struct {
    // Text to search for (embedded internally by the collection's EmbeddingFunc).
    QueryText string
    // Precomputed query embedding (same model as the docs). Overrides QueryText if set.
    QueryEmbedding []float32
    // Number of results to return.
    NResults int
    // Metadata conditional filter, e.g. {"source_category": "policy"}.
    Where map[string]string
    // Document conditional filter, e.g. {"$contains": "foo"}.
    WhereDocument map[string]string
    Negative NegativeQueryOptions
}
```

### `Result`
```go
type Result struct {
    ID         string            // → chunk_id
    Metadata   map[string]string // e.g. Metadata["title"]
    Embedding  []float32
    Content    string            // → document text
    Similarity float32           // cosine similarity in [-1, 1]; higher = more similar
}
```

---

## 3. Local embeddings (`all-MiniLM-L6-v2`) — the main open item

Python uses a local sentence-transformer model producing **384-dim** vectors.
chromem-go ships only API-based embedding helpers (`NewEmbeddingFuncOpenAI`,
`NewEmbeddingFuncOllama`, `NewEmbeddingFuncCohere`, `NewEmbeddingFuncJina`,
`NewEmbeddingFuncMistral`, `NewEmbeddingFuncVertex`, etc.). **None is a local
sentence-transformer.**

**Recommended approach:** run a tiny local embedding server and have a custom
`EmbeddingFunc` call it.

Option A — Ollama-compatible endpoint:
```go
func ollamaEmbed(ctx context.Context, text string) ([]float32, error) {
    req, _ := http.NewRequestWithContext(ctx, "POST", ollamaBaseURL+"/api/embeddings",
        bytes.NewJSON(map[string]any{"model": model, "prompt": text}))
    resp, err := http.DefaultClient.Do(req)
    // decode {"embedding": [...]}, return []float32
}
```

Option B — a small `sentence-transformers` FastAPI/Flask wrapper that returns
normalized 384-dim vectors.

**Critical requirement:** the returned vector MUST be unit-length (normalized),
because chromem-go treats it as cosine. `all-MiniLM-L6-v2` already produces
normalized outputs, but if your server/path doesn't, normalize yourself:
```go
// Euclidean normalization so similarity is cosine-based
var norm float32
for _, v := range vec { norm += v * v }
norm = math.Sqrt(norm)
for i := range vec { vec[i] /= norm }
```

This `EmbeddingFunc` is created **once** and passed into
`GetOrCreateCollection` at init. It is reused by the collection for any
internal (query-text) embedding.

---

## 4. Cosine metric — what is different from Python

Python sets collection metadata `{"hnsw:space": "cosine"}`. chromem-go has
**no such setting**. The equivalent behavior comes entirely from
`EmbeddingFunc` returning **normalized vectors** (see §3). Keep this in mind:
do not look for a space/collection-metadata option that does not exist.

---

## 5. Similarity semantics — skip the distance conversion

| | Range | Meaning | Threshold check |
|---|---|---|---|
| Python ChromaDB `distances` | [0, 2] | *distance* | `sim = 1 - distance/2`, then `sim >= threshold` |
| chromem-go `Result.Similarity` | [-1, 1] | *similarity* | **directly** `result.Similarity >= threshold` |

Python converts distance→similarity; chromem-go already gives similarity. Do
**not** re-derive it. The config default is `RAG_SCORE_THRESHOLD=0.25`.

---

## 6. `search()` mapping

Python:
```python
query_embedding = self.embedder.encode(query).tolist()
where = {"source_category": category} if category else None
res = self.collection.query(query_embeddings=[query_embedding],
                            n_results=limit, where=where,
                            include=["documents","metadatas","distances"])
```

Go:
```go
func (s *RAGStore) Search(ctx context.Context, query string, limit int, category string) ([]RAGResult, error) {
    // 1. (optional) precompute query embedding via your EmbeddingFunc
    // qEmb, err := s.embed(ctx, query)

    // 2. build filter
    where := map[string]string{}
    if category != "" { where["source_category"] = category }

    // 3. query (pass QueryText to use the collection's own embedder,
    //    OR pass QueryEmbedding: qEmb to reuse a precomputed one)
    results, err := s.collection.QueryWithOptions(ctx, chromem.QueryOptions{
        QueryText:  query,      // or QueryEmbedding: qEmb
        NResults:   limit,
        Where:      where,
    })
    if err != nil { return nil, err }

    // 4. format + threshold filter
    out := []RAGResult{}
    for _, r := range results {
        if r.Similarity < s.threshold { continue }
        pageNum, _ := strconv.Atoi(r.Metadata["page_number"])
        out = append(out, RAGResult{
            ChunkID:         r.ID,
            DocumentTitle:   r.Metadata["title"],
            SourceFile:      r.Metadata["source_file"],
            SourceCategory:  r.Metadata["source_category"],
            PageNumber:      pageNum,
            Content:         r.Content,
            SimilarityScore: r.Similarity,
        })
    }
    return out, nil
}
```

**Equivalent Go struct:**
```go
type RAGResult struct {
    ChunkID         string  `json:"chunk_id"`
    DocumentTitle   string  `json:"document_title"`
    SourceFile      string  `json:"source_file"`
    SourceCategory  string  `json:"source_category"`
    PageNumber      int     `json:"page_number"`
    Content         string  `json:"content"`
    SimilarityScore float32 `json:"similarity_score"`
}
```

---

## 7. `add_documents()` mapping

Python:
```python
embeddings = self.embedder.encode(texts).tolist()   // [][]float32
self.collection.add(ids=ids, embeddings=embeddings, metadatas=metadatas, documents=texts)
```

Go:
```go
func (s *RAGStore) AddDocuments(ctx context.Context, chunks []Chunk) (int, error) {
    if len(chunks) == 0 { return 0, nil }

    ids       := make([]string, len(chunks))
    contents  := make([]string, len(chunks))
    emb       := make([][]float32, len(chunks))
    metas     := make([]map[string]string, len(chunks))

    for i, c := range chunks {
        ids[i]       = c.ChunkID
        contents[i]  = c.Content
        emb[i]       = c.Embedding   // precomputed [][]float32
        metas[i]     = map[string]string{
            "source_file":     c.SourceFile,
            "source_category": c.SourceCategory,
            "document_type":   c.DocumentType,
            "title":           c.Title,
            "page_number":     strconv.Itoa(c.PageNumber),   // MUST be string
            "chunk_size":      strconv.Itoa(c.ChunkSize),    // MUST be string
            "last_indexed":    time.Now().UTC().Format(time.RFC3339),
            "checksum":        c.Checksum,
        }
    }
    if err := s.collection.Add(ctx, ids, emb, metas, contents); err != nil {
        return 0, err
    }
    return len(chunks), nil
}
```

**Metadata string rule (from §2):** every metadata value is `string`. Any
Python metadata that was int/float must be stringified here
(`page_number`, `chunk_size`). Read them back with `strconv.Atoi` when needed.

---

## 8. `get_categories()` & `get_stats()` — the enumeration workaround

chromem-go **has no `Get`/`All()`/"return all metadata"** method.
The only read-all path is `QueryWithOptions` with a large `NResults`.

```go
func (s *RAGStore) getCategories(ctx context.Context) ([]string, error) {
    n := s.collection.Count()
    if n == 0 { return []string{}, nil }
    results, err := s.collection.QueryWithOptions(ctx, chromem.QueryOptions{
        NResults: n, // enumerate everything at once
    })
    if err != nil { return nil, err }

    set := map[string]bool{}
    for _, r := range results {
        if cat := r.Metadata["source_category"]; cat != "" { set[cat] = true }
    }
    return keys(sorted(set)), nil
}

func (s *RAGStore) Stats(ctx context.Context) (Stats, error) {
    total := s.collection.Count()
    categories, err := s.getCategories(ctx)
    if err != nil { return Stats{}, err }
    categoryCounts := map[string]int{}

    if total > 0 {
        results, _ := s.collection.QueryWithOptions(ctx, chromem.QueryOptions{NResults: total})
        for _, r := range results {
            if cat := r.Metadata["source_category"]; cat != "" {
                categoryCounts[cat]++
            }
        }
    }
    return Stats{
        TotalChunks:          total,
        Categories:           categories,
        CategoryCounts:       categoryCounts,
        EmbeddingDimensions:  384,
        EmbeddingModel:       RAG_EMBEDDING_MODEL,
    }, nil
}
```

> Note: a single query returns at most `NResults` docs, so enumerate in a loop
> with an offset/token approach if `total` is large. For typical RAG corpora
> (hundreds–low thousands of chunks) one `NResults: total` query is fine.

---

## 9. `delete_collection()` mapping (reset mode)

```go
func (s *RAGStore) Reset(ctx context.Context) error {
    if err := s.db.DeleteCollection(collectionName); err != nil {
        return err
    }
    col, err := s.db.GetOrCreateCollection(collectionName, nil, s.embed)
    if err != nil { return err }
    s.collection = col
    return nil
}
```

---

## 10. Endpoints (`routers/rag.py` → Go HTTP)

Three endpoints to re-expose in the Go backend:

| Method + path | Body / params | Returns |
|---|---|---|
| `POST /rag/search` | `{query, limit?, category?}` | `{results, total, category}` |
| `GET /rag/categories` | — | `{categories}` |
| `GET /rag/stats` | — | `{total_chunks, categories, category_counts, embedding_dimensions, embedding_model}` |

Wire them through whatever router the main module uses (Wails bridge or Go
`net/http`). The `RAGStore` should own all chromem-go calls so handlers stay
thin.

---

## 11. Implementation checklist for the next agent

- [ ] Add `chromem-go@v0.7.0` to root `go.mod` (`go get`).
- [ ] Implement local `EmbeddingFunc` returning **normalized 384-dim** `[]float32`
      (local server/Ollama/sentence-transformer). Wire via env config
      (`RAG_ENABLED`, model base URL, etc.).
- [ ] Build `RAGStore` init: `NewPersistentDB(path, false)` +
      `GetOrCreateCollection("rag_documents", nil, embed)`.
- [ ] Implement `Search` using `QueryWithOptions`, `Result.Similarity >= threshold`
      (no distance conversion). Handle `where = {source_category}`.
- [ ] Implement `AddDocuments` using `Add` with precomputed embeddings;
      **stringify** `page_number` and `chunk_size` in metadata.
- [ ] Implement `getCategories` + `Stats` via `Count()` + `QueryWithOptions(NResults: total)`.
- [ ] Implement `Reset` via `DeleteCollection` + `GetOrCreateCollection`.
- [ ] Expose the three HTTP routes with the same response shapes as Python.
- [ ] Respect `RAG_ENABLED` parity — if disabled, all methods are no-ops
      returning empty/zero (Python returns `[]`, `0`, empty maps).
- [ ] Test: fresh index → search with a known query returns matching chunk;
      threshold filter drops low-similarity results; category filter narrows.

---

## 12. Gotchas recap (the short list)

1. Add to the **root** `go.mod`, not `mcp-server`.
2. **No local sentence-transformer helper** — write your own `EmbeddingFunc`.
3. **Cosine is implicit** via normalized embeddings — no collection metadata.
4. **`Result.Similarity` is already similarity** in [-1, 1] — no `1 - d/2`.
5. **Metadata is `map[string]string`** — stringify ints/floats.
6. **No `Get`/`All()`** — use `Count()` + `QueryWithOptions` to enumerate.
7. **Persistence doesn't store `EmbeddingFunc`** — pass it in on every open.
