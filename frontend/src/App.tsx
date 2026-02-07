import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useDashboardStore } from '@/store/dashboardStore';
import DashboardGrid from '@/components/Dashboard/Grid';
import NLQuery from '@/components/Search/NLQuery';
import { Menu, Settings, Bell, LayoutDashboard } from 'lucide-react';

const queryClient = new QueryClient();

function App() {
    const { theme, setTheme } = useDashboardStore();

    return (
        <QueryClientProvider client={queryClient}>
            <div className={`flex h-screen bg-background text-foreground ${theme}`}>
                {/* Sidebar */}
                <aside className="w-16 border-r border-border flex flex-col items-center py-4 gap-6 bg-card/50 backdrop-blur-sm z-50">
                    <div className="w-10 h-10 bg-primary rounded-xl flex items-center justify-center text-white font-bold shadow-glow">
                        AI
                    </div>
                    <nav className="flex flex-col gap-4">
                        <button className="w-10 h-10 rounded-lg bg-accent text-accent-foreground flex items-center justify-center transition-colors hover:bg-primary/20 hover:text-primary">
                            <LayoutDashboard size={20} />
                        </button>
                        <button className="w-10 h-10 rounded-lg text-muted-foreground hover:bg-muted/50 flex items-center justify-center transition-colors">
                            <Bell size={20} />
                        </button>
                    </nav>
                    <div className="mt-auto flex flex-col gap-4">
                        <button
                            onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
                            className="w-10 h-10 rounded-lg text-muted-foreground hover:bg-muted/50 flex items-center justify-center transition-colors"
                            title="Toggle Theme"
                        >
                            {theme === 'dark' ? '🌞' : '🌙'}
                        </button>
                        <button className="w-10 h-10 rounded-lg text-muted-foreground hover:bg-muted/50 flex items-center justify-center transition-colors">
                            <Settings size={20} />
                        </button>
                    </div>
                </aside>

                {/* Main Content */}
                <main className="flex-1 flex flex-col overflow-hidden relative">
                    {/* Header */}
                    <header className="h-16 border-b border-border flex items-center justify-between px-6 bg-background/80 backdrop-blur sticky top-0 z-40">
                        <h1 className="text-xl font-bold bg-gradient-to-r from-primary to-purple-400 bg-clip-text text-transparent">
                            SRE Command Center
                        </h1>
                        <div className="flex-1 max-w-2xl mx-8">
                            <NLQuery />
                        </div>
                        <div className="flex items-center gap-4">
                            <div className="text-xs text-right hidden md:block">
                                <div className="font-medium text-foreground">John Doe</div>
                                <div className="text-muted-foreground">Lead SRE</div>
                            </div>
                            <div className="w-8 h-8 rounded-full bg-gradient-to-br from-blue-500 to-purple-600 border border-white/10" />
                        </div>
                    </header>

                    {/* Dashboard Area */}
                    <div className="flex-1 overflow-auto bg-dot-pattern p-6 relative">
                        <div className="absolute inset-0 bg-gradient-to-b from-background via-transparent to-background pointer-events-none" />
                        <DashboardGrid />
                    </div>
                </main>
            </div>
        </QueryClientProvider>
    )
}

export default App
