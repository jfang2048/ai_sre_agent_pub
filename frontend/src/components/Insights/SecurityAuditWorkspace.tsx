import { useState, type FormEvent } from 'react';
import { isAxiosError } from 'axios';
import { useMutation } from '@tanstack/react-query';
import {
    explainAudit,
    fetchAuditAnalysis,
    importAuditReport,
    type AuditFormat,
    type AuditScope,
} from '@/api/securityAudit';

const maxReportBytes = 4 * 1024 * 1024;
const inputClass = 'block w-full rounded border border-border bg-background px-2 py-1 text-sm';
const buttonClass = 'rounded border border-border px-3 py-1.5 text-sm font-medium hover:bg-muted/40 disabled:opacity-50';

function auditError(error: unknown, fallback: string): string {
    if (isAxiosError(error)) {
        switch (error.response?.status) {
            case 400:
                return 'Check the asset ID, report format, and time range.';
            case 403:
                return 'Your credentials do not grant access to this operation.';
            case 409:
                return 'The audit evidence store has reached capacity.';
            case 413:
                return 'The report exceeds the server upload limit.';
            case 503:
                return 'The security audit service is unavailable. Check the controller’s audit configuration.';
        }
    }
    return fallback;
}

function EvidenceLinks({ ids, visibleIDs }: { ids: string[]; visibleIDs: Set<string> }) {
    if (!ids?.length) {
        return null;
    }
    return (
        <ul aria-label="Evidence references" className="mt-1 flex flex-wrap gap-x-2 gap-y-1 font-mono text-xs break-all">
            {ids.map((id) => (
                <li key={id}>
                    {visibleIDs.has(id) ? (
                        <a className="text-sky-300 underline" href={`#security-audit-record-${encodeURIComponent(id)}`}>{id}</a>
                    ) : id}
                </li>
            ))}
        </ul>
    );
}

