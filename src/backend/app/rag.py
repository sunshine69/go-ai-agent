"""
RAG Store - Vector database operations for RAG documents
Uses ChromaDB with SQLite backend for persistence.
"""
import os
from typing import List, Dict, Optional, Tuple
from datetime import datetime

# RAG configuration from environment
RAG_ENABLED = os.getenv("RAG_ENABLED", "true").lower() == "true"

# Only import RAG dependencies if RAG is enabled
if RAG_ENABLED:
    import chromadb
    from chromadb.config import Settings as ChromaSettings
    from sentence_transformers import SentenceTransformer

CHROMA_PATH = os.getenv("CHROMA_PATH", "./rag_chroma_db")
RAG_EMBEDDING_MODEL = os.getenv("RAG_EMBEDDING_MODEL", "all-MiniLM-L6-v2")
RAG_CHUNK_SIZE = int(os.getenv("RAG_CHUNK_SIZE", "1500"))
RAG_CHUNK_OVERLAP = int(os.getenv("RAG_CHUNK_OVERLAP", "300"))
RAG_SEARCH_LIMIT = int(os.getenv("RAG_SEARCH_LIMIT", "5"))
RAG_SCORE_THRESHOLD = float(os.getenv("RAG_SCORE_THRESHOLD", "0.25"))


class RAGStore:
    """ChromaDB-based vector store for RAG documents.
    
    When RAG_ENABLED=false, this store is a no-op - it won't try to load
    the embedding model or ChromaDB, so you don't need those dependencies.
    """
    
    def __init__(self, collection_name: str = "rag_documents"):
        if not RAG_ENABLED:
            # RAG disabled - no-op mode
            self._disabled = True
            return
        self._disabled = False
        
        # Load embedding model
        self.embedder = SentenceTransformer(RAG_EMBEDDING_MODEL)
        
        # ChromaDB persistent client
        self.chroma_client = chromadb.PersistentClient(
            path=CHROMA_PATH,
            settings=ChromaSettings(
                anonymized_telemetry=False,
            ),
        )
        
        # Get or create collection
        self.collection = self.chroma_client.get_or_create_collection(
            name=collection_name,
            metadata={
                "hnsw:space": "cosine",
            },
        )
    
    def search(
        self,
        query: str,
        limit: int = RAG_SEARCH_LIMIT,
        category: Optional[str] = None,
    ) -> List[Dict]:
        """Search RAG documents by vector similarity.
        
        Args:
            query: Search query text
            limit: Maximum number of results to return
            category: Optional filter by document category
        
        Returns:
            List of results with content, metadata, and similarity score
        """
        if self._disabled:
            return []
        
        # Generate query embedding
        query_embedding = self.embedder.encode(query).tolist()
        
        # Build filter by category if specified
        where_filter = {"source_category": category} if category else None
        
        # Query ChromaDB - ids are always returned by ChromaDB by default, no need to include
        results = self.collection.query(
            query_embeddings=[query_embedding],
            n_results=limit,
            where=where_filter,
            include=["documents", "metadatas", "distances"],
        )
        
        # Format results
        formatted_results = []
        for i, (doc, metadata, distance, chunk_id) in enumerate(
            zip(
                results["documents"][0],
                results["metadatas"][0],
                results["distances"][0],
                results["ids"][0],
            )
        ):
            # Convert cosine distance to similarity score
            # ChromaDB cosine distance is in range [0, 2], so similarity = 1 - (distance / 2)
            similarity = 1.0 - (distance / 2.0)
            
            # Skip results below threshold
            if similarity < RAG_SCORE_THRESHOLD:
                continue
            
            formatted_results.append({
                "chunk_id": chunk_id,
                "document_title": metadata.get("title", ""),
                "source_file": metadata.get("source_file", ""),
                "source_category": metadata.get("source_category", ""),
                "page_number": metadata.get("page_number", 0),
                "content": doc,
                "similarity_score": similarity,
            })
        
        return formatted_results
    
    def add_documents(self, chunks: List[Dict]) -> int:
        """Add new document chunks to the RAG store.
        
        Args:
            chunks: List of chunk dictionaries with:
                - chunk_id: Unique identifier
                - content: Text content
                - source_file: Source file path
                - source_category: Category (policy/procedure/training/reference)
                - document_type: "pdf" or "md"
                - title: Document title
                - page_number: Page number (for PDFs)
                - checksum: File checksum for incremental indexing
        
        Returns:
            Number of chunks added
        """
        if self._disabled:
            return 0
        
        if not chunks:
            return 0
        
        # Generate embeddings for all chunks at once (batch)
        texts = [chunk["content"] for chunk in chunks]
        embeddings = self.embedder.encode(texts).tolist()
        
        # Prepare IDs and metadata
        ids = [chunk["chunk_id"] for chunk in chunks]
        metadatas = [
            {
                "source_file": chunk["source_file"],
                "source_category": chunk["source_category"],
                "document_type": chunk["document_type"],
                "title": chunk["title"],
                "page_number": chunk.get("page_number", 0),
                "chunk_size": chunk["chunk_size"],
                "last_indexed": datetime.now().isoformat(),
                "checksum": chunk.get("checksum", ""),
            }
            for chunk in chunks
        ]
        
        # Add to ChromaDB
        self.collection.add(
            ids=ids,
            embeddings=embeddings,
            metadatas=metadatas,
            documents=texts,
        )
        
        return len(chunks)
    
    def get_categories(self) -> List[str]:
        """Get all document categories in the RAG store."""
        if self._disabled:
            return []
        results = self.collection.get(include=[])
        categories = set()
        for metadata in results["metadatas"] or []:
            if metadata and "source_category" in metadata:
                categories.add(metadata["source_category"])
        return sorted(categories)
    
    def get_stats(self) -> Dict:
        """Get RAG store statistics."""
        if self._disabled:
            return {
                "total_chunks": 0,
                "categories": [],
                "category_counts": {},
                "embedding_dimensions": 0,
                "embedding_model": RAG_EMBEDDING_MODEL,
                "disabled": True,
            }
        total = self.collection.count()
        categories = self.get_categories()
        
        # Count by category
        category_counts = {}
        results = self.collection.get(include=["metadatas"])
        for metadata in results["metadatas"] or []:
            if metadata and "source_category" in metadata:
                cat = metadata["source_category"]
                category_counts[cat] = category_counts.get(cat, 0) + 1
        
        return {
            "total_chunks": total,
            "categories": categories,
            "category_counts": category_counts,
            "embedding_dimensions": 384,  # all-MiniLM-L6-v2
            "embedding_model": RAG_EMBEDDING_MODEL,
        }
    
    def delete_collection(self) -> None:
        """Delete the entire collection (for reset mode)."""
        if self._disabled:
            return
        try:
            self.chroma_client.delete_collection(name="rag_documents")
            # Recreate empty collection
            self.collection = self.chroma_client.get_or_create_collection(
                name="rag_documents",
                metadata={"hnsw:space": "cosine"},
            )
        except Exception as e:
            raise RuntimeError(f"Failed to delete collection: {e}")

# Singleton instance for use in routes
rag_store = RAGStore()
