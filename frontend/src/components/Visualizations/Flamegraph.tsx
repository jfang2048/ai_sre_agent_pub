import React from 'react';

const Flamegraph = () => {
    return (
        <div className="w-full h-full bg-card p-4 flex flex-col items-center justify-center border border-border rounded-lg relative overflow-hidden group">
            <div className="absolute inset-x-0 top-0 h-1 bg-gradient-to-r from-orange-500 to-red-500" />
            <div className="flex flex-col gap-1 w-full max-w-md opacity-80 group-hover:opacity-100 transition-opacity">
                <div className="w-full h-6 bg-orange-500/80 rounded cursor-pointer hover:bg-orange-500 flex items-center px-2 text-xs text-white">
                    <span>GET /api/v1/checkout (250ms)</span>
                </div>
                <div className="flex gap-1 w-full">
                    <div className="w-2/3 h-6 bg-orange-400/80 rounded cursor-pointer hover:bg-orange-400 flex items-center px-2 text-xs text-white">
                        <span>auth_middleware (150ms)</span>
                    </div>
                    <div className="w-1/3 h-6 bg-yellow-400/80 rounded cursor-pointer hover:bg-yellow-400 flex items-center px-2 text-xs text-black/70">
                        <span>validate (80ms)</span>
                    </div>
                </div>
                <div className="flex gap-1 w-full pl-4">
                    <div className="w-1/2 h-6 bg-red-500/80 rounded cursor-pointer hover:bg-red-500 flex items-center px-2 text-xs text-white">
                        <span>db_query (140ms)</span>
                    </div>
                </div>
            </div>
            <p className="mt-4 text-xs text-muted-foreground font-mono">Trace ID: 89f4...23a</p>
        </div>
    );
};

export default Flamegraph;