function formatAuditDate(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export default function SecurityAuditWorkspace() {
    const [asset, setAsset] = useState('');
    const [window, setWindow] = useState('168h');
    const [format, setFormat] = useState<AuditFormat>('sarif');
    const [report, setReport] = useState<File>();
    const [formError, setFormError] = useState('');
    const [observedAt, setObservedAt] = useState('');
    const analysis = useMutation({ mutationFn: fetchAuditAnalysis });
    const explanation = useMutation({ mutationFn: explainAudit });
    const upload = useMutation({
        mutationFn: async (input: { asset: string; file: File; format: AuditFormat; window: string; observedAt?: string }) => {
            const report = await input.file.text();
            return importAuditReport({ asset: input.asset, format: input.format, report, observed_at: input.observedAt });
        },
        onSuccess: (_result, input) => analysis.mutate({ asset: input.asset, window: input.window }),
    });

    const activeScope: AuditScope = { asset: asset.trim(), window };
    const busy = analysis.isLoading || explanation.isLoading || upload.isLoading;
    const records = analysis.data?.records ?? [];
    const visibleIDs = new Set(records.map((record) => record.id));
    const correlations = analysis.data?.correlations ?? [];
    const anomalies = analysis.data?.anomalies ?? [];
    const explanationCurrent = activeScope.asset === analysis.variables?.asset && window === analysis.variables?.window;

    function loadAudit(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        explanation.reset();
        analysis.mutate(activeScope);
    }

    function submitReport(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setFormError('');
        upload.reset();
        if (!activeScope.asset || !report) {
            setFormError('Enter an asset ID and select a report file.');
            return;
        }
        if (!report.size || report.size > maxReportBytes) {
            setFormError('Select a non-empty report up to 4 MiB.');
            return;
        }
        explanation.reset();
        upload.mutate({
            asset: activeScope.asset,
            file: report,
            format,
            window,
            observedAt: format === 'sarif' && observedAt ? new Date(observedAt).toISOString() : undefined,
        });
    }

    return (
        <section className="space-y-4 rounded-lg border border-border bg-card p-4" aria-labelledby="security-audit-title">
            <h3 id="security-audit-title" className="font-semibold">DevSecOps Audit</h3>
            <div className="grid gap-4 lg:grid-cols-2">
                <form onSubmit={loadAudit} className="space-y-2">
                    <label className="block space-y-1 text-xs text-muted-foreground">
                        <span>Asset ID</span>
                        <input className={inputClass} maxLength={128} value={asset} onChange={(event) => setAsset(event.target.value)} placeholder="All assets" />
                    </label>
                    <label className="block space-y-1 text-xs text-muted-foreground">
                        <span>Audit window</span>
                        <select className={inputClass} value={window} onChange={(event) => setWindow(event.target.value)}>
                            <option value="24h">Last 24 hours</option>
                            <option value="168h">Last 7 days</option>
                            <option value="720h">Last 30 days</option>
                        </select>
                    </label>
                    <button type="submit" className={buttonClass} disabled={busy}>{analysis.isLoading ? 'Loading audit…' : 'Load audit'}</button>
                </form>
                <form onSubmit={submitReport} className="space-y-2">
                    <label className="block space-y-1 text-xs text-muted-foreground">
                        <span>Report format</span>
                        <select className={inputClass} value={format} onChange={(event) => setFormat(event.target.value as AuditFormat)}>
                            <option value="sarif">SARIF 2.1.0</option>
                            <option value="kubernetes">Kubernetes audit JSON / JSONL</option>
                        </select>
                    </label>
                    <label className="block space-y-1 text-xs text-muted-foreground">
                        <span>Report file (up to 4 MiB)</span>
                        <input type="file" className={inputClass} onChange={(event) => setReport(event.target.files?.[0])} />
                    </label>
                    {format === 'sarif' && (
                        <label className="block space-y-1 text-xs text-muted-foreground">
                            <span>Scan time (optional, local time)</span>
                            <input type="datetime-local" className={inputClass} value={observedAt} onChange={(event) => setObservedAt(event.target.value)} />
                        </label>
                    )}
                    <button type="submit" className={buttonClass} disabled={busy}>{upload.isLoading ? 'Importing…' : 'Import report'}</button>
                    <p className="text-xs text-muted-foreground">Use the same asset ID for reports that describe the same deployed asset.</p>
                    {formError && <p role="alert" className="text-sm text-rose-300">{formError}</p>}
                    {upload.isError && <p role="alert" className="text-sm text-rose-300">{auditError(upload.error, 'Unable to import the report.')}</p>}
                    {upload.data && <p role="status" className="text-sm">{upload.data.imported} imported · {upload.data.updated} updated</p>}
                </form>
            </div>
            {analysis.isError && <p role="alert" className="text-sm text-rose-300">{auditError(analysis.error, 'Unable to load audit evidence.')}</p>}
            {analysis.data && (
                <div className="space-y-4">
                    <div className="flex flex-wrap items-center gap-3 text-sm">
                        <p>{analysis.variables?.asset || 'All assets'} · {analysis.variables?.window} · {analysis.data.count} records</p>
                        {analysis.variables?.asset && (
                            <button type="button" className={buttonClass} disabled={busy || !explanationCurrent} onClick={() => explanation.mutate(activeScope)}>
                                {explanation.isLoading ? 'Explaining…' : 'Explain audit'}
                            </button>
                        )}
                    </div>
                    {explanation.isError && <p role="alert" className="text-sm text-rose-300">{auditError(explanation.error, 'Unable to explain this audit.')}</p>}
                    {explanation.data && (
                        <div className="rounded border border-border p-3 text-sm">
                            <h4 className="font-medium">Audit explanation · {explanation.data.mode}</h4>
                            <p className="mt-1 whitespace-pre-wrap break-words">{explanation.data.summary}</p>
                            <EvidenceLinks ids={explanation.data.evidence_ids} visibleIDs={visibleIDs} />
                        </div>
                    )}
                    <div className="grid gap-4 lg:grid-cols-2">
                        <div>
                            <h4 className="mb-2 text-sm font-semibold">Correlations</h4>
                            {correlations.length === 0 && <p className="text-sm text-muted-foreground">No correlations in this window.</p>}
                            <ul className="space-y-2">
                                {correlations.map((item) => (
                                    <li key={item.id} className="rounded border border-border p-2 text-sm break-words">
                                        <p className="font-medium">{item.summary}</p>
                                        <p className="text-xs text-muted-foreground">{item.asset} · {item.rule}</p>
                                        <EvidenceLinks ids={item.evidence_ids} visibleIDs={visibleIDs} />
                                    </li>
                                ))}
                            </ul>
                        </div>
                        <div>
                            <h4 className="mb-2 text-sm font-semibold">Baseline anomalies</h4>
                            {anomalies.length === 0 && <p className="text-sm text-muted-foreground">No anomalies reported for the available history.</p>}
                            <ul className="space-y-2">
                                {anomalies.map((item) => (
                                    <li key={`${item.asset}/${item.bucket}`} className="rounded border border-border p-2 text-sm break-words">
                                        <p className="font-medium">{item.asset} · {formatAuditDate(item.bucket)}</p>
                                        <p className="text-xs text-muted-foreground">Denial rate {(item.observed * 100).toFixed(1)}% · baseline {(item.baseline * 100).toFixed(1)}% · score {item.score.toFixed(2)} · {item.samples} samples</p>
                                        <EvidenceLinks ids={item.evidence_ids} visibleIDs={visibleIDs} />
                                    </li>
                                ))}
                            </ul>
                        </div>
                    </div>
                    <div>
                        <h4 className="mb-2 text-sm font-semibold">Source records</h4>
                        {records.length === 0 ? (
                            <p className="text-sm text-muted-foreground">No audit records in this window.</p>
                        ) : (
                            <div className="max-h-96 overflow-auto">
                                <table className="w-full text-left text-xs">
                                    <thead className="sticky top-0 bg-card">
                                        <tr>
                                            <th scope="col" className="p-2">Observed</th>
                                            <th scope="col" className="p-2">Asset</th>
                                            <th scope="col" className="p-2">Source and rule</th>
                                            <th scope="col" className="p-2">Evidence</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {records.map((record) => (
                                            <tr key={record.id} id={`security-audit-record-${encodeURIComponent(record.id)}`} className="scroll-mt-8 border-t border-border target:bg-sky-400/10">
                                                <td className="p-2 align-top">{formatAuditDate(record.observed_at)}</td>
                                                <td className="p-2 align-top break-all">{record.asset}</td>
                                                <td className="p-2 align-top break-all">
                                                    <p className="font-medium">{record.rule}</p>
                                                    <p>{[record.source, record.tool, record.kind, record.severity].filter(Boolean).join(' · ')}</p>
                                                </td>
                                                <td className="p-2 align-top break-all">
                                                    <p className="font-mono">{record.id}</p>
                                                    <p>{[record.verb, record.resource, record.actor, record.status].filter(Boolean).join(' · ')}</p>
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </div>
                </div>
            )}
        </section>
    );
}
