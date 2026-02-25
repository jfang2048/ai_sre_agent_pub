import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useDashboardStore } from '@/store/dashboardStore';
import DashboardGrid from '@/components/Dashboard/Grid';
import NLQuery from '@/components/Search/NLQuery';
import MetricTrendsPage from '@/components/Visualizations/MetricTrendsPage';
import DataPathDiagnosticsPage from '@/components/Visualizations/DataPathDiagnosticsPage';
import GPUObservabilityPage from '@/components/Visualizations/GPUObservabilityPage';
import type { TrendsNavigationIntent, TrendsNavigationIntentInput } from '@/components/Visualizations/trendsIntent';
import AgentTab from '@/Agent';
import { Settings, Bell, LayoutDashboard, Activity, Bot, Moon, Sun, X, Workflow, Gauge } from 'lucide-react';

const queryClient = new QueryClient();
type ActivePage = 'dashboard' | 'trends' | 'dataPath' | 'gpu' | 'agent';

function App() {
    const { theme, setTheme } = useDashboardStore();
    const [activePage, setActivePage] = useState<ActivePage>('dashboard');
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [trendsIntent, setTrendsIntent] = useState<TrendsNavigationIntent | null>(null);
    const trendsIntentTokenRef = useRef(0);

    const pageTitle = useMemo(() => {
        if (activePage === 'trends') {
            return 'Metric Trends';
        }
        if (activePage === 'dataPath') {
            return 'Data Path Diagnostics';
        }
        if (activePage === 'gpu') {
            return 'GPU Observability';
        }
        if (activePage === 'agent') {
            return 'AGENT Operations';
        }
        return 'SRE Command Center';
    }, [activePage]);

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

    return (
        <QueryClientProvider client={queryClient}>
            <div className={`flex h-screen bg-background text-foreground ${theme}`}>
                {/* Sidebar */}
                <aside className="w-16 border-r border-border flex flex-col items-center py-4 gap-6 bg-card z-50">
                    <div className="w-10 h-10 bg-primary rounded-xl flex items-center justify-center text-white font-bold shadow-glow">
                        AI
                    </div>
                    <nav className="flex flex-col gap-4">
                        <button
                            onClick={() => setActivePage('dashboard')}
                            title="Dashboard"
                            className={`w-10 h-10 rounded-lg flex items-center justify-center transition-colors ${
                                activePage === 'dashboard'
                                    ? 'bg-accent text-accent-foreground'
                                    : 'text-muted-foreground hover:bg-muted/50'
                            }`}
                        >
                            <LayoutDashboard size={20} />
                        </button>
                        <button
                            onClick={() => setActivePage('trends')}
                            title="Metric Trends"
                            className={`w-10 h-10 rounded-lg flex items-center justify-center transition-colors ${
                                activePage === 'trends'
                                    ? 'bg-accent text-accent-foreground'
                                    : 'text-muted-foreground hover:bg-muted/50'
                            }`}
                        >
                            <Activity size={20} />
                        </button>
                        <button
                            onClick={() => setActivePage('gpu')}
                            title="GPU Observability"
                            className={`w-10 h-10 rounded-lg flex items-center justify-center transition-colors ${
                                activePage === 'gpu'
                                    ? 'bg-accent text-accent-foreground'
                                    : 'text-muted-foreground hover:bg-muted/50'
                            }`}
                        >
                            <Gauge size={20} />
                        </button>
                        <button
                            onClick={() => setActivePage('agent')}
                            title="AGENT"
                            className={`w-10 h-10 rounded-lg flex items-center justify-center transition-colors ${
                                activePage === 'agent'
                                    ? 'bg-accent text-accent-foreground'
                                    : 'text-muted-foreground hover:bg-muted/50'
                            }`}
                        >
                            <Bot size={20} />
                        </button>
                        <button
                            onClick={() => setActivePage('dataPath')}
                            title="Data Path Diagnostics"
                            className={`w-10 h-10 rounded-lg flex items-center justify-center transition-colors ${
                                activePage === 'dataPath'
                                    ? 'bg-accent text-accent-foreground'
                                    : 'text-muted-foreground hover:bg-muted/50'
                            }`}
                        >
                            <Workflow size={20} />
                        </button>
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

                {/* Main Content */}
                <main className="flex-1 flex flex-col overflow-hidden relative">
                    {/* Header */}
                    <header className="h-16 border-b border-border flex items-center justify-between px-6 bg-background sticky top-0 z-40">
                        <h1 className="text-xl font-bold text-foreground">
                            {pageTitle}
                        </h1>
                        <div className="flex-1 max-w-2xl mx-8">
                            <NLQuery />
                        </div>
                        <div className="flex items-center gap-4">
                            <div className="w-8 h-8 rounded-full bg-primary border border-border" />
                        </div>
                    </header>

                    {/* Dashboard Area */}
                    <div className="flex-1 overflow-auto bg-background p-6 relative">
                        {activePage === 'dashboard' && <DashboardGrid />}
                        {activePage === 'trends' && (
                            <MetricTrendsPage
                                navigationIntent={trendsIntent}
                                onNavigationIntentConsumed={() => setTrendsIntent(null)}
                            />
                        )}
                        {activePage === 'dataPath' && (
                            <DataPathDiagnosticsPage onOpenTrends={openTrendsFromDiagnostics} />
                        )}
                        {activePage === 'gpu' && <GPUObservabilityPage />}
                        {activePage === 'agent' && <AgentTab />}
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
