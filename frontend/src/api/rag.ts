import { api } from './client';

export interface RAGStats {
    enabled: boolean;
    ready: boolean;
    dataset_path: string;
    index_path: string;
    storage_path: string;
    cache_path: string;
    doc_count: number;
    chunk_count: number;
    source_count: number;
    quarantine_count: number;
    retrieval_mode: string;
    embedding_provider: string;
    embedding_model?: string;
    chunk_size: number;
    chunk_overlap: number;
    max_snippet_len: number;
    last_built_at?: string;
    last_updated_at?: string;
    last_error?: string;
    source_types?: Record<string, number>;
}

export interface RAGSearchHit {
    doc_id: string;
    chunk_id: string;
    score: number;
    source_path: string;
    source_type: string;
    title: string;
    snippet: string;
    tags?: string[];
    metadata?: Record<string, string>;
    timestamp_if_available?: string;
}

export interface RAGQueryResponse {
    query: string;
    hits: RAGSearchHit[];
    retrieval_mode: string;
    latency_ms: number;
    summary?: string;
    confidence?: number;
    retrieval_evidence_ids?: string[];
}

export interface RAGDocument {
    doc_id: string;
    source_path: string;
    source_type: string;
    title: string;
    content: string;
    tags?: string[];
    timestamp_if_available?: string;
    metadata?: Record<string, string>;
}

export interface RAGChunk {
    chunk_id: string;
    doc_id: string;
    source_path: string;
    source_type: string;
    title: string;
    content: string;
    tags?: string[];
    timestamp_if_available?: string;
    metadata?: Record<string, string>;
    chunk_index: number;
    offset_start: number;
    offset_end: number;
    strategy: string;
}

export interface RAGDocumentResponse {
    requested_id: string;
    document: RAGDocument;
    chunks: RAGChunk[];
}

export interface RAGQueryRequest {
    query: string;
    top_k?: number;
}

export async function fetchRAGStatus(): Promise<RAGStats> {
    const { data } = await api.get<RAGStats>('/rag/status');
    return data;
}

export async function queryRAG(payload: RAGQueryRequest): Promise<RAGQueryResponse> {
    const { data } = await api.post<RAGQueryResponse>('/rag/query', payload);
    return data;
}

export async function rebuildRAGIndex(): Promise<RAGStats> {
    const { data } = await api.post<RAGStats>('/rag/index/rebuild');
    return data;
}

export async function updateRAGIndex(): Promise<RAGStats> {
    const { data } = await api.post<RAGStats>('/rag/index/update');
    return data;
}

export async function fetchRAGDocument(id: string): Promise<RAGDocumentResponse> {
    const trimmed = id.trim();
    if (!trimmed) {
        throw new Error('document id is required');
    }
    const { data } = await api.get<RAGDocumentResponse>(`/rag/doc/${encodeURIComponent(trimmed)}`);
    return data;
}
