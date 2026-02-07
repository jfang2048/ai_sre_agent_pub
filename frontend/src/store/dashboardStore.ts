import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export interface LayoutItem {
    i: string;
    x: number;
    y: number;
    w: number;
    h: number;
    [key: string]: any;
}

interface DashboardState {
    theme: 'dark' | 'light';
    setTheme: (theme: 'dark' | 'light') => void;
    layout: LayoutItem[];
    setLayout: (layout: LayoutItem[]) => void;
    widgets: string[];
    addWidget: (id: string) => void;
    removeWidget: (id: string) => void;
}

const DEFAULT_LAYOUT: LayoutItem[] = [
    { i: 'overview-metrics', x: 0, y: 0, w: 12, h: 3 },
    { i: 'ai-insights', x: 0, y: 3, w: 4, h: 6 },
    { i: 'topology-graph', x: 4, y: 3, w: 4, h: 6 },
    { i: 'top-programs', x: 8, y: 3, w: 4, h: 6 },
    { i: 'logs-feed', x: 0, y: 9, w: 12, h: 4 },
];

const DEFAULT_WIDGETS = ['overview-metrics', 'ai-insights', 'topology-graph', 'top-programs', 'logs-feed'];

function ensureTopPrograms(state: Partial<DashboardState>): Partial<DashboardState> {
    const widgets = [...(state.widgets ?? DEFAULT_WIDGETS)];
    const layout = [...(state.layout ?? DEFAULT_LAYOUT)];
    if (!widgets.includes('top-programs')) {
        widgets.push('top-programs');
        layout.push({ i: 'top-programs', x: 8, y: 3, w: 4, h: 6 });
    }
    return { ...state, widgets, layout };
}

export const useDashboardStore = create<DashboardState>()(
    persist(
        (set, get) => ({
            theme: 'dark',
            setTheme: (theme) => {
                const root = window.document.documentElement;
                if (theme === 'dark') {
                    root.classList.add('dark');
                } else {
                    root.classList.remove('dark');
                }
                set({ theme });
            },
            layout: DEFAULT_LAYOUT,
            setLayout: (layout) => set({ layout }),
            widgets: DEFAULT_WIDGETS,
            addWidget: (id) => set((s) => ({
                widgets: [...s.widgets, id],
                layout: [...s.layout, { i: id, x: 0, y: Infinity, w: 6, h: 4 }]
            })),
            removeWidget: (id) => set((s) => ({
                widgets: s.widgets.filter(w => w !== id),
                layout: s.layout.filter(l => l.i !== id)
            })),
        }),
        {
            name: 'dashboard-store',
            version: 2,
            migrate: (persisted) => ensureTopPrograms(persisted as Partial<DashboardState>),
            onRehydrateStorage: () => (state) => {
                // Ensure top-programs is present even if storage was cleared.
                if (state) {
                    const next = ensureTopPrograms(state);
                    state.layout = next.layout as LayoutItem[];
                    state.widgets = next.widgets as string[];
                }
            },
        }
    )
);
