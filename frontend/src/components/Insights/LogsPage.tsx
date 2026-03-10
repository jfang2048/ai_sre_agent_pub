import React from 'react';
import LogsExplorerPanel from '@/components/Insights/LogsExplorerPanel';

export default function LogsPage() {
    return (
        <div className="space-y-4" data-testid="logs-page">
            <section className="rounded-lg border border-border bg-card p-4">
                <h2 className="text-lg font-semibold">Logs</h2>
                <p className="text-xs text-muted-foreground mt-1">
                    Indexed logs with timeline and metric correlation drilldowns.
                </p>
            </section>
            <section className="h-[72vh] min-h-[560px]">
                <LogsExplorerPanel />
            </section>
        </div>
    );
}

