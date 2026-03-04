import React from 'react';
import RGL, { WidthProvider } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import { useDashboardStore } from '@/store/dashboardStore';
import AIInsightsPanel from '@/components/Insights/AIInsights';
import ServiceGraph from '@/components/ServiceGraph';
import { GripHorizontal, Maximize2 } from 'lucide-react';
import TopProgramsPanel from '@/components/Insights/TopPrograms';
import MetricOverviewPanel from '@/components/Visualizations/MetricOverviewPanel';
import K8sDrilldown from '@/components/Insights/K8sDrilldown';
import OrchestrationSLOPanel from '@/components/Insights/OrchestrationSLOPanel';
import LogsExplorerPanel from '@/components/Insights/LogsExplorerPanel';
import OperationsControlPanel from '@/components/Insights/OperationsControlPanel';

const ReactGridLayout = WidthProvider(RGL);

const DashboardGrid = () => {
    const { layout, widgets, setLayout } = useDashboardStore();

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

    return (
        <ReactGridLayout
            className="layout select-none"
            layout={layout}
            cols={12}
            rowHeight={100}
            width={1600} // WidthProvider usually handles this but good default
            onLayoutChange={setLayout}
            draggableHandle=".drag-handle"
            margin={[16, 16]}
        >
            {widgets.map(w => (
                <div key={w} className="bg-card rounded-lg overflow-hidden border border-border flex flex-col shadow-lg transition-shadow hover:shadow-xl hover:border-primary/50 group">
                    <div className="drag-handle bg-gradient-to-r from-muted/10 to-transparent h-6 cursor-move flex items-center justify-between px-3 border-b border-border/50">
                        <div className="flex items-center gap-2">
                            <GripHorizontal className="w-3 h-3 text-muted-foreground" />
                            <span className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">{w.replace('-', ' ')}</span>
                        </div>
                        <Maximize2 className="w-3 h-3 text-muted-foreground opacity-0 group-hover:opacity-100 hover:text-foreground cursor-pointer transition-opacity" />
                    </div>
                    <div className="flex-1 overflow-hidden relative">
                        {renderWidget(w)}
                    </div>
                </div>
            ))}
        </ReactGridLayout>
    );
};

export default DashboardGrid;
