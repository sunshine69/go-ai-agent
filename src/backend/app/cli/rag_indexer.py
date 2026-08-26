"""
RAG Indexer CLI - Index RAG documents into ChromaDB

Usage:
    python -m app.cli.rag_indexer --mode full
    python -m app.cli.rag_indexer --mode incremental
    python -m app.cli.rag_indexer --category policy
    python -m app.cli.rag_indexer --mode reset
    python -m app.cli.rag_indexer --mode dry-run
"""
import argparse
import hashlib
import json
import os
import sys
from pathlib import Path
from datetime import datetime

# Add backend root to path
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from app.rag import RAGStore, RAG_CHUNK_SIZE, RAG_CHUNK_OVERLAP

# Configuration
RAG_DOCS_DIR = os.getenv("RAG_DOCS_DIR", "./resources/rag_documents")
# From rag_indexer.py: <project>/src/backend/app/cli/rag_indexer.py
# Go up 5 levels to reach <project>/, then resources/rag_documents
RAG_DOCS_DIR = os.path.normpath(os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))),
    "resources", "rag_documents"
))

def parse_args():
    parser = argparse.ArgumentParser(description="Index RAG documents into ChromaDB")
    parser.add_argument(
        "--mode",
        type=str,
        choices=["full", "incremental", "reset", "dry-run"],
        default="incremental",
        help="Indexing mode",
    )
    parser.add_argument(
        "--category",
        type=str,
        default=None,
        help="Filter by category (any directory under RAG_DOCS_DIR)",
    )
    parser.add_argument(
        "--docs-dir",
        type=str,
        default=None,
        help="Override RAG documents directory",
    )
    return parser.parse_args()

def get_file_checksum(filepath):
    """Calculate SHA256 checksum of a file."""
    sha256_hash = hashlib.sha256()
    with open(filepath, "rb") as f:
        for chunk in iter(lambda: f.read(4096), b""):
            sha256_hash.update(chunk)
    return sha256_hash.hexdigest()

def chunk_text(text, chunk_size=RAG_CHUNK_SIZE, overlap=RAG_CHUNK_OVERLAP):
    """Split text into chunks with overlap."""
    if len(text) <= chunk_size:
        return [text]
    
    chunks = []
    start = 0
    while start < len(text):
        end = start + chunk_size
        chunk = text[start:end]
        chunks.append(chunk)
        start = end - overlap  # Overlap for continuity
    
    return chunks

def load_pdf(filepath):
    """Extract text from PDF using PyMuPDF."""
    import pymupdf
    
    doc = pymupdf.open(filepath)
    text = ""
    for page in doc:
        page_text = page.get_text()
        if page_text.strip():
            text += f"\n--- Page {page.number + 1} ---\n{page_text}\n"
    doc.close()
    return text.strip()

def load_md(filepath):
    """Read text from Markdown file."""
    with open(filepath, "r", encoding="utf-8") as f:
        return f.read().strip()

def load_file(filepath):
    """Load text from file based on extension."""
    ext = filepath.lower().split(".")[-1]
    
    if ext in ("pdf",):
        return load_pdf(filepath)
    elif ext in ("md",):
        return load_md(filepath)
    else:
        raise ValueError(f"Unsupported file type: {ext}")

def get_document_title(filepath):
    """Extract document title from filename."""
    title = Path(filepath).stem
    # Replace underscores and hyphens with spaces, capitalize
    title = title.replace("_", " ").replace("-", " ")
    title = title.title()
    return title

def build_chunks(filepath, category):
    """Extract text from file and build chunks."""
    try:
        text = load_file(filepath)
    except Exception as e:
        print(f"  ERROR loading {filepath}: {e}")
        return []
    
    if not text.strip():
        print(f"  WARNING: Empty file {filepath}")
        return []
    
    chunks = chunk_text(text)
    
    # Determine page numbers for PDF chunks
    if filepath.lower().endswith(".pdf"):
        import pymupdf
        doc = pymupdf.open(filepath)
        page_counts = [0] * doc.page_count
        for i, page in enumerate(doc):
            page_text = page.get_text()
            page_counts[i] = len(page_text)
        doc.close()
        
        # Assign page numbers to chunks
        chunk_pages = []
        current_page = 0
        current_text_pos = 0
        for chunk in chunks:
            # Find which page this chunk starts on
            while current_page < len(page_counts) - 1 and current_text_pos + len(chunk) > page_counts[current_page]:
                current_text_pos += page_counts[current_page]
                current_page += 1
            chunk_pages.append(current_page + 1)
        
        chunks = [
            {
                "content": chunk,
                "page_number": chunk_pages[i],
            }
            for i, chunk in enumerate(chunks)
        ]
    else:
        # MD files - page_number is 0 (not applicable)
        chunks = [
            {
                "content": chunk,
                "page_number": 0,
            }
            for chunk in chunks
        ]
    
    # Build chunk dictionaries
    document_title = get_document_title(filepath)
    checksum = get_file_checksum(filepath)
    document_type = filepath.lower().split(".")[-1]
    
    result_chunks = []
    for i, chunk in enumerate(chunks):
        chunk_id = f"rag-{category}-{Path(filepath).stem}-{i:04d}"
        result_chunks.append({
            "chunk_id": chunk_id,
            "content": chunk["content"],
            "source_file": filepath,
            "source_category": category,
            "document_type": document_type,
            "title": document_title,
            "page_number": chunk["page_number"],
            "chunk_size": len(chunk["content"]),
            "checksum": checksum,
        })
    
    return result_chunks

