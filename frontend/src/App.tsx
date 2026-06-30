import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useDashboardStore } from '@/store/dashboardStore';
import DashboardGrid from '@/components/Dashboard/Grid';
import NLQuery from '@/components/Search/NLQuery';
import MetricTrendsPage from '@/components/Visualizations/MetricTrendsPage';
import DataPathDiagnosticsPage from '@/components/Visualizations/DataPathDiagnosticsPage';
import GPUObservabilityPage from '@/components/Visualizations/GPUObservabilityPage';
import IncidentAnalysisPage from '@/components/Insights/IncidentAnalysisPage';
import JointRiskPage from '@/components/Insights/JointRiskPage';
import KnowledgePage from '@/components/Insights/KnowledgePage';
import RiskInsightsPage from '@/components/Insights/RiskInsightsPage';
import RCAPage from '@/components/Insights/RCAPage';
import SecurityDashboardPage from '@/components/Insights/SecurityDashboardPage';
import IncidentsPage from '@/components/Insights/IncidentsPage';
import AuditLogPage from '@/components/Insights/AuditLogPage';
import LogsPage from '@/components/Insights/LogsPage';
import type { TrendsNavigationIntent, TrendsNavigationIntentInput } from '@/components/Visualizations/trendsIntent';
import AgentTab from '@/Agent';
import { Settings, Bell, LayoutDashboard, Activity, Bot, Moon, Sun, X, Workflow, Gauge, ShieldAlert, BrainCircuit, AlertTriangle, ScrollText, Shield, Siren, ClipboardList, Database } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

const queryClient = new QueryClient();
type ActivePage = 'dashboard' | 'trends' | 'analysis' | 'riskInsights' | 'jointRisk' | 'rca' | 'knowledge' | 'security' | 'incidents' | 'audit' | 'logs' | 'dataPath' | 'gpu' | 'agent';

const pageSlugByActivePage: Record<ActivePage, string> = {
    dashboard: 'dashboard',
    trends: 'trends',
    analysis: 'incident-analysis',
    riskInsights: 'risk-insights',
    jointRisk: 'joint-risk',
    rca: 'rca',
    knowledge: 'knowledge-base',
    security: 'security-dashboard',
    incidents: 'incidents',
    audit: 'audit-log',
    logs: 'logs',
    dataPath: 'data-path',
    gpu: 'gpu-observability',
    agent: 'agent',
};

const activePageBySlug: Record<string, ActivePage> = {
    dashboard: 'dashboard',
    trends: 'trends',
    analysis: 'analysis',
    'incident-analysis': 'analysis',
    'risk-insights': 'riskInsights',
    'joint-risk': 'jointRisk',
    rca: 'rca',
    knowledge: 'knowledge',
    'knowledge-base': 'knowledge',
    security: 'security',
    'security-dashboard': 'security',
    incidents: 'incidents',
    audit: 'audit',
    'audit-log': 'audit',
    logs: 'logs',
    'data-path': 'dataPath',
    gpu: 'gpu',
    'gpu-observability': 'gpu',
    agent: 'agent',
};

const pageTitleByActivePage: Record<ActivePage, string> = {
    dashboard: 'SRE Command Center',
    trends: 'Metric Trends',
    analysis: 'Incident / Analysis',
    riskInsights: 'Risk Insights',
    jointRisk: 'Joint Risk',
    rca: 'RCA Workflow',
    knowledge: 'Knowledge Base',
    security: 'Security Dashboard',
    incidents: 'Incidents',
    audit: 'Action Audit Log',
    logs: 'Logs',
    dataPath: 'Data Path Diagnostics',
    gpu: 'GPU Observability',
    agent: 'Platform Operations',
};

const primaryNavigationItems: Array<{ page: ActivePage; title: string; icon: LucideIcon }> = [
    { page: 'dashboard', title: 'Platform Overview', icon: LayoutDashboard },
    { page: 'trends', title: 'Metric Trends', icon: Activity },
    { page: 'analysis', title: 'Incident Analysis', icon: ShieldAlert },
    { page: 'riskInsights', title: 'Risk Insights', icon: AlertTriangle },
    { page: 'jointRisk', title: 'Joint Risk', icon: ShieldAlert },
    { page: 'rca', title: 'RCA Workflow', icon: BrainCircuit },
    { page: 'knowledge', title: 'Knowledge Base', icon: Database },
    { page: 'security', title: 'Security Dashboard', icon: Shield },
    { page: 'incidents', title: 'Incidents', icon: Siren },
    { page: 'audit', title: 'Audit Log', icon: ClipboardList },
    { page: 'logs', title: 'Logs', icon: ScrollText },
    { page: 'gpu', title: 'GPU Observability', icon: Gauge },
    { page: 'agent', title: 'Operations', icon: Bot },
    { page: 'dataPath', title: 'Data Path Diagnostics', icon: Workflow },
];

