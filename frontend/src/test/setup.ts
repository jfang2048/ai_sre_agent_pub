import '@testing-library/jest-dom';

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
