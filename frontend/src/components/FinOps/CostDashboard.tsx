import React, { useMemo } from 'react';
import { BarChart, Bar, XAxis, YAxis, Tooltip, Legend, ResponsiveContainer, CartesianGrid } from 'recharts';
import { DollarSign, TrendingDown, Server, PiggyBank } from 'lucide-react';

const mockDailyCost = [
    { day: 'Mon', compute: 120, storage: 45, network: 30 },
    { day: 'Tue', compute: 132, storage: 45, network: 35 },
    { day: 'Wed', compute: 125, storage: 46, network: 32 },
    { day: 'Thu', compute: 140, storage: 46, network: 40 }, // Spike
    { day: 'Fri', compute: 135, storage: 47, network: 38 },
    { day: 'Sat', compute: 90, storage: 47, network: 20 },
    { day: 'Sun', compute: 85, storage: 47, network: 18 },
];

const mockRecommendations = [
    { id: 1, type: 'Terminate', resource: 'i-0a1b2c3d (dev-worker-05)', savings: 145.00, confidence: 'High' },
    { id: 2, type: 'Rightsize', resource: 'i-0x9y8z (prod-app-02)', savings: 85.50, confidence: 'Medium' },
    { id: 3, type: 'Spot', resource: 'batch-job-cluster', savings: 320.00, confidence: 'High' },
];

const CostCard = ({ title, value, subs, icon: Icon, color }: any) => (
    <div className="bg-card p-4 rounded-lg border border-border flex items-start justify-between">
        <div>
            <p className="text-sm text-muted-foreground font-medium">{title}</p>
            <h3 className="text-2xl font-bold mt-1 text-foreground">{value}</h3>
            <p className="text-xs text-muted-foreground mt-1 flex items-center gap-1">
                {subs}
            </p>
        </div>
        <div className={`p-3 rounded-full bg-${color}-500/10 text-${color}-500`}>
            <Icon size={20} />
        </div>
    </div>
);

const CostDashboard = () => {
    const totalSavings = mockRecommendations.reduce((acc, r) => acc + r.savings, 0);

    return (
        <div className="p-6 space-y-6 h-full overflow-y-auto">
            <div className="flex items-center justify-between">
                <h2 className="text-2xl font-bold tracking-tight">FinOps Logic & Cost Optimization</h2>
                <div className="px-3 py-1 bg-green-500/10 text-green-500 rounded-full text-sm font-medium border border-green-500/20">
                    Projected Monthly Savings: ${totalSavings.toFixed(2)}
                </div>
            </div>

            {/* KPI Cards */}
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                <CostCard
                    title="Total Spend (MTD)"
                    value="$3,450.20"
                    subs={<><TrendingDown size={12} className="text-red-400 rotate-180" /> +5.2% vs last month</>}
                    icon={DollarSign}
                    color="blue"
                />
                <CostCard
                    title="Potential Savings"
                    value={`$${totalSavings.toFixed(2)}`}
                    subs="Monthly recurring"
                    icon={PiggyBank}
                    color="green"
                />
                <CostCard
                    title="Active Instances"
                    value="42"
                    subs="3 Idle Detected"
                    icon={Server}
                    color="purple"
                />
                <CostCard
                    title="Budget Utilization"
                    value="82%"
                    subs="Team SRE (Limit: $5k)"
                    icon={DollarSign}
                    color="yellow"
                />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                {/* Cost Trend Chart */}
                <div className="lg:col-span-2 bg-card p-6 rounded-lg border border-border">
                    <h3 className="text-lg font-semibold mb-6">Daily Cost Breakdown</h3>
                    <div className="h-80 w-full">
                        <ResponsiveContainer width="100%" height="100%">
                            <BarChart data={mockDailyCost}>
                                <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#333" />
                                <XAxis dataKey="day" stroke="#888" fontSize={12} tickLine={false} axisLine={false} />
                                <YAxis stroke="#888" fontSize={12} tickLine={false} axisLine={false} tickFormatter={(value) => `$${value}`} />
                                <Tooltip
                                    cursor={{ fill: '#333', opacity: 0.4 }}
                                    contentStyle={{ backgroundColor: '#18181b', borderColor: '#333', borderRadius: '8px' }}
                                />
                                <Legend />
                                <Bar dataKey="compute" stackId="a" fill="#3b82f6" name="Compute (EC2)" radius={[0, 0, 4, 4]} />
                                <Bar dataKey="storage" stackId="a" fill="#a855f7" name="Storage (EBS)" />
                                <Bar dataKey="network" stackId="a" fill="#14b8a6" name="Network" radius={[4, 4, 0, 0]} />
                            </BarChart>
                        </ResponsiveContainer>
                    </div>
                </div>

                {/* Recommendations List */}
                <div className="bg-card p-6 rounded-lg border border-border flex flex-col">
                    <h3 className="text-lg font-semibold mb-4">AI Recommendations</h3>
                    <div className="space-y-4 flex-1 overflow-auto pr-2">
                        {mockRecommendations.map((rec) => (
                            <div key={rec.id} className="p-4 rounded-lg bg-muted/40 border border-border hover:bg-muted/60 transition-colors group">
                                <div className="flex justify-between items-start mb-2">
                                    <span className={`px-2 py-0.5 rounded textxs font-semibold uppercase tracking-wider
                                        ${rec.type === 'Terminate' ? 'bg-red-500/10 text-red-400' :
                                            rec.type === 'Spot' ? 'bg-purple-500/10 text-purple-400' : 'bg-blue-500/10 text-blue-400'}`}>
                                        {rec.type}
                                    </span>
                                    <span className="text-green-400 font-mono font-bold text-sm">+${rec.savings}/mo</span>
                                </div>
                                <p className="text-sm font-medium mb-1">{rec.resource}</p>
                                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                                    <span>Confidence: {rec.confidence}</span>
                                    <button className="ml-auto text-primary hover:underline opacity-0 group-hover:opacity-100 transition-opacity">
                                        Apply Fix &rarr;
                                    </button>
                                </div>
                            </div>
                        ))}
                    </div>
                    <button className="mt-4 w-full py-2 bg-primary/10 text-primary rounded-lg text-sm font-medium hover:bg-primary/20 transition-colors">
                        View All Optimizations
                    </button>
                </div>
            </div>
        </div>
    );
};

export default CostDashboard;
