import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import ForceGraph2D from 'react-force-graph-2d';
import { Activity, Network, AlertTriangle } from 'lucide-react';
import { api } from '@/api/client';

const STATUS_COLOR = {
    healthy: '#22c55e',
    degraded: '#f59e0b',
    critical: '#ef4444',
};

const TYPE_SIZE = {
    fleet: 8,
    host: 6,
    process: 4,
};

function useContainerSize(ref) {
    const [size, setSize] = useState({ width: 640, height: 420 });

    useEffect(() => {
        if (!ref.current) {
            return undefined;
        }
        const update = () => {
            const rect = ref.current?.getBoundingClientRect();
            if (!rect) {
                return;
            }
            setSize({
                width: Math.max(320, Math.floor(rect.width)),
                height: Math.max(260, Math.floor(rect.height)),
            });
        };
        update();

        const observer = new ResizeObserver(update);
        observer.observe(ref.current);
        window.addEventListener('resize', update);

        return () => {
            observer.disconnect();
            window.removeEventListener('resize', update);
        };
    }, [ref]);

    return size;
}

const ServiceGraph = () => {
    const containerRef = useRef(null);
    const { width, height } = useContainerSize(containerRef);

    const topologyQuery = useQuery({
        queryKey: ['topology-graph'],
        queryFn: async () => {
            const { data } = await api.get('/topology');
            return data;
        },
        refetchInterval: 10000,
    });

    const graphData = useMemo(() => {
        const nodes = (topologyQuery.data?.nodes ?? []).map((node) => ({
            ...node,
            val: TYPE_SIZE[node.type] ?? 4,
        }));
        const links = (topologyQuery.data?.links ?? []).map((link) => ({
            ...link,
            source: link.source,
            target: link.target,
        }));
        return { nodes, links };
    }, [topologyQuery.data?.links, topologyQuery.data?.nodes]);

    const hotspots = useMemo(() => {
        const processNodes = (topologyQuery.data?.nodes ?? [])
            .filter((node) => node.type === 'process')
            .sort((a, b) => (b.score ?? 0) - (a.score ?? 0));
        return processNodes.slice(0, 5);
    }, [topologyQuery.data?.nodes]);

    return (
        <div className="h-full w-full p-3 flex flex-col gap-2 bg-card/50">
            <div className="flex items-center justify-between">
                <div>
                    <div className="text-sm font-semibold">Service & Process Topology</div>
                    <div className="text-[11px] text-muted-foreground">Fleet nodes, hottest processes, and pressure severity links.</div>
                </div>
                <div className="text-[11px] text-muted-foreground text-right">
                    <div>Hosts {topologyQuery.data?.summary?.host_count ?? 0}</div>
                    <div>Processes {topologyQuery.data?.summary?.process_count ?? 0}</div>
                </div>
            </div>

            {topologyQuery.isLoading ? (
                <div className="flex-1 flex items-center justify-center text-sm text-muted-foreground">Loading topology...</div>
            ) : topologyQuery.isError ? (
                <div className="flex-1 flex items-center justify-center text-sm text-rose-300">Topology API unavailable.</div>
            ) : graphData.nodes.length === 0 ? (
                <div className="flex-1 flex items-center justify-center text-sm text-muted-foreground">No topology data yet.</div>
            ) : (
                <>
                    <div ref={containerRef} className="flex-1 min-h-[220px] border border-border rounded-lg overflow-hidden bg-background/50">
                        <ForceGraph2D
                            width={width}
                            height={height}
                            graphData={graphData}
                            nodeLabel={(node) => `${node.name} (${node.type})`}
                            nodeColor={(node) => STATUS_COLOR[node.status] ?? '#94a3b8'}
                            nodeRelSize={5}
                            linkDirectionalArrowLength={3}
                            linkDirectionalArrowRelPos={1}
                            linkColor={() => '#64748b'}
                            cooldownTicks={80}
                        />
                    </div>

                    <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
                        <div className="rounded-md border border-border bg-background/40 p-2">
                            <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1">Severity</div>
                            <div className="text-xs flex flex-wrap gap-3">
                                <span><span style={{ color: STATUS_COLOR.healthy }}>●</span> Healthy</span>
                                <span><span style={{ color: STATUS_COLOR.degraded }}>●</span> Degraded</span>
                                <span><span style={{ color: STATUS_COLOR.critical }}>●</span> Critical</span>
                            </div>
                        </div>
                        <div className="rounded-md border border-border bg-background/40 p-2">
                            <div className="text-[11px] uppercase tracking-wider text-muted-foreground mb-1">Hotspots</div>
                            {hotspots.length === 0 ? (
                                <div className="text-xs text-muted-foreground">No process hotspots detected.</div>
                            ) : (
                                <div className="space-y-1">
                                    {hotspots.map((item) => (
                                        <div key={item.id} className="text-xs flex items-center justify-between gap-2">
                                            <span className="truncate">{item.name} ({item.pid || '—'})</span>
                                            <span className="text-primary font-semibold">{(item.score ?? 0).toFixed(2)}</span>
                                        </div>
                                    ))}
                                </div>
                            )}
                        </div>
                    </div>

                    <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground">
                        <span className="inline-flex items-center gap-1"><Network className="w-3 h-3" /> collector links</span>
                        <span className="inline-flex items-center gap-1"><Activity className="w-3 h-3" /> host-process hotspot links</span>
                        <span className="inline-flex items-center gap-1"><AlertTriangle className="w-3 h-3" /> critical means highest pressure score</span>
                    </div>
                </>
            )}
        </div>
    );
};

export default ServiceGraph;
