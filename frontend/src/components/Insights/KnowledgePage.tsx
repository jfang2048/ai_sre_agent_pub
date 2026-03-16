import React, { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Database, FileSearch, RefreshCcw, Search, Wrench } from 'lucide-react';
import {
    fetchRAGDocument,
    fetchRAGStatus,
    queryRAG,
    rebuildRAGIndex,
    type RAGSearchHit,
    updateRAGIndex,
} from '@/api/rag';

export default function KnowledgePage() {
    const queryClient = useQueryClient();
    const [queryText, setQueryText] = useState('timeout deployment runbook');
    const [intent, setIntent] = useState('general');
    const [knowledgeTypeFilter, setKnowledgeTypeFilter] = useState('all');
    const [caseTypeFilter, setCaseTypeFilter] = useState('all');
    const [sourceTypeFilter, setSourceTypeFilter] = useState('all');
    const [selectedID, setSelectedID] = useState('');

    const statusQuery = useQuery({
        queryKey: ['rag-status'],
        queryFn: fetchRAGStatus,
        refetchInterval: 15000,
    });

    const searchMutation = useMutation({
        mutationFn: () =>
            queryRAG({
                query: queryText.trim(),
                top_k: 8,
                intent,
                knowledge_types: knowledgeTypeFilter === 'all' ? undefined : [knowledgeTypeFilter],
                case_types: caseTypeFilter === 'all' ? undefined : [caseTypeFilter],
                source_types: sourceTypeFilter === 'all' ? undefined : [sourceTypeFilter],
            }),
    });

    const rebuildMutation = useMutation({
        mutationFn: rebuildRAGIndex,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['rag-status'] });
        },
    });

    const updateMutation = useMutation({
        mutationFn: updateRAGIndex,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['rag-status'] });
        },
    });

    const hits = searchMutation.data?.hits ?? [];
    const knowledgeTypeRows = useMemo(() => {
        const counts: Record<string, number> = {};
        hits.forEach((hit) => {
            const key = hit.knowledge_type || 'reference';
            counts[key] = (counts[key] ?? 0) + 1;
        });
        return Object.entries(counts).sort((left, right) => right[1] - left[1]);
    }, [hits]);

    useEffect(() => {
        if (hits.length === 0) {
            setSelectedID('');
            return;
        }
        if (!selectedID || !hits.some((hit) => hit.chunk_id === selectedID || hit.doc_id === selectedID)) {
            setSelectedID(hits[0].chunk_id);
        }
    }, [hits, selectedID]);

    const selectedHit = useMemo(() => {
        if (!selectedID) {
            return hits[0];
        }
        return hits.find((hit) => hit.chunk_id === selectedID || hit.doc_id === selectedID) ?? hits[0];
    }, [hits, selectedID]);

    const documentQuery = useQuery({
        queryKey: ['rag-document', selectedHit?.chunk_id, selectedHit?.doc_id],
        queryFn: async () => fetchRAGDocument(selectedHit?.chunk_id || selectedHit?.doc_id || ''),
        enabled: Boolean(selectedHit?.chunk_id || selectedHit?.doc_id),
        retry: false,
    });

    const sourceTypeRows = Object.entries(statusQuery.data?.source_types ?? {}).sort((left, right) => right[1] - left[1]);
    const caseTypeRows = Object.entries(statusQuery.data?.case_types ?? {}).sort((left, right) => right[1] - left[1]);
    const availableKnowledgeTypes = useMemo(
        () => Object.keys(statusQuery.data?.knowledge_types ?? {}).sort((left, right) => left.localeCompare(right)),
        [statusQuery.data?.knowledge_types],
    );
    const availableCaseTypes = useMemo(
        () => Object.keys(statusQuery.data?.case_types ?? {}).sort((left, right) => left.localeCompare(right)),
        [statusQuery.data?.case_types],
    );
    const availableSourceTypes = useMemo(
        () => Object.keys(statusQuery.data?.source_types ?? {}).sort((left, right) => left.localeCompare(right)),
        [statusQuery.data?.source_types],
    );
    const errorMessage = String(
        statusQuery.data?.last_error ||
        rebuildMutation.error ||
        updateMutation.error ||
        searchMutation.error ||
        ''
    ).trim();

    return (
        <div className="space-y-4" data-testid="knowledge-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                        <h2 className="text-lg font-semibold flex items-center gap-2">
                            <Database className="w-5 h-5 text-cyan-300" />
                            Knowledge Base
                        </h2>
                        <p className="text-xs text-muted-foreground mt-1">
                            Dataset-backed operational knowledge engine with local-first retrieval, normalized incident and runbook records, and source-linked evidence.
                        </p>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                        <button
                            type="button"
                            className="inline-flex items-center gap-1 rounded border border-border bg-background px-3 py-2 text-sm hover:bg-muted/60 disabled:opacity-60"
                            onClick={() => updateMutation.mutate()}
                            disabled={updateMutation.isLoading}
                        >
                            <RefreshCcw className="w-4 h-4" />
                            {updateMutation.isLoading ? 'Updating…' : 'Update Index'}
                        </button>
                        <button
                            type="button"
                            className="inline-flex items-center gap-1 rounded border border-border bg-background px-3 py-2 text-sm hover:bg-muted/60 disabled:opacity-60"
                            onClick={() => rebuildMutation.mutate()}
                            disabled={rebuildMutation.isLoading}
                        >
                            <Wrench className="w-4 h-4" />
                            {rebuildMutation.isLoading ? 'Rebuilding…' : 'Rebuild Index'}
                        </button>
                    </div>
                </div>
            </section>

            <div className="grid grid-cols-1 md:grid-cols-5 gap-3">
                <StatusCard label="Ready" value={statusQuery.data?.ready ? 'YES' : 'NO'} detail={statusQuery.data?.enabled ? 'enabled' : 'disabled'} />
                <StatusCard label="Docs" value={String(statusQuery.data?.doc_count ?? 0)} detail={statusQuery.data?.dataset_path ?? 'n/a'} />
                <StatusCard label="Chunks" value={String(statusQuery.data?.chunk_count ?? 0)} detail={statusQuery.data?.index_path ?? 'n/a'} />
                <StatusCard label="Sources" value={String(statusQuery.data?.source_count ?? 0)} detail={`quarantine=${statusQuery.data?.quarantine_count ?? 0}`} />
                <StatusCard label="Mode" value={statusQuery.data?.retrieval_mode ?? 'n/a'} detail={statusQuery.data?.embedding_provider ?? 'n/a'} />
            </div>

            <section className="rounded-lg border border-border bg-card p-4">
                <form
                    className="space-y-3"
                    onSubmit={(event) => {
                        event.preventDefault();
                        if (!queryText.trim()) {
                            return;
                        }
                        searchMutation.mutate();
                    }}
                >
                    <div className="text-sm font-semibold flex items-center gap-2">
                        <Search className="w-4 h-4" />
                        Search Knowledge
                    </div>
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-2" data-testid="knowledge-filters">
                        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                            Knowledge type
                            <select
                                aria-label="Knowledge type"
                                value={knowledgeTypeFilter}
                                onChange={(event) => setKnowledgeTypeFilter(event.target.value)}
                                className="rounded border border-border bg-background px-3 py-2 text-sm text-foreground"
                            >
                                <option value="all">All knowledge types</option>
                                {availableKnowledgeTypes.map((value) => (
                                    <option key={value} value={value}>
                                        {value}
                                    </option>
                                ))}
                            </select>
                        </label>
                        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                            Case type
                            <select
                                aria-label="Case type"
                                value={caseTypeFilter}
                                onChange={(event) => setCaseTypeFilter(event.target.value)}
                                className="rounded border border-border bg-background px-3 py-2 text-sm text-foreground"
                            >
                                <option value="all">All case types</option>
                                {availableCaseTypes.map((value) => (
                                    <option key={value} value={value}>
                                        {value}
                                    </option>
                                ))}
                            </select>
                        </label>
                        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                            Source type
                            <select
                                aria-label="Source type"
                                value={sourceTypeFilter}
                                onChange={(event) => setSourceTypeFilter(event.target.value)}
                                className="rounded border border-border bg-background px-3 py-2 text-sm text-foreground"
                            >
                                <option value="all">All source types</option>
                                {availableSourceTypes.map((value) => (
                                    <option key={value} value={value}>
                                        {value}
                                    </option>
                                ))}
                            </select>
                        </label>
                    </div>
                    <div className="flex flex-col lg:flex-row gap-2">
                        <input
                            value={queryText}
                            onChange={(event) => setQueryText(event.target.value)}
                            className="flex-1 rounded border border-border bg-background px-3 py-2 text-sm"
                            placeholder="Search for incidents, runbooks, deployment errors, architecture notes..."
                        />
                        <select
                            aria-label="Intent"
                            value={intent}
                            onChange={(event) => setIntent(event.target.value)}
                            className="rounded border border-border bg-background px-3 py-2 text-sm"
                        >
                            <option value="general">General</option>
                            <option value="runbook">Runbook</option>
                            <option value="historical_incident">Historical Incident</option>
                            <option value="rca">RCA</option>
                            <option value="joint_risk">Joint Risk</option>
                            <option value="recommendation">Recommendation</option>
                            <option value="security">Security</option>
                        </select>
                        <button
                            type="submit"
                            className="rounded bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
                            disabled={searchMutation.isLoading || !queryText.trim()}
                        >
                            {searchMutation.isLoading ? 'Searching…' : 'Query'}
                        </button>
                    </div>
                    {searchMutation.data && (
                        <div className="text-xs text-muted-foreground">
                            mode={searchMutation.data.retrieval_mode} · intent={searchMutation.data.intent || intent} · latency={searchMutation.data.latency_ms}ms · hits={hits.length}
                            {searchMutation.data.summary ? ` · ${searchMutation.data.summary}` : ''}
                            {searchMutation.data.normalized_query ? ` · query=${searchMutation.data.normalized_query}` : ''}
                            {knowledgeTypeFilter !== 'all' ? ` · knowledge=${knowledgeTypeFilter}` : ''}
                            {caseTypeFilter !== 'all' ? ` · case=${caseTypeFilter}` : ''}
                            {sourceTypeFilter !== 'all' ? ` · source=${sourceTypeFilter}` : ''}
                        </div>
                    )}
                    {errorMessage && (
                        <div className="rounded border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-200">
                            {errorMessage}
                        </div>
                    )}
                </form>
            </section>

            <div className="grid grid-cols-1 xl:grid-cols-[1.1fr_0.9fr] gap-4">
                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3 flex items-center gap-2">
                        <FileSearch className="w-4 h-4" />
                        Retrieved Operational Knowledge
                    </h3>
                    {hits.length === 0 ? (
                        <div className="text-sm text-muted-foreground">Run a query to inspect retrieved runbooks, incident cases, and knowledge fragments.</div>
                    ) : (
                        <div className="space-y-2">
                            {hits.map((hit) => (
                                <button
                                    key={hit.chunk_id}
                                    type="button"
                                    onClick={() => setSelectedID(hit.chunk_id)}
                                    className={`w-full rounded border p-3 text-left transition-colors ${
                                        selectedHit?.chunk_id === hit.chunk_id
                                            ? 'border-cyan-300 bg-cyan-300/10'
                                            : 'border-border bg-background/50 hover:bg-muted/40'
                                    }`}
                                >
                                    <HitRow hit={hit} />
                                </button>
                            ))}
                        </div>
                    )}
                </section>

                <section className="rounded-lg border border-border bg-card p-4">
                    <h3 className="font-semibold mb-3">Document Detail</h3>
                    {!selectedHit ? (
                        <div className="text-sm text-muted-foreground">Select a hit to inspect the backing document and chunks.</div>
                    ) : documentQuery.isLoading ? (
                        <div className="text-sm text-muted-foreground">Loading document…</div>
                    ) : documentQuery.data ? (
                        <div className="space-y-3">
                            <article className="rounded border border-border bg-background/50 p-3">
                                <div className="text-sm font-medium">{documentQuery.data.document.title || selectedHit.title || selectedHit.source_path}</div>
                                <div className="text-[11px] text-muted-foreground mt-1 break-all">{documentQuery.data.document.source_path}</div>
                                <div className="text-xs text-muted-foreground mt-2">
                                    type={documentQuery.data.document.source_type} · knowledge={documentQuery.data.document.knowledge_type || 'reference'} · case={documentQuery.data.document.case_type || 'reference'} · chunks={documentQuery.data.chunks.length}
                                </div>
                                {documentQuery.data.document.summary && (
                                    <div className="text-xs text-muted-foreground mt-2">{documentQuery.data.document.summary}</div>
                                )}
                                {(documentQuery.data.document.likely_causes ?? []).length > 0 && (
                                    <div className="text-[11px] text-muted-foreground mt-2">
                                        likely causes: {(documentQuery.data.document.likely_causes ?? []).slice(0, 3).join(' · ')}
                                    </div>
                                )}
                                {(documentQuery.data.document.remediation_steps ?? []).length > 0 && (
                                    <div className="text-[11px] text-muted-foreground mt-2">
                                        remediation: {(documentQuery.data.document.remediation_steps ?? []).slice(0, 3).join(' · ')}
                                    </div>
                                )}
                                {(documentQuery.data.document.tags ?? []).length > 0 && (
                                    <div className="text-[11px] text-muted-foreground mt-2">
                                        tags: {(documentQuery.data.document.tags ?? []).join(' · ')}
                                    </div>
                                )}
                            </article>
                            <div className="space-y-2 max-h-[420px] overflow-auto pr-1">
                                {documentQuery.data.chunks.slice(0, 6).map((chunk) => (
                                    <article key={chunk.chunk_id} className="rounded border border-border bg-background/50 p-3">
                                        <div className="text-xs text-muted-foreground">
                                            chunk={chunk.chunk_index} · strategy={chunk.strategy} · section={chunk.section_type || 'body'} · offsets={chunk.offset_start}-{chunk.offset_end}
                                        </div>
                                        {chunk.summary && <div className="text-xs text-muted-foreground mt-2">{chunk.summary}</div>}
                                        <div className="text-sm mt-2 whitespace-pre-wrap">{chunk.content}</div>
                                    </article>
                                ))}
                            </div>
                        </div>
                    ) : (
                        <div className="text-sm text-muted-foreground">Document lookup returned no detail.</div>
                    )}
                </section>
            </div>

            <section className="rounded-lg border border-border bg-card p-4">
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                    <div>
                        <h3 className="font-semibold mb-3">Knowledge Mix</h3>
                        {knowledgeTypeRows.length === 0 ? (
                            <div className="text-sm text-muted-foreground">Run a query to inspect matched runbooks, incident cases, and references.</div>
                        ) : (
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-2 mb-4">
                                {knowledgeTypeRows.map(([knowledgeType, count]) => (
                                    <article key={knowledgeType} className="rounded border border-border bg-background/50 p-3">
                                        <div className="text-sm font-medium">{knowledgeType}</div>
                                        <div className="text-xs text-muted-foreground mt-1">{count} matches</div>
                                    </article>
                                ))}
                            </div>
                        )}
                        <h3 className="font-semibold mb-3">Indexed Case Types</h3>
                        {caseTypeRows.length === 0 ? (
                            <div className="text-sm text-muted-foreground">No indexed case types yet.</div>
                        ) : (
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                                {caseTypeRows.map(([caseType, count]) => (
                                    <article key={caseType} className="rounded border border-border bg-background/50 p-3">
                                        <div className="text-sm font-medium">{caseType}</div>
                                        <div className="text-xs text-muted-foreground mt-1">{count} documents</div>
                                    </article>
                                ))}
                            </div>
                        )}
                    </div>
                    <div>
                        <h3 className="font-semibold mb-3">Indexed Source Types</h3>
                        {sourceTypeRows.length === 0 ? (
                            <div className="text-sm text-muted-foreground">No indexed source types yet.</div>
                        ) : (
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                                {sourceTypeRows.map(([sourceType, count]) => (
                                    <article key={sourceType} className="rounded border border-border bg-background/50 p-3">
                                        <div className="text-sm font-medium">{sourceType}</div>
                                        <div className="text-xs text-muted-foreground mt-1">{count} sources</div>
                                    </article>
                                ))}
                            </div>
                        )}
                    </div>
                </div>
            </section>
        </div>
    );
}

