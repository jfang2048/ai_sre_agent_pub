import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';

export function createTestQueryClient() {
    return new QueryClient({
        defaultOptions: {
            queries: {
                retry: false,
            },
        },
        logger: {
            log: console.log,
            warn: console.warn,
            error: () => {},
        },
    });
}

type RenderWithClientOptions = {
    width?: number;
    height?: number;
};

export function renderWithClient(ui: React.ReactElement, options: RenderWithClientOptions = {}) {
    const client = createTestQueryClient();
    const width = options.width ?? 1600;
    const height = options.height ?? 1000;
    return render(
        <div style={{ width, height }}>
            <QueryClientProvider client={client}>{ui}</QueryClientProvider>
        </div>,
    );
}