def load_index_state():
    """Load indexing state from JSON file."""
    state_file = os.path.join(RAG_DOCS_DIR, "_index_state.json")
    if os.path.exists(state_file):
        with open(state_file, "r") as f:
            return json.load(f)
    return {}

def save_index_state(state):
    """Save indexing state to JSON file."""
    state_file = os.path.join(RAG_DOCS_DIR, "_index_state.json")
    with open(state_file, "w") as f:
        json.dump(state, f, indent=2)

def index_documents(mode="incremental", category=None):
    """Index RAG documents into ChromaDB."""
    print("=" * 60)
    print("RAG Document Indexer")
    print("=" * 60)
    
    # Get list of categories
    if category:
        categories = [category]
    else:
        categories = [
            d for d in os.listdir(RAG_DOCS_DIR)
            if os.path.isdir(os.path.join(RAG_DOCS_DIR, d))
        ]
    
    if not categories:
        print("No categories found in rag_documents directory")
        return
    
    print(f"\nCategories to index: {', '.join(categories)}\n")
    
    # Initialize ChromaDB
    print("Initializing ChromaDB...")
    rag_store = RAGStore()
    
    # Reset mode - delete collection first
    if mode == "reset":
        print("\n[RESET] Deleting existing collection...")
        try:
            rag_store.delete_collection()
            print("[RESET] Collection deleted")
        except Exception as e:
            print(f"[RESET] Error deleting collection: {e}")
            return
    
    # Load state
    print("Loading index state...")
    state = load_index_state()
    
    # Total stats
    total_files = 0
    total_chunks = 0
    total_new = 0
    total_updated = 0
    total_skipped = 0
    
    # Process each category
    for cat in categories:
        print(f"\n{'='*40}")
        print(f"Category: {cat}")
        print("=" * 40)
        
        cat_dir = os.path.join(RAG_DOCS_DIR, cat)
        if not os.path.isdir(cat_dir):
            print(f"  Category directory not found: {cat_dir}")
            continue
        
        # Process files recursively — .md and .pdf only
        for dirpath, _dirnames, filenames in os.walk(cat_dir):
            for filename in sorted(filenames):
                if not filename.endswith(('.md', '.pdf')):
                    continue
                
                filepath = os.path.join(dirpath, filename)
                total_files += 1
                file_checksum = get_file_checksum(filepath)
                
                # state_key uses relative path within category (e.g. "dhm/test-advice/file.md")
                relpath = os.path.relpath(filepath, cat_dir)
                state_key = f"{cat}/{relpath}"
                
                # Check if file needs indexing
                old_checksum = state.get(state_key, {}).get("checksum", "")
                
                if mode == "incremental" and old_checksum == file_checksum:
                    print(f"  SKIP: {relpath} (unchanged)")
                    total_skipped += 1
                    continue
                
                print(f"  Parsing: {relpath}")
                
                # Build chunks
                try:
                    chunks = build_chunks(filepath, cat)
                except Exception as e:
                    print(f"    ERROR: {e}")
                    continue
                
                if not chunks:
                    print(f"    WARNING: No chunks generated")
                    total_skipped += 1
                    continue
                
                if mode == "dry-run":
                    print(f"    DRY-RUN: {len(chunks)} chunks would be created")
                    total_new += len(chunks)
                    continue
                
                # Check if chunks already exist (for incremental)
                if mode == "incremental" and state.get(state_key):
                    # Check if any chunks are new or modified
                    old_chunks = state[state_key].get("chunks", [])
                    new_chunks = []
                    for chunk in chunks:
                        if chunk["chunk_id"] not in old_chunks:
                            new_chunks.append(chunk)
                        # Skip old chunks (they're already indexed)
                    
                    if new_chunks:
                        print(f"    Adding: {len(new_chunks)} new chunks")
                        rag_store.add_documents(new_chunks)
                        total_new += len(new_chunks)
                    else:
                        print(f"    UPDATING: Metadata only")
                        # Update metadata for existing chunks
                        # (ChromaDB doesn't support metadata updates, so we'd need to delete and re-add)
                        # For now, skip updates
                        total_updated += 1
                else:
                    # New file or full mode - add all chunks
                    print(f"    Adding: {len(chunks)} chunks")
                    rag_store.add_documents(chunks)
                    total_new += len(chunks)
                
                # Update state
                state[state_key] = {
                    "checksum": file_checksum,
                    "last_indexed": datetime.now().isoformat(),
                    "chunks": [chunk["chunk_id"] for chunk in chunks],
                }
    
    # Save state
    save_index_state(state)
    
    # Print summary
    print("\n" + "=" * 60)
    print("Indexing Summary")
    print("=" * 60)
    print(f"Total files: {total_files}")
    print(f"New chunks: {total_new}")
    print(f"Updated: {total_updated}")
    print(f"Skipped: {total_skipped}")
    
    if mode != "dry-run":
        stats = rag_store.get_stats()
        print(f"Total in ChromaDB: {stats['total_chunks']}")
        print(f"Categories: {', '.join(stats['categories'])}")

def main():
    args = parse_args()
    
    # Override docs dir if specified
    if args.docs_dir:
        global RAG_DOCS_DIR
        RAG_DOCS_DIR = os.path.normpath(os.path.join(
            os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
            args.docs_dir
        ))
    
    if not os.path.isdir(RAG_DOCS_DIR):
        print(f"ERROR: RAG documents directory not found: {RAG_DOCS_DIR}")
        sys.exit(1)
    
    print(f"\nRAG documents directory: {RAG_DOCS_DIR}\n")
    
    index_documents(
        mode=args.mode,
        category=args.category,
    )

if __name__ == "__main__":
    main()
