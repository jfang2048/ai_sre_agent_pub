import React from 'react';

type KnowledgeEvidenceRow = {
    id: string;
    title?: string;
    source_path: string;
    source_type?: string;
    knowledge_type?: string;
    case_type?: string;
    summary?: string;
    snippet: string;
    score?: number;
    likely_causes?: string[];
    remediation_steps?: string[];
    commands?: string[];
    signals?: string[];
    tags?: string[];
};

interface KnowledgeEvidencePanelProps {
    title?: string;
    summary?: string;
    confidence?: number;
    evidenceIDs?: string[];
    docs?: KnowledgeEvidenceRow[];
    emptyText?: string;
    testId?: string;
}

export default function KnowledgeEvidencePanel({
    title = 'Knowledge Evidence',
    summary,
    confidence,
    evidenceIDs = [],
    docs = [],
    emptyText = 'No retrieved knowledge evidence for this view.',
    testId,
}: KnowledgeEvidencePanelProps) {
    return (
        <section className="rounded-lg border border-border bg-card p-4" data-testid={testId}>
            <div className="flex flex-wrap items-center justify-between gap-2 mb-3">
                <h3 className="font-semibold">{title}</h3>
                <div className="text-xs text-muted-foreground">
                    hits={docs.length}
                    {typeof confidence === 'number' && ` · confidence=${(confidence * 100).toFixed(0)}%`}
                </div>
            </div>
            {summary && (
                <div className="rounded border border-border bg-background/50 p-3 text-sm text-muted-foreground mb-3">
                    {summary}
                </div>
            )}
            {docs.length === 0 ? (
                <div className="text-sm text-muted-foreground">{emptyText}</div>
            ) : (
                <div className="space-y-2">
                    {docs.map((doc) => (
                        <article key={doc.id} className="rounded border border-border bg-background/50 p-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <div className="text-sm font-medium">{doc.title || doc.source_path}</div>
                                <div className="text-[11px] text-muted-foreground">
                                    {typeof doc.score === 'number' ? `score=${doc.score.toFixed(3)} · ` : ''}
                                    {doc.knowledge_type || doc.case_type || doc.source_type || 'document'}
                                </div>
                            </div>
                            <div className="text-[11px] text-muted-foreground mt-1 break-all">{doc.source_path}</div>
                            {(doc.knowledge_type || doc.case_type) && (
                                <div className="text-[11px] text-muted-foreground mt-2">
                                    {doc.knowledge_type || 'knowledge'}{doc.case_type ? ` · ${doc.case_type}` : ''}
                                </div>
                            )}
                            {doc.summary && (
                                <div className="text-xs text-muted-foreground mt-2">{doc.summary}</div>
                            )}
                            <div className="text-xs text-muted-foreground mt-2 whitespace-pre-wrap">{doc.snippet}</div>
                            {(doc.likely_causes ?? []).length > 0 && (
                                <div className="text-[11px] text-muted-foreground mt-2">
                                    likely causes: {(doc.likely_causes ?? []).slice(0, 2).join(' · ')}
                                </div>
                            )}
                            {(doc.remediation_steps ?? []).length > 0 && (
                                <div className="text-[11px] text-muted-foreground mt-2">
                                    runbook steps: {(doc.remediation_steps ?? []).slice(0, 2).join(' · ')}
                                </div>
                            )}
                            {(doc.signals ?? []).length > 0 && (
                                <div className="text-[11px] text-muted-foreground mt-2">
                                    signals: {(doc.signals ?? []).slice(0, 4).join(' · ')}
                                </div>
                            )}
                            {(doc.commands ?? []).length > 0 && (
                                <div className="text-[11px] text-muted-foreground mt-2">
                                    commands: {(doc.commands ?? []).slice(0, 2).join(' · ')}
                                </div>
                            )}
                            {(doc.tags ?? []).length > 0 && (
                                <div className="text-[11px] text-muted-foreground mt-2">
                                    tags: {(doc.tags ?? []).join(' · ')}
                                </div>
                            )}
                        </article>
                    ))}
                </div>
            )}
            {evidenceIDs.length > 0 && (
                <div className="text-[11px] text-muted-foreground mt-3 break-words">
                    evidence ids: {evidenceIDs.join(' · ')}
                </div>
            )}
        </section>
    );
}
