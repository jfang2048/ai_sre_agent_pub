import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export interface LayoutItem {
    i: string;
    x: number;
    y: number;
    w: number;
    h: number;
    minW?: number;
    minH?: number;
    maxW?: number;
    maxH?: number;
    static?: boolean;
    moved?: boolean;
    isDraggable?: boolean;
    isResizable?: boolean;
    resizeHandles?: Array<'s' | 'w' | 'e' | 'n' | 'sw' | 'nw' | 'se' | 'ne'>;
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

const OVERVIEW_METRICS_MIN_HEIGHT = 5;

const DEFAULT_LAYOUT: LayoutItem[] = [
    { i: 'overview-metrics', x: 0, y: 0, w: 12, h: OVERVIEW_METRICS_MIN_HEIGHT, minH: OVERVIEW_METRICS_MIN_HEIGHT },
    { i: 'ai-insights', x: 0, y: OVERVIEW_METRICS_MIN_HEIGHT, w: 4, h: 6 },
    { i: 'topology-graph', x: 4, y: OVERVIEW_METRICS_MIN_HEIGHT, w: 4, h: 6 },
    { i: 'top-programs', x: 8, y: OVERVIEW_METRICS_MIN_HEIGHT, w: 4, h: 6 },
    { i: 'logs-feed', x: 0, y: OVERVIEW_METRICS_MIN_HEIGHT + 6, w: 12, h: 4 },
];

const DEFAULT_WIDGETS = ['overview-metrics', 'ai-insights', 'topology-graph', 'top-programs', 'logs-feed'];

function enforceOverviewMetricsHeight(layout: LayoutItem[], shiftRowsBelow: boolean): LayoutItem[] {
    const next = layout.map((item) => ({ ...item }));
    const overview = next.find((item) => item.i === 'overview-metrics');
    if (!overview) {
        return next;
    }

    const currentHeight = overview.h;
    const targetHeight = Math.max(currentHeight, OVERVIEW_METRICS_MIN_HEIGHT);
    const delta = targetHeight - currentHeight;

    overview.h = targetHeight;
    overview.minH = Math.max(overview.minH ?? 0, OVERVIEW_METRICS_MIN_HEIGHT);

    if (shiftRowsBelow && delta > 0) {
        const originalBottom = overview.y + currentHeight;
        next.forEach((item) => {
            if (item.i !== 'overview-metrics' && item.y >= originalBottom) {
                item.y += delta;
            }
        });
    }

    return next;
}

function ensureDashboardLayout(state: Partial<DashboardState>): Partial<DashboardState> {
    const widgets = [...(state.widgets ?? DEFAULT_WIDGETS)];
    let layout = [...(state.layout ?? DEFAULT_LAYOUT)];
    if (!widgets.includes('top-programs')) {
        widgets.push('top-programs');
        layout.push({ i: 'top-programs', x: 8, y: OVERVIEW_METRICS_MIN_HEIGHT, w: 4, h: 6 });
    }
    layout = enforceOverviewMetricsHeight(layout, true);
    return { ...state, widgets, layout };
}

function createDefaultLayoutItem(id: string): LayoutItem {
    return { i: id, x: 0, y: Infinity, w: 6, h: 4 };
}

export const useDashboardStore = create<DashboardState>()(
    persist(
        (set) => ({
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
            setLayout: (layout) => set({ layout: enforceOverviewMetricsHeight(layout, false) }),
            widgets: DEFAULT_WIDGETS,
            addWidget: (id) => set((s) => {
                if (s.widgets.includes(id)) {
                    return s;
                }
                return {
                    widgets: [...s.widgets, id],
                    layout: [...s.layout, createDefaultLayoutItem(id)],
                };
            }),
            removeWidget: (id) => set((s) => ({
                widgets: s.widgets.filter(w => w !== id),
                layout: s.layout.filter(l => l.i !== id)
            })),
        }),
        {
            name: 'dashboard-store',
            version: 3,
            migrate: (persisted) => ensureDashboardLayout(persisted as Partial<DashboardState>),
            onRehydrateStorage: () => (state) => {
                // Ensure top-programs is present even if storage was cleared.
                if (state) {
                    const next = ensureDashboardLayout(state);
                    state.layout = next.layout as LayoutItem[];
                    state.widgets = next.widgets as string[];
                }
            },
        }
    )
);
