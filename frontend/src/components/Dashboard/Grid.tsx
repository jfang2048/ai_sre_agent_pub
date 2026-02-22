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
                return (
                    <div className="p-4 font-mono text-xs text-muted-foreground h-full overflow-auto">
                        <div className="border-b border-border pb-1 mb-2">LIVE LOGS STREAM</div>
                        {Array.from({ length: 10 }).map((_, i) => (
                            <div key={i} className="mb-1">
                                <span className="text-blue-400">2023-10-27 10:0{i}:23</span>
                                <span className="mx-2 text-green-500">[INFO]</span>
                                Service worker processed job {Math.floor(Math.random() * 1000)}
                            </div>
                        ))}
                    </div>
                );
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
