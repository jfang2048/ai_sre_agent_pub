import React from 'react';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import KnowledgePage from '../KnowledgePage';
import {
    fetchRAGDocument,
    fetchRAGStatus,
    queryRAG,
    rebuildRAGIndex,
    updateRAGIndex,
} from '@/api/rag';
import { renderWithClient } from '@/test/utils';

vi.mock('@/api/rag', () => ({
    fetchRAGDocument: vi.fn(),
    fetchRAGStatus: vi.fn(),
    queryRAG: vi.fn(),
    rebuildRAGIndex: vi.fn(),
    updateRAGIndex: vi.fn(),
}));

const fetchRAGStatusMock = vi.mocked(fetchRAGStatus);
const queryRAGMock = vi.mocked(queryRAG);
const fetchRAGDocumentMock = vi.mocked(fetchRAGDocument);
const rebuildRAGIndexMock = vi.mocked(rebuildRAGIndex);
const updateRAGIndexMock = vi.mocked(updateRAGIndex);

describe('KnowledgePage', () => {
    beforeEach(() => {
        vi.clearAllMocks();

        const status = {
            enabled: true,
            ready: true,
            dataset_path: './dataset',
            index_path: './data/agent/rag/index.json',
            storage_path: './data/agent/rag',
            cache_path: './data/agent/rag/cache',
            doc_count: 12,
            chunk_count: 48,
            source_count: 6,
            quarantine_count: 1,
            retrieval_mode: 'hybrid',
            embedding_provider: 'local',
            chunk_size: 900,
            chunk_overlap: 120,
            max_snippet_len: 280,
            source_types: {
                markdown: 4,
                jsonl: 1,
                csv: 1,
            },
            knowledge_types: {
                runbook: 4,
                historical_incident: 3,
                question_pattern: 2,
            },
            case_types: {
                runbook: 4,
                historical_incident: 3,
                operational_qa: 2,
            },
        };
        fetchRAGStatusMock.mockResolvedValue(status);

        queryRAGMock.mockResolvedValue({
            query: 'timeout deployment runbook',
            normalized_query: 'timeout deployment runbook remediation troubleshooting',
            intent: 'runbook',
            retrieval_mode: 'hybrid',
            latency_ms: 12,
            summary: 'retrieved 1 knowledge hits across 1 documents (runbook=1)',
            confidence: 0.78,
            retrieval_evidence_ids: ['rag-1'],
            hits: [
                {
                    evidence_id: 'rag-1',
                    doc_id: 'doc-1',
                    chunk_id: 'chunk-1',
                    score: 0.91,
                    source_path: 'cases/timeout-runbook.md',
                    source_type: 'markdown',
                    knowledge_type: 'runbook',
                    case_type: 'runbook',
                    title: 'Timeout Runbook',
                    summary: 'Check retry rates and credentials after deployment.',
                    snippet: 'Inspect retries, deployment timing, and cache credentials.',
                    likely_causes: ['stale credential after rollout'],
                    remediation_steps: ['inspect retry rate', 'validate credentials'],
                    commands: ['kubectl rollout history deploy/checkout'],
                    signals: ['deployment', 'network', 'cache'],
                    section_type: 'remediation',
                    tags: ['runbook', 'deployment'],
                    metadata: { service: 'checkout' },
                },
            ],
        });

        fetchRAGDocumentMock.mockResolvedValue({
            requested_id: 'chunk-1',
            document: {
                doc_id: 'doc-1',
                source_path: 'cases/timeout-runbook.md',
                source_type: 'markdown',
                knowledge_type: 'runbook',
                case_type: 'runbook',
                title: 'Timeout Runbook',
                summary: 'Check retry rates and credentials after deployment.',
                content: 'Inspect retries and validate credentials.',
                likely_causes: ['stale credential after rollout'],
                remediation_steps: ['inspect retry rate', 'validate credentials'],
                commands: ['kubectl rollout history deploy/checkout'],
                signals: ['deployment', 'network', 'cache'],
                tags: ['runbook', 'deployment'],
                metadata: { service: 'checkout' },
            },
            chunks: [
                {
                    chunk_id: 'chunk-1',
                    doc_id: 'doc-1',
                    source_path: 'cases/timeout-runbook.md',
                    source_type: 'markdown',
                    knowledge_type: 'runbook',
                    case_type: 'runbook',
                    title: 'Timeout Runbook',
                    summary: 'Check retry rates and credentials after deployment.',
                    content: 'Inspect retries and validate credentials.',
                    remediation_steps: ['inspect retry rate', 'validate credentials'],
                    commands: ['kubectl rollout history deploy/checkout'],
                    signals: ['deployment', 'network', 'cache'],
                    chunk_index: 1,
                    offset_start: 0,
                    offset_end: 120,
                    strategy: 'case',
                    section_type: 'remediation',
                },
            ],
        });

        rebuildRAGIndexMock.mockResolvedValue(status);
        updateRAGIndexMock.mockResolvedValue(status);
    });

    it('renders RAG filters and passes them to the query API', async () => {
        renderWithClient(<KnowledgePage />);

        await waitFor(() => expect(screen.getByText(/Knowledge Base/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getByLabelText(/Knowledge type/i)).toBeInTheDocument());

        fireEvent.change(screen.getByLabelText(/Knowledge type/i), { target: { value: 'runbook' } });
        fireEvent.change(screen.getByLabelText(/Case type/i), { target: { value: 'runbook' } });
        fireEvent.change(screen.getByLabelText(/Source type/i), { target: { value: 'markdown' } });
        fireEvent.change(screen.getByPlaceholderText(/Search for incidents, runbooks, deployment errors/i), {
            target: { value: 'timeout deployment runbook' },
        });
        fireEvent.change(screen.getByLabelText(/Intent/i), { target: { value: 'runbook' } });
        fireEvent.click(screen.getByRole('button', { name: /^Query$/ }));

        await waitFor(() => {
            expect(queryRAGMock).toHaveBeenCalledWith({
                query: 'timeout deployment runbook',
                top_k: 8,
                intent: 'runbook',
                knowledge_types: ['runbook'],
                case_types: ['runbook'],
                source_types: ['markdown'],
            });
        });

        await waitFor(() => expect(screen.getByText(/Retrieved Operational Knowledge/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getAllByText(/Timeout Runbook/i).length).toBeGreaterThan(0));
        await waitFor(() => expect(screen.getByText(/Indexed Case Types/i)).toBeInTheDocument());
        await waitFor(() => expect(screen.getAllByText(/runbook/i).length).toBeGreaterThan(0));
        await waitFor(() => expect(screen.getAllByText(/knowledge=runbook/i).length).toBeGreaterThan(0));
    });
});
