"""Context retrieval for runbooks/docs with Haystack-backed BM25 indexing."""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass
from threading import RLock
from typing import List, Optional, Sequence, Set

from .structured_log import emit

try:  # Optional dependency for production retrieval backend.
    from haystack import Document
    from haystack.components.retrievers.in_memory import InMemoryBM25Retriever
    from haystack.document_stores.in_memory import InMemoryDocumentStore
except Exception:  # pragma: no cover - exercised when dependency is absent
    Document = None
    InMemoryBM25Retriever = None
    InMemoryDocumentStore = None


@dataclass
class DocumentChunk:
    """One document chunk stored in the fallback lexical index."""

    path: str
    content: str
    tokens: Set[str]


@dataclass
class ContextSnippet:
    """Search result snippet."""

    path: str
    content: str
    score: float


class ContextStore:
    """In-process context index with Haystack BM25 backend and deterministic fallback."""

    def __init__(
        self,
        paths: Sequence[str],
        max_chars: int,
        extensions: Sequence[str],
        logger: logging.Logger,
    ) -> None:
        self._paths = [p for p in paths if p]
        self._max_chars = max(300, max_chars)
        self._extensions = {ext.lower() for ext in extensions}
        self._logger = logger
        self._lock = RLock()

        self._chunks: List[DocumentChunk] = []
        self._document_store: Optional[InMemoryDocumentStore] = None
        self._retriever: Optional[InMemoryBM25Retriever] = None

        self._backend = "haystack_bm25" if _haystack_available() else "lexical_fallback"
        self.rebuild()

    @property
    def backend(self) -> str:
        return self._backend

    def rebuild(self) -> None:
        """Rebuild index from configured paths."""
        file_chunks: List[tuple[str, str]] = []
        files_loaded = 0

        for path in self._paths:
            if os.path.isdir(path):
                for root, _, files in os.walk(path):
                    for filename in files:
                        full = os.path.join(root, filename)
                        if self._allowed(full):
                            contents = self._read_chunks(full)
                            if contents:
                                files_loaded += 1
                                file_chunks.extend((full, content) for content in contents)
            elif os.path.isfile(path) and self._allowed(path):
                contents = self._read_chunks(path)
                if contents:
                    files_loaded += 1
                    file_chunks.extend((path, content) for content in contents)

        if _haystack_available():
            self._rebuild_haystack(file_chunks)
        else:
            self._rebuild_lexical(file_chunks)

        emit(
            self._logger,
            "context_store_rebuilt",
            backend=self._backend,
            files_loaded=files_loaded,
            chunk_count=len(file_chunks),
            max_chars=self._max_chars,
        )

    def search(self, query: str, limit: int = 4) -> List[ContextSnippet]:
        """Return top snippets for a query."""
        query = query.strip()
        if not query:
            return []

        if self._backend == "haystack_bm25":
            return self._search_haystack(query, limit)
        return self._search_lexical(query, limit)

    def _rebuild_haystack(self, file_chunks: List[tuple[str, str]]) -> None:
        assert Document is not None
        assert InMemoryDocumentStore is not None
        assert InMemoryBM25Retriever is not None

        store = InMemoryDocumentStore()
        docs = [
            Document(content=content, meta={"path": path})
            for path, content in file_chunks
            if content.strip()
        ]

        if docs:
            store.write_documents(docs)

        retriever = InMemoryBM25Retriever(document_store=store)

        with self._lock:
            self._document_store = store
            self._retriever = retriever
            self._chunks = []
            self._backend = "haystack_bm25"

    def _rebuild_lexical(self, file_chunks: List[tuple[str, str]]) -> None:
        chunks: List[DocumentChunk] = []
        for path, content in file_chunks:
            chunks.append(DocumentChunk(path=path, content=content, tokens=set(_tokenize(content))))

        with self._lock:
            self._chunks = chunks
            self._document_store = None
            self._retriever = None
            self._backend = "lexical_fallback"

    def _search_haystack(self, query: str, limit: int) -> List[ContextSnippet]:
        with self._lock:
            retriever = self._retriever

        if retriever is None:
            return []

        try:
            result = retriever.run(query=query, top_k=max(1, limit))
        except Exception as exc:  # pylint: disable=broad-except
            emit(
                self._logger,
                "context_retrieval_failed",
                level="warning",
                backend="haystack_bm25",
                error=str(exc),
            )
            return []

        snippets: List[ContextSnippet] = []
        for doc in result.get("documents", []):
            content = getattr(doc, "content", "") or ""
            meta = getattr(doc, "meta", {}) or {}
            raw_score = getattr(doc, "score", 0.0)
            try:
                score = float(raw_score)
            except (TypeError, ValueError):
                score = 0.0
            snippets.append(
                ContextSnippet(
                    path=str(meta.get("path", "")),
                    content=content,
                    score=score,
                )
            )
        return snippets

    def _search_lexical(self, query: str, limit: int) -> List[ContextSnippet]:
        tokens = _tokenize(query)
        if not tokens:
            return []

        with self._lock:
            chunks = list(self._chunks)

        scored: List[ContextSnippet] = []
        for chunk in chunks:
            score = sum(1 for token in tokens if token in chunk.tokens)
            if score > 0:
                scored.append(
                    ContextSnippet(path=chunk.path, content=chunk.content, score=float(score))
                )

        scored.sort(key=lambda item: item.score, reverse=True)
        return scored[: max(1, limit)]

    def _allowed(self, path: str) -> bool:
        lower = path.lower()
        return any(lower.endswith(ext) for ext in self._extensions)

    def _read_chunks(self, path: str) -> List[str]:
        try:
            with open(path, "r", encoding="utf-8", errors="ignore") as handle:
                text = handle.read()
        except OSError as exc:
            emit(
                self._logger, "context_file_read_failed", level="warning", path=path, error=str(exc)
            )
            return []

        if not text.strip():
            return []

        return _chunk_text(text, self._max_chars)


def _haystack_available() -> bool:
    return (
        Document is not None
        and InMemoryDocumentStore is not None
        and InMemoryBM25Retriever is not None
    )


def _chunk_text(text: str, max_chars: int) -> List[str]:
    chunks: List[str] = []
    current: List[str] = []
    current_size = 0
    for line in text.splitlines():
        line_size = len(line) + 1
        if current and current_size + line_size > max_chars:
            chunks.append("\n".join(current).strip())
            current = []
            current_size = 0
        current.append(line)
        current_size += line_size
    if current:
        chunks.append("\n".join(current).strip())
    return [chunk for chunk in chunks if chunk]


def _tokenize(text: str) -> List[str]:
    separators = ".,:;()[]{}<>|/\\\n\t\"'"
    cleaned = text.lower()
    for char in separators:
        cleaned = cleaned.replace(char, " ")
    return [part for part in cleaned.split(" ") if len(part) >= 3]