function getInitialActivePage(): ActivePage {
    if (typeof window === 'undefined') {
        return 'dashboard';
    }
    const requestedPage = new URLSearchParams(window.location.search).get('page');
    if (!requestedPage) {
        return 'dashboard';
    }
    return activePageBySlug[requestedPage.trim().toLowerCase()] ?? 'dashboard';
}

function App() {
    const { theme, setTheme } = useDashboardStore();
    const [activePage, setActivePage] = useState<ActivePage>(getInitialActivePage);
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [trendsIntent, setTrendsIntent] = useState<TrendsNavigationIntent | null>(null);
    const [queuedAgentQuery, setQueuedAgentQuery] = useState<{ query: string; requestToken: number } | null>(null);
    const trendsIntentTokenRef = useRef(0);
    const agentQueryTokenRef = useRef(0);

    const pageTitle = pageTitleByActivePage[activePage];

    useEffect(() => {
        if (!settingsOpen) {
            return undefined;
        }
        const handleEsc = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                setSettingsOpen(false);
            }
        };
        window.addEventListener('keydown', handleEsc);
        return () => window.removeEventListener('keydown', handleEsc);
    }, [settingsOpen]);

    useEffect(() => {
        if (typeof window === 'undefined') {
            return;
        }
        const url = new URL(window.location.href);
        if (activePage === 'dashboard') {
            url.searchParams.delete('page');
        } else {
            url.searchParams.set('page', pageSlugByActivePage[activePage]);
        }
        const nextLocation = `${url.pathname}${url.search}${url.hash}`;
        const currentLocation = `${window.location.pathname}${window.location.search}${window.location.hash}`;
        if (nextLocation !== currentLocation) {
            window.history.replaceState({}, '', nextLocation);
        }
    }, [activePage]);

    const toggleTheme = () => {
        setTheme(theme === 'dark' ? 'light' : 'dark');
    };

    const openTrendsFromDiagnostics = (intent: TrendsNavigationIntentInput) => {
        trendsIntentTokenRef.current += 1;
        setTrendsIntent({
            ...intent,
            requestToken: trendsIntentTokenRef.current,
        });
        setActivePage('trends');
    };

    const handOffAgentQuery = (query: string) => {
        agentQueryTokenRef.current += 1;
        setQueuedAgentQuery({
            query,
            requestToken: agentQueryTokenRef.current,
        });
        setActivePage('agent');
    };

    function renderActivePage() {
        switch (activePage) {
            case 'dashboard':
                return <DashboardGrid />;
            case 'trends':
                return (
                    <MetricTrendsPage
                        navigationIntent={trendsIntent}
                        onNavigationIntentConsumed={() => setTrendsIntent(null)}
                    />
                );
            case 'analysis':
                return <IncidentAnalysisPage onOpenTrends={openTrendsFromDiagnostics} />;
            case 'riskInsights':
                return <RiskInsightsPage />;
            case 'jointRisk':
                return <JointRiskPage />;
            case 'rca':
                return <RCAPage />;
            case 'knowledge':
                return <KnowledgePage />;
            case 'security':
                return <SecurityDashboardPage />;
            case 'incidents':
                return <IncidentsPage />;
            case 'audit':
                return <AuditLogPage />;
            case 'logs':
                return <LogsPage />;
            case 'dataPath':
                return <DataPathDiagnosticsPage onOpenTrends={openTrendsFromDiagnostics} />;
            case 'gpu':
                return <GPUObservabilityPage />;
            case 'agent':
                return (
                    <AgentTab
                        queuedQuery={queuedAgentQuery}
                        onQueuedQueryHandled={() => setQueuedAgentQuery(null)}
                    />
                );
            default:
                return null;
        }
    }

    return (
        <QueryClientProvider client={queryClient}>
            <div className={`flex h-screen bg-background text-foreground ${theme}`}>
                <aside className="w-16 border-r border-border flex flex-col items-center py-4 gap-6 bg-card z-50">
                    <div className="w-10 h-10 bg-primary rounded-xl flex items-center justify-center text-white font-bold shadow-glow">
                        CP
                    </div>
                    <nav className="flex flex-col gap-4">
                        {primaryNavigationItems.map(({ page, title, icon: Icon }) => (
                            <button
                                key={page}
                                type="button"
                                onClick={() => setActivePage(page)}
                                title={title}
                                className={`w-10 h-10 rounded-lg flex items-center justify-center transition-colors ${
                                    activePage === page
                                        ? 'bg-accent text-accent-foreground'
                                        : 'text-muted-foreground hover:bg-muted/50'
                                }`}
                            >
                                <Icon size={20} />
                            </button>
                        ))}
                        <button className="w-10 h-10 rounded-lg text-muted-foreground hover:bg-muted/50 flex items-center justify-center transition-colors">
                            <Bell size={20} />
                        </button>
                    </nav>
                    <div className="mt-auto flex flex-col gap-4">
                        <button
                            onClick={toggleTheme}
                            type="button"
                            className="w-10 h-10 rounded-lg text-muted-foreground hover:bg-muted/50 flex items-center justify-center transition-colors"
                            title="Toggle Theme"
                        >
                            {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
                        </button>
                        <button
                            type="button"
                            onClick={() => setSettingsOpen(true)}
                            className="w-10 h-10 rounded-lg text-muted-foreground hover:bg-muted/50 flex items-center justify-center transition-colors"
                            title="Open Settings"
                        >
                            <Settings size={20} />
                        </button>
                    </div>
                </aside>

                <main className="flex-1 flex flex-col overflow-hidden relative">
                    <header className="h-16 border-b border-border flex items-center justify-between px-6 bg-background sticky top-0 z-40">
                        <h1 className="text-xl font-bold text-foreground">
                            {pageTitle}
                        </h1>
                        <div className="flex-1 max-w-2xl mx-8">
                            <NLQuery onSubmitQuery={handOffAgentQuery} />
                        </div>
                        <div className="flex items-center gap-4">
                            <div className="w-8 h-8 rounded-full bg-primary border border-border" />
                        </div>
                    </header>

                    <div className="flex-1 overflow-auto bg-background p-6 relative">
                        {renderActivePage()}
                    </div>
                </main>
            </div>
            {settingsOpen && (
                <div
                    className="fixed inset-0 z-[70] bg-black/50 backdrop-blur-sm flex items-center justify-center p-4"
                    onClick={() => setSettingsOpen(false)}
                >
                    <section
                        role="dialog"
                        aria-modal="true"
                        aria-label="Dashboard settings"
                        className="w-full max-w-md rounded-xl border border-border bg-card p-5 shadow-2xl"
                        onClick={(event) => event.stopPropagation()}
                    >
                        <div className="flex items-center justify-between">
                            <h2 className="text-lg font-semibold">Settings</h2>
                            <button
                                type="button"
                                onClick={() => setSettingsOpen(false)}
                                className="rounded-md p-1 text-muted-foreground hover:bg-muted/60 hover:text-foreground transition-colors"
                                aria-label="Close settings"
                            >
                                <X size={18} />
                            </button>
                        </div>
                        <p className="mt-2 text-sm text-muted-foreground">
                            Choose display mode for better visibility in your environment.
                        </p>
                        <div className="mt-4 grid grid-cols-2 gap-3">
                            <button
                                type="button"
                                onClick={() => setTheme('light')}
                                className={`rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${
                                    theme === 'light'
                                        ? 'border-primary bg-primary text-primary-foreground'
                                        : 'border-border bg-background text-foreground hover:bg-muted/60'
                                }`}
                            >
                                Light
                            </button>
                            <button
                                type="button"
                                onClick={() => setTheme('dark')}
                                className={`rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${
                                    theme === 'dark'
                                        ? 'border-primary bg-primary text-primary-foreground'
                                        : 'border-border bg-background text-foreground hover:bg-muted/60'
                                }`}
                            >
                                Dark
                            </button>
                        </div>
                    </section>
                </div>
            )}
        </QueryClientProvider>
    )
}

export default App
