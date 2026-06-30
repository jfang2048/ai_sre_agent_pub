import '@testing-library/jest-dom';
import React from 'react';
import { vi } from 'vitest';

function createMemoryStorage(): Storage {
    const values = new Map<string, string>();
    return {
        get length() {
            return values.size;
        },
        clear() {
            values.clear();
        },
        getItem(key: string) {
            return values.has(key) ? values.get(key)! : null;
        },
        key(index: number) {
            return Array.from(values.keys())[index] ?? null;
        },
        removeItem(key: string) {
            values.delete(key);
        },
        setItem(key: string, value: string) {
            values.set(key, String(value));
        },
    };
}

function ensureTestStorage() {
    const storage = createMemoryStorage();
    if (typeof window !== 'undefined') {
        Object.defineProperty(window, 'localStorage', {
            configurable: true,
            value: storage,
        });
    }
    Object.defineProperty(globalThis, 'localStorage', {
        configurable: true,
        value: storage,
    });
}

ensureTestStorage();

class ResizeObserverMock {
    observe() {}
    unobserve() {}
    disconnect() {}
}

if (typeof window !== 'undefined' && !window.ResizeObserver) {
    (window as typeof window & { ResizeObserver: typeof ResizeObserverMock }).ResizeObserver = ResizeObserverMock;
}

if (typeof globalThis !== 'undefined' && !('ResizeObserver' in globalThis)) {
    (globalThis as typeof globalThis & { ResizeObserver: typeof ResizeObserverMock }).ResizeObserver = ResizeObserverMock;
}

if (typeof HTMLElement !== 'undefined') {
    const defaultRect = {
        x: 0,
        y: 0,
        top: 0,
        left: 0,
        right: 1200,
        bottom: 800,
        width: 1200,
        height: 800,
        toJSON: () => {},
    };

    Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
        configurable: true,
        get() {
            return 1200;
        },
    });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
        configurable: true,
        get() {
            return 800;
        },
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
        configurable: true,
        get() {
            return 1200;
        },
    });
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
        configurable: true,
        get() {
            return 800;
        },
    });
    HTMLElement.prototype.getBoundingClientRect = () => defaultRect as DOMRect;
}

vi.mock('recharts', async () => {
    const actual = await vi.importActual<typeof import('recharts')>('recharts');

    function placeholder(displayName: string) {
        const Component = () =>
            React.createElement('div', {
                'data-recharts-mock': displayName,
            });
        Component.displayName = displayName;
        return Component;
    }

    function noop(displayName: string) {
        const Component = () => null;
        Component.displayName = displayName;
        return Component;
    }

    return {
        ...actual,
        ResponsiveContainer: placeholder('ResponsiveContainer'),
        LineChart: placeholder('LineChart'),
        AreaChart: placeholder('AreaChart'),
        BarChart: placeholder('BarChart'),
        CartesianGrid: noop('CartesianGrid'),
        XAxis: noop('XAxis'),
        YAxis: noop('YAxis'),
        Tooltip: noop('Tooltip'),
        Legend: noop('Legend'),
        Line: noop('Line'),
        Area: noop('Area'),
        Bar: noop('Bar'),
    };
});
