import React, { useEffect, useState } from 'react';
import ForceGraph2D from 'react-force-graph-2d';

const ServiceGraph = () => {
    const [graphData, setGraphData] = useState({ nodes: [], links: [] });
    const [dimensions, setDimensions] = useState({ w: 800, h: 600 });

    useEffect(() => {
        // Fetch topology data
        const fetchData = async () => {
            try {
                // Mock endpoint or real one
                const res = await fetch('/api/v1/topology');
                const data = await res.json();

                // Transform to generic graph format if needed
                // data.nodes should have 'id'
                const nodes = data.nodes.map(n => ({ ...n, id: n.name, val: n.status === 'critical' ? 10 : 5 }));
                const links = data.links.map(l => ({ source: l.source, target: l.target }));

                setGraphData({ nodes, links });
            } catch (err) {
                console.error("Failed to fetch topology", err);
            }
        };

        fetchData();
        const interval = setInterval(fetchData, 10000); // 10s refresh
        return () => clearInterval(interval);
    }, []);

    // Color nodes by status
    const nodeColor = (node) => {
        switch (node.status) {
            case 'critical': return '#ff4444';
            case 'degraded': return '#ffbb33';
            default: return '#00C851';
        }
    };

    return (
        <div style={{ padding: '20px', background: '#1e1e1e', borderRadius: '8px' }}>
            <h3>Service Dependency Topology</h3>
            <div style={{ height: '600px', border: '1px solid #333' }}>
                <ForceGraph2D
                    width={dimensions.w}
                    height={dimensions.h}
                    graphData={graphData}
                    nodeLabel="name"
                    nodeColor={nodeColor}
                    nodeRelSize={6}
                    linkColor={() => '#666'}
                    linkDirectionalArrowLength={3.5}
                    linkDirectionalArrowRelPos={1}
                />
            </div>
            <div className="legend" style={{ marginTop: '10px', display: 'flex', gap: '15px' }}>
                <span><span style={{ color: '#00C851' }}>●</span> Healthy</span>
                <span><span style={{ color: '#ffbb33' }}>●</span> Degraded</span>
                <span><span style={{ color: '#ff4444' }}>●</span> Critical</span>
            </div>
        </div>
    );
};

export default ServiceGraph;