function StatusCard({ label, value, detail }: { label: string; value: string; detail: string }) {
    return (
        <article className="rounded-lg border border-border bg-card p-3">
            <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
            <div className="text-lg font-semibold mt-2">{value}</div>
            <div className="text-[11px] text-muted-foreground mt-1 break-all">{detail}</div>
        </article>
    );
}

function HitRow({ hit }: { hit: RAGSearchHit }) {
    return (
        <article>
            <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="text-sm font-medium">{hit.title || hit.source_path}</div>
                <div className="text-[11px] text-muted-foreground">score={hit.score.toFixed(3)}</div>
            </div>
            {(hit.knowledge_type || hit.case_type) && (
                <div className="text-[11px] text-muted-foreground mt-1">
                    {hit.knowledge_type || 'reference'}{hit.case_type ? ` · ${hit.case_type}` : ''}
                </div>
            )}
            <div className="text-[11px] text-muted-foreground mt-1 break-all">{hit.source_path}</div>
            {hit.summary && <div className="text-xs text-muted-foreground mt-2">{hit.summary}</div>}
            <div className="text-xs text-muted-foreground mt-2 whitespace-pre-wrap">{hit.snippet}</div>
            {(hit.likely_causes ?? []).length > 0 && (
                <div className="text-[11px] text-muted-foreground mt-2">
                    likely causes: {(hit.likely_causes ?? []).slice(0, 2).join(' · ')}
                </div>
            )}
            {(hit.remediation_steps ?? []).length > 0 && (
                <div className="text-[11px] text-muted-foreground mt-2">
                    runbook steps: {(hit.remediation_steps ?? []).slice(0, 2).join(' · ')}
                </div>
            )}
        </article>
    );
}
