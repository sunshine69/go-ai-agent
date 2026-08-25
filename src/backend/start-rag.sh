#!/bin/bash

# RAG Document Indexer
# Usage:
#   ./start-rag.sh [mode] [category]
#   where mode: full, incremental, reset, dry-run (default: incremental)
#   where category: policy, procedure, training, reference (optional)

MODE=${1:-incremental}
CAT=${2}

# Change to backend directory so app module is importable
cd "$(dirname "$0")" || exit 1

if [ -z "$CAT" ]; then
    # No category specified - index all categories
    python -m app.cli.rag_indexer --mode "$MODE"
else
    # Category specified - index only that category
    python -m app.cli.rag_indexer --category "$CAT" --mode "$MODE"
fi

# Examples:
# Full indexing of all documents:
#   ./start-rag.sh full
#
# Incremental indexing (only new/modified files):
#   ./start-rag.sh incremental
#
# Index specific category:
#   ./start-rag.sh incremental policy
#
# Delete and re-index all:
#   ./start-rag.sh reset
#
# Dry run (show what would be indexed):
#   ./start-rag.sh dry-run
