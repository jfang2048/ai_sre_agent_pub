import { describe, it, expect } from 'vitest';
import { useDashboardStore } from './dashboardStore';

describe('dashboard store migration', () => {
    it('adds top-programs widget when missing', () => {
        // simulate legacy state
        useDashboardStore.setState({
            theme: 'dark',
            widgets: ['overview-metrics'],
            layout: [{ i: 'overview-metrics', x: 0, y: 0, w: 12, h: 3 }],
            setTheme: () => {},
            setLayout: () => {},
            addWidget: () => {},
            removeWidget: () => {},
        });

        const state = useDashboardStore.getState();
        expect(state.widgets).toContain('top-programs');
        expect(state.layout.some((l) => l.i === 'top-programs')).toBe(true);
    });
});

