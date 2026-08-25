"""
__main__.py for CLI module

Usage:
    python -m app.cli.rag_indexer --mode full
    python -m app.cli.rag_indexer --mode incremental
"""
from app.cli.rag_indexer import main

if __name__ == "__main__":
    main()
