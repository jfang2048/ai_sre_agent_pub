import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

const chunkGroups: Record<string, string[]> = {
    react: ['react', 'react-dom', 'react-router-dom'],
    recharts: ['recharts'],
    forcegraph: ['react-force-graph-2d'],
    motion: ['framer-motion'],
    query: ['@tanstack/react-query', 'axios'],
    dnd: ['@dnd-kit/core', '@dnd-kit/sortable'],
}

function manualChunk(id: string): string | undefined {
    for (const [chunk, packages] of Object.entries(chunkGroups)) {
        if (packages.some((packageName) => id.includes(`/node_modules/${packageName}/`))) {
            return chunk
        }
    }
    return undefined
}

// https://vitejs.dev/config/
export default defineConfig({
    plugins: [react()],
    resolve: {
        alias: {
            '@': path.resolve(import.meta.dirname, './src'),
        },
    },
    build: {
        outDir: path.resolve(import.meta.dirname, '../web'),
        emptyOutDir: true,
        rollupOptions: {
            output: {
                manualChunks: manualChunk,
            },
        },
    },
    test: {
        globals: true,
        environment: 'jsdom',
        setupFiles: './src/test/setup.ts',
    },
    server: {
        proxy: {
            '/api': {
                target: 'http://localhost:8080',
                changeOrigin: true,
            },
        }
    }
})
