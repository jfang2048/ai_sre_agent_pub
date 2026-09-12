import React, { useEffect, useState } from 'react';
import RGL, { WidthProvider } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import { useDashboardStore } from '@/store/dashboardStore';
import AIInsightsPanel from '@/components/Insights/AIInsights';
import ServiceGraph from '@/components/ServiceGraph';
import { GripHorizontal } from 'lucide-react';
import TopProgramsPanel from '@/components/Insights/TopPrograms';
import MetricOverviewPanel from '@/components/Visualizations/MetricOverviewPanel';
import K8sDrilldown from '@/components/Insights/K8sDrilldown';
import OrchestrationSLOPanel from '@/components/Insights/OrchestrationSLOPanel';
import LogsExplorerPanel from '@/components/Insights/LogsExplorerPanel';
import OperationsControlPanel from '@/components/Insights/OperationsControlPanel';

const ReactGridLayout = WidthProvider(RGL);

const DashboardGrid = () => {
    const { layout, widgets, setLayout } = useDashboardStore();
    const [compact, setCompact] = useState(() => window.matchMedia?.('(max-width: 767px)').matches ?? false);
    useEffect(() => {
        const query = window.matchMedia?.('(max-width: 767px)');
        if (!query) return;
        const update = () => setCompact(query.matches);
        query.addEventListener('change', update);
        return () => query.removeEventListener('change', update);
    }, []);

    const renderWidget = (id: string) => {
        switch (id) {
            case 'ai-insights':
                return <AIInsightsPanel />;
            case 'topology-graph':
                return <ServiceGraph />;
            case 'top-programs':
                return <TopProgramsPanel />;
            case 'overview-metrics':
                return <MetricOverviewPanel />;
            case 'k8s-drilldown':
                return <K8sDrilldown />;
            case 'orchestration-slo':
                return <OrchestrationSLOPanel />;
            case 'logs-feed':
                return <LogsExplorerPanel />;
            case 'ops-control':
                return <OperationsControlPanel />;
            default:
                return (
                    <div className="flex items-center justify-center h-full text-muted-foreground">
                        Widget: {id}
                    </div>
                );
        }
    };

    const panels = widgets.map(w => (
        <div key={w} className={`bg-card rounded-lg overflow-hidden border border-border flex flex-col shadow-sm ${compact && w !== 'overview-metrics' ? 'h-[36rem]' : ''}`}>
            <div className={`drag-handle shrink-0 h-8 flex items-center gap-2 px-3 border-b border-border/50 ${compact ? '' : 'cursor-move'}`}>
                {!compact && <GripHorizontal aria-hidden="true" className="w-3 h-3 text-muted-foreground" />}
                <span className="text-xs font-medium text-muted-foreground capitalize">{w.replaceAll('-', ' ')}</span>
            </div>
            <div className="flex-1 min-h-0 overflow-hidden relative">{renderWidget(w)}</div>
        </div>
    ));

    // Narrow layouts are read-only, so resizing the window cannot overwrite a
    // user's saved desktop positions with a one-column layout.
    if (compact) return <div className="space-y-4">{panels}</div>;

    return (
        <ReactGridLayout
            className="layout"
            layout={layout}
            cols={12}
            rowHeight={100}
            width={1600} // WidthProvider usually handles this but good default
            onLayoutChange={setLayout}
            draggableHandle=".drag-handle"
            margin={[16, 16]}
        >
            {panels}
        </ReactGridLayout>
    );
};

export default DashboardGrid;
