import { describe, it, expect } from 'vitest';
import { useDashboardStore } from './dashboardStore';

describe('dashboard store migration', () => {
    it('includes top-programs widget by default', () => {
        const state = useDashboardStore.getState();
        expect(state.widgets).toContain('top-programs');
        expect(state.layout.some((l) => l.i === 'top-programs')).toBe(true);
    });

    it('keeps overview-metrics tall enough to avoid clipping', () => {
        const state = useDashboardStore.getState();
        const overview = state.layout.find((l) => l.i === 'overview-metrics');
        expect(overview).toBeDefined();
        expect(overview!.h).toBeGreaterThanOrEqual(5);
    });

    it('does not duplicate widgets when adding an existing widget', () => {
        const start = useDashboardStore.getState();
        const initialCount = start.widgets.filter((w) => w === 'top-programs').length;

        start.addWidget('top-programs');
        const next = useDashboardStore.getState();
        const nextCount = next.widgets.filter((w) => w === 'top-programs').length;

        expect(nextCount).toBe(initialCount);
    });
});
