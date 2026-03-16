import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.tsx'
import './index.css'

type PersistedDashboardStore = {
    state?: {
        theme?: 'dark' | 'light';
    };
};

function readInitialTheme(): 'dark' | 'light' {
    const raw = localStorage.getItem('dashboard-store');
    try {
        if (!raw) {
            return 'dark';
        }
        const parsed = JSON.parse(raw) as PersistedDashboardStore;
        return parsed.state?.theme === 'light' ? 'light' : 'dark';
    } catch {
        return 'dark';
    }
}

document.documentElement.classList.toggle('dark', readInitialTheme() === 'dark');

const rootElement = document.getElementById('root');
if (!rootElement) {
    throw new Error('Root element #root was not found');
}

ReactDOM.createRoot(rootElement).render(
    <React.StrictMode>
        <App />
    </React.StrictMode>,
)
